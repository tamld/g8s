# ==============================================================================
# g8s (The Gatekeepers) — Windows PowerShell Standalone Installer
# Zero-Trust SHA-256 Checksum Verified Release Installer
#
# Usage:
#   irm https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.ps1 | iex
#   $env:G8S_VERSION="v0.20.0"; .\scripts\install.ps1
# ==============================================================================
$ErrorActionPreference = "Stop"

$Repo = if ($env:G8S_REPO) { $env:G8S_REPO } else { "tamld/g8s" }
$Version = if ($env:G8S_VERSION) { $env:G8S_VERSION } else { "latest" }
$InstallDir = if ($env:G8S_INSTALL_DIR) { $env:G8S_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\g8s\bin" }

$ArchRaw = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
$Arch = switch ($ArchRaw) {
    "x64"   { "amd64" }
    "amd64" { "amd64" }
    "arm64" { "arm64" }
    default { throw "Unsupported Windows architecture: $ArchRaw (supported: amd64, arm64)" }
}

if ($Version -eq "latest") {
    Write-Host "[g8s-install] Resolving latest release from https://github.com/$Repo ..." -ForegroundColor Cyan
    $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $Release.tag_name
    if (-not $Version) {
        throw "Could not resolve latest release tag from GitHub API. Set G8S_VERSION=vX.Y.Z."
    }
}

$VersionNum = $Version.TrimStart("v")
$BaseUrl = "https://github.com/$Repo/releases/download/$Version"
$Archive = "g8s_${VersionNum}_windows_${Arch}.zip"
$ChecksumsUrl = "$BaseUrl/checksums.txt"

$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("g8s-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

try {
    $ArchivePath = Join-Path $TmpDir $Archive
    $ChecksumsPath = Join-Path $TmpDir "checksums.txt"

    Write-Host "[g8s-install] Downloading $Archive ($Version) ..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri "$BaseUrl/$Archive" -OutFile $ArchivePath

    Write-Host "[g8s-install] Downloading checksums.txt ..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $ChecksumsPath

    Write-Host "[g8s-install] Verifying SHA-256 checksum (fail-closed) ..." -ForegroundColor Cyan
    $ExpectedLine = Get-Content $ChecksumsPath | Where-Object { $_ -match "\s+\*?$([Regex]::Escape($Archive))$" } | Select-Object -First 1
    if (-not $ExpectedLine) {
        throw "Archive $Archive not found in checksums.txt!"
    }
    $ExpectedSha = ($ExpectedLine -split "\s+")[0].ToLowerInvariant()
    $ActualSha = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()

    if ($ExpectedSha -ne $ActualSha) {
        throw "SHA-256 mismatch for $Archive! Expected $ExpectedSha, got $ActualSha"
    }
    Write-Host "[g8s-install] SHA-256 verified: $ActualSha" -ForegroundColor Green

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    $BinPath = Join-Path $TmpDir "g8s.exe"
    if (-not (Test-Path $BinPath)) {
        throw "g8s.exe not found inside $Archive!"
    }
    $TargetBin = Join-Path $InstallDir "g8s.exe"
    Copy-Item -Path $BinPath -Destination $TargetBin -Force
    Write-Host "[g8s-install] Installed g8s.exe to $TargetBin" -ForegroundColor Green

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not ($UserPath -split ";" | Where-Object { $_ -eq $InstallDir })) {
        [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$UserPath", "User")
        $env:Path = "$InstallDir;$env:Path"
        Write-Host "[g8s-install] Added $InstallDir to User PATH." -ForegroundColor Green
    }

    & $TargetBin --version
} finally {
    Remove-Item -Path $TmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
