$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if (-not $env:MDS_VERSION) {
    throw "Set MDS_VERSION to an exact released version."
}
if (-not $env:MDS_SHA256) {
    throw "Set MDS_SHA256 to the published archive checksum."
}
if ($env:MDS_VERSION -notmatch "^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$") {
    throw "MDS_VERSION must be an exact version without a v prefix."
}
if ($env:MDS_SHA256 -notmatch "^[0-9a-fA-F]{64}$") {
    throw "MDS_SHA256 must be exactly 64 hexadecimal characters."
}

$architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid())
$archive = Join-Path $temporary "mds.zip"
$destination = if ($env:MDS_INSTALL_DIR) {
    $env:MDS_INSTALL_DIR
}
else {
    Join-Path $env:LOCALAPPDATA "my-desk-setup\bin"
}
$archiveName = "mds_$($env:MDS_VERSION)_windows_$architecture.zip"
$baseUrl = if ($env:MDS_BASE_URL) {
    $env:MDS_BASE_URL.TrimEnd("/")
}
else {
    "https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v$($env:MDS_VERSION)"
}
$url = "$baseUrl/$archiveName"

function Get-BoundedHttpsFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Destination,
        [long]$MaximumBytes = 536870912,
        [TimeSpan]$Timeout = [TimeSpan]::FromMinutes(10),
        [object]$Handler = $null
    )
    if ($Timeout -le [TimeSpan]::Zero) {
        throw "Release download timeout must be positive."
    }
    $parsed = [System.Uri]::new($Uri)
    if ($parsed.Scheme -cne "https" -or
        -not [string]::IsNullOrEmpty($parsed.UserInfo) -or
        -not [string]::IsNullOrEmpty($parsed.Query) -or
        -not [string]::IsNullOrEmpty($parsed.Fragment)) {
        throw "Release URL must be credential-free HTTPS without query or fragment."
    }
    Add-Type -AssemblyName System.Net.Http
    $ownsHandler = $null -eq $Handler
    if ($ownsHandler) {
        $Handler = [System.Net.Http.HttpClientHandler]::new()
        $Handler.AllowAutoRedirect = $false
    }
    $client = [System.Net.Http.HttpClient]::new(
        [System.Net.Http.HttpMessageHandler]$Handler,
        $false
    )
    $client.Timeout = [System.Threading.Timeout]::InfiniteTimeSpan
    $cancellation = [System.Threading.CancellationTokenSource]::new()
    $cancellation.CancelAfter($Timeout)
    try {
        for ($redirectCount = 0; $redirectCount -le 3; $redirectCount++) {
            $response = $client.GetAsync(
                $parsed,
                [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead,
                $cancellation.Token
            ).GetAwaiter().GetResult()
            try {
                if ([int]$response.StatusCode -ge 300 -and
                    [int]$response.StatusCode -lt 400) {
                    if ($redirectCount -eq 3 -or
                        $null -eq $response.Headers.Location) {
                        throw "Release download exceeded the bounded redirect policy."
                    }
                    $next = if ($response.Headers.Location.IsAbsoluteUri) {
                        $response.Headers.Location
                    }
                    else {
                        [System.Uri]::new($parsed, $response.Headers.Location)
                    }
                    if ($next.Scheme -cne "https" -or
                        -not [string]::IsNullOrEmpty($next.UserInfo) -or
                        -not [string]::IsNullOrEmpty($next.Fragment)) {
                        throw "Release redirect must remain credential-free HTTPS."
                    }
                    $parsed = $next
                    continue
                }
                [void]$response.EnsureSuccessStatusCode()
                if ($response.Content.Headers.ContentLength -and
                    $response.Content.Headers.ContentLength -gt $MaximumBytes) {
                    throw "Release archive exceeds $MaximumBytes bytes."
                }
                $inputStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
                try {
                    $outputStream = [System.IO.File]::Open(
                        $Destination,
                        [System.IO.FileMode]::CreateNew,
                        [System.IO.FileAccess]::Write,
                        [System.IO.FileShare]::None
                    )
                    try {
                        $buffer = New-Object byte[] 81920
                        [long]$total = 0
                        while (($read = $inputStream.ReadAsync(
                            $buffer,
                            0,
                            $buffer.Length,
                            $cancellation.Token
                        ).GetAwaiter().GetResult()) -gt 0) {
                            $total += $read
                            if ($total -gt $MaximumBytes) {
                                throw "Release archive exceeds $MaximumBytes bytes."
                            }
                            $outputStream.Write($buffer, 0, $read)
                        }
                        $outputStream.Flush($true)
                    }
                    finally {
                        $outputStream.Dispose()
                    }
                }
                finally {
                    $inputStream.Dispose()
                }
                return
            }
            finally {
                $response.Dispose()
            }
        }
    }
    finally {
        $cancellation.Dispose()
        $client.Dispose()
        if ($ownsHandler) {
            $Handler.Dispose()
        }
    }
}

