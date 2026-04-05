#!/usr/bin/env pwsh
# install.ps1 - Install aipack on Windows (~/.local/bin)
# Usage: irm https://raw.githubusercontent.com/shrug-labs/aipack/main/install.ps1 | iex
# Optional env:
#   AIPACK_VERSION=latest|vX.Y.Z|X.Y.Z (default: latest)

$ErrorActionPreference = 'Stop'

$repo = "shrug-labs/aipack"
$binaryName = "aipack"
$requestedVersion = $(if ($env:AIPACK_VERSION) { $env:AIPACK_VERSION } else { "latest" })

function Normalize-Tag([string]$version) {
    if (-not $version -or $version -eq "latest") {
        return "latest"
    }
    if ($version.StartsWith("v")) {
        return $version
    }
    return "v$version"
}

function Get-LatestVersion {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'aipack-installer' }
    return $release.tag_name
}

function Get-InstallDir {
    $dir = Join-Path $HOME ".local\bin"
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    return $dir
}

function Install-Aipack {
    $tag = Normalize-Tag $requestedVersion
    if ($tag -eq "latest") {
        Write-Host "Detecting latest aipack release..." -ForegroundColor Cyan
        $tag = Get-LatestVersion
        Write-Host "Latest version: $tag"
    } else {
        Write-Host "Using requested version: $tag" -ForegroundColor Cyan
    }

    $arch = $(if ($env:PROCESSOR_ARCHITECTURE -eq 'AMD64') { 'amd64' } else { 'arm64' })
    $asset = "$binaryName-windows-$arch.exe"
    $url = "https://github.com/$repo/releases/download/$tag/$asset"
    $sumsUrl = "https://github.com/$repo/releases/download/$tag/SHA256SUMS"

    $installDir = Get-InstallDir
    $dest = Join-Path $installDir "$binaryName.exe"
    $tmpFile = Join-Path $installDir ".aipack-download.exe"

    Write-Host "Downloading $url..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $url -OutFile $tmpFile -UseBasicParsing

    # Verify checksum
    Write-Host "Verifying checksum..." -ForegroundColor Cyan
    $resp = Invoke-WebRequest -Uri $sumsUrl -UseBasicParsing
    # .Content may be byte[] in PS 5.1 depending on content-type; coerce to string.
    if ($resp.Content -is [byte[]]) {
        $sums = [System.Text.Encoding]::UTF8.GetString($resp.Content)
    } else {
        $sums = $resp.Content
    }
    $expectedHash = ($sums -split '[\r\n]+' | Where-Object { $_ -like "*$asset*" } | ForEach-Object { ($_ -split '\s+')[0] })
    if (-not $expectedHash) {
        Remove-Item $tmpFile -Force
        throw "No checksum found for $asset"
    }
    $actualHash = (Get-FileHash -Path $tmpFile -Algorithm SHA256).Hash.ToLower()
    if ($actualHash -ne $expectedHash) {
        Remove-Item $tmpFile -Force
        throw "Checksum mismatch: expected $expectedHash, got $actualHash"
    }

    # Replace binary
    if (Test-Path $dest) {
        $oldPath = "$dest.old"
        if (Test-Path $oldPath) { Remove-Item $oldPath -Force }
        Rename-Item $dest $oldPath -Force
    }
    Rename-Item $tmpFile $dest -Force

    Write-Host "Installed $binaryName $tag to $dest" -ForegroundColor Green

    # Add to PATH if not already present
    $userPath = [System.Environment]::GetEnvironmentVariable('PATH', 'User')
    if ($userPath -notlike "*$installDir*") {
        Write-Host "Adding $installDir to user PATH..." -ForegroundColor Cyan
        [System.Environment]::SetEnvironmentVariable('PATH', "$userPath;$installDir", 'User')
        $env:PATH = "$env:PATH;$installDir"
        Write-Host "Added to PATH. Restart your terminal for it to take effect." -ForegroundColor Yellow
    }

    Write-Host ""
    Write-Host "Run 'aipack version' to verify the installation." -ForegroundColor Cyan
}

Install-Aipack
