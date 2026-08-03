param(
    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$version = "8.30.1"
$architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "x64" }
    "ARM64" { "arm64" }
    default { throw "unsupported Windows architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$expectedSHA256 = switch ($architecture) {
    "x64" { "d29144deff3a68aa93ced33dddf84b7fdc26070add4aa0f4513094c8332afc4e" }
    "arm64" { "b95f5e4f5c425cedca7ee203d9afd29597e692c4924a12ed42f970537c72cc0f" }
}
$url = "https://github.com/gitleaks/gitleaks/releases/download/v$version/gitleaks_${version}_windows_${architecture}.zip"
$temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) ("mds-gitleaks-" + [Guid]::NewGuid())
$archive = Join-Path $temporaryDirectory "gitleaks.zip"
$expanded = Join-Path $temporaryDirectory "expanded"

try {
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    $response = Invoke-WebRequest `
        -Uri $url `
        -OutFile $archive `
        -MaximumRedirection 3 `
        -TimeoutSec 600 `
        -UseBasicParsing `
        -PassThru
    $baseResponse = $response.BaseResponse
    $responseUriProperty = $baseResponse.PSObject.Properties["ResponseUri"]
    $requestMessageProperty = $baseResponse.PSObject.Properties["RequestMessage"]
    if ($null -ne $responseUriProperty) {
        $effectiveUri = $responseUriProperty.Value
    }
    elseif ($null -ne $requestMessageProperty) {
        $effectiveUri = $requestMessageProperty.Value.RequestUri
    }
    else {
        throw "Gitleaks download did not expose its effective URI"
    }
    if ($effectiveUri.Scheme -ne "https" -or
        -not [string]::IsNullOrEmpty($effectiveUri.UserInfo)) {
        throw "Gitleaks download did not preserve a credential-free HTTPS URL"
    }
    if ((Get-Item -LiteralPath $archive).Length -gt 67108864) {
        throw "Gitleaks archive exceeds the 64 MiB limit"
    }
    $actualSHA256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
    if ($actualSHA256 -ne $expectedSHA256) {
        throw "Gitleaks archive SHA-256 mismatch"
    }

    Expand-Archive -LiteralPath $archive -DestinationPath $expanded
    $executables = @(
        Get-ChildItem -LiteralPath $expanded -Recurse -File -Filter "gitleaks.exe"
    )
    if ($executables.Count -ne 1) {
        throw "Gitleaks archive must contain exactly one executable"
    }
    if (($executables[0].Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Gitleaks executable must not be a reparse point"
    }

    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    $destination = Join-Path $OutputDirectory "gitleaks.exe"
    Copy-Item -LiteralPath $executables[0].FullName -Destination $destination -Force
    & $destination version
}
finally {
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
    }
}
