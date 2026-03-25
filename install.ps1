#!/usr/bin/env pwsh
# install.ps1 - Install aipack on Windows
# Usage: irm https://raw.githubusercontent.com/shrug-labs/aipack/main/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$repo = "shrug-labs/aipack"
$binaryName = "aipack"

function Get-LatestVersion {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'aipack-installer' }
    return $release.tag_name -replace '^v', ''
}

function Get-InstallDir {
    $dir = Join-Path $env:LOCALAPPDATA "aipack" "bin"
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    return $dir
}

function Install-Aipack {
    Write-Host "Detecting latest aipack release..." -ForegroundColor Cyan
    $version = Get-LatestVersion
    Write-Host "Latest version: $version"

    $arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'X64') { 'amd64' } else { 'arm64' }
    $asset = "$binaryName-windows-$arch.exe"
    $tag = "v$version"
    $url = "https://github.com/$repo/releases/download/$tag/$asset"
    $sumsUrl = "https://github.com/$repo/releases/download/$tag/SHA256SUMS"

    $installDir = Get-InstallDir
    $dest = Join-Path $installDir "$binaryName.exe"
    $tmpFile = Join-Path $installDir ".aipack-download.exe"

    Write-Host "Downloading $url..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $url -OutFile $tmpFile -UseBasicParsing

    # Verify checksum
    Write-Host "Verifying checksum..." -ForegroundColor Cyan
    $sums = (Invoke-WebRequest -Uri $sumsUrl -UseBasicParsing).Content
    $expectedHash = ($sums -split "`n" | Where-Object { $_ -match $asset } | ForEach-Object { ($_ -split '\s+')[0] })
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

    Write-Host "Installed $binaryName $version to $dest" -ForegroundColor Green

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