if ($env:MDS_BOOTSTRAP_LIBRARY_ONLY -eq "1") {
    return
}

try {
    New-Item -ItemType Directory -Path $temporary | Out-Null
    if ($env:MDS_ARCHIVE) {
        $sourceArchive = Get-Item -LiteralPath $env:MDS_ARCHIVE
        if ($sourceArchive.PSIsContainer -or
            (($sourceArchive.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)) {
            throw "MDS_ARCHIVE must be a regular, non-reparse-point file."
        }
        Copy-Item -LiteralPath $sourceArchive.FullName -Destination $archive
    }
    else {
        Get-BoundedHttpsFile -Uri $url -Destination $archive
    }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($actual -ne $env:MDS_SHA256.ToLowerInvariant()) {
        throw "Checksum mismatch: expected $($env:MDS_SHA256), got $actual"
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
    try {
        if ($zip.Entries.Count -ne 1) {
            throw "Release archive must contain exactly one entry."
        }
        $entry = $zip.Entries[0]
        $unixType = (($entry.ExternalAttributes -shr 16) -band 0xF000)
        $windowsReparse = (
            ($entry.ExternalAttributes -band [int][System.IO.FileAttributes]::ReparsePoint) -ne 0
        )
        if ($entry.FullName -cne "mds.exe" -or
            $entry.Length -le 0 -or
            $unixType -eq 0xA000 -or
            $windowsReparse) {
            throw "Release archive must contain one regular mds.exe entry."
        }
    }
    finally {
        $zip.Dispose()
    }

    Expand-Archive -Path $archive -DestinationPath $temporary
    $extracted = Get-Item -LiteralPath (Join-Path $temporary "mds.exe")
    if ($extracted.PSIsContainer -or
        (($extracted.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw "Extracted mds.exe must be a regular, non-reparse-point file."
    }
    if (Test-Path -LiteralPath $destination) {
        $destinationItem = Get-Item -LiteralPath $destination
        if (($destinationItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Refusing to install through a reparse-point directory."
        }
    }
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    $installed = Join-Path $destination "mds.exe"
    if (Test-Path -LiteralPath $installed) {
        $installedItem = Get-Item -LiteralPath $installed
        if (($installedItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Refusing to replace a reparse-point mds.exe."
        }
    }
    Copy-Item -Force -LiteralPath $extracted.FullName -Destination $installed

    $managedPaths = @(
        $destination
        (Join-Path $env:USERPROFILE ".local\\bin")
        (Join-Path $env:USERPROFILE ".local\\share\\bun\\bin")
    )
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $pathEntries = @($userPath -split ";" | Where-Object { $_ })
    foreach ($managedPath in $managedPaths) {
        if ($pathEntries -notcontains $managedPath) {
            $pathEntries += $managedPath
        }
    }
    [Environment]::SetEnvironmentVariable("Path", ($pathEntries -join ";"), "User")
    [Environment]::SetEnvironmentVariable("DISABLE_AUTOUPDATER", "1", "User")
    [Environment]::SetEnvironmentVariable("OPENCODE_DISABLE_AUTOUPDATE", "1", "User")
    $env:Path = (($managedPaths + @($env:Path)) -join ";")
    $env:DISABLE_AUTOUPDATER = "1"
    $env:OPENCODE_DISABLE_AUTOUPDATE = "1"
}
finally {
    if (Test-Path $temporary) {
        Remove-Item -Recurse -Force $temporary
    }
}

Write-Host "Installed mds $($env:MDS_VERSION). Authentication remains a manual user action."
& $installed --version
if ($env:MDS_PLAN_SMOKE -eq "1") {
    & $installed plan --profile owner --format json
}
Write-Host "Next: $destination\mds.exe plan --profile owner"
