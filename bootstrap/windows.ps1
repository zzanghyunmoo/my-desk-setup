$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if (-not $env:MDS_VERSION) {
    throw "Set MDS_VERSION to an exact released version."
}
if (-not $env:MDS_SHA256) {
    throw "Set MDS_SHA256 to the published archive checksum."
}

$architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid())
$archive = Join-Path $temporary "mds.zip"
$destination = Join-Path $env:LOCALAPPDATA "my-desk-setup\bin"
$url = "https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v$($env:MDS_VERSION)/mds_$($env:MDS_VERSION)_windows_$architecture.zip"

try {
    New-Item -ItemType Directory -Path $temporary | Out-Null
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $archive
    $actual = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($actual -ne $env:MDS_SHA256.ToLowerInvariant()) {
        throw "Checksum mismatch: expected $($env:MDS_SHA256), got $actual"
    }
    Expand-Archive -Path $archive -DestinationPath $temporary
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    Copy-Item -Force (Join-Path $temporary "mds.exe") (Join-Path $destination "mds.exe")

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
Write-Host "Next: $destination\mds.exe plan --profile owner"
