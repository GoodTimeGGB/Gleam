# ============================================================
# Gleam install script (Windows PowerShell)
# Usage: powershell -ExecutionPolicy Bypass -File scripts\install.ps1
#
# Flow:
#   1. Detect Go -> skip if found
#   2. Not found -> auto-download & install Go (or user specifies folder)
#   3. Build Gleam executable
#   4. Write PATH, output startup guide
# ============================================================

[CmdletBinding()]
param(
    [string]$GoPath = "",
    [string]$InstallDir = "",
    [switch]$SkipBuild,
    [switch]$ForceGoInstall,
    [string]$GoVersion = "1.23.4"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Write-Ok($msg)  { Write-Host "  [OK] $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "  [!]  $msg" -ForegroundColor Yellow }
function Write-Err($msg) { Write-Host "  [X]  $msg" -ForegroundColor Red }

$script:stepNum = 0
$script:stepTotal = 4
function Step($msg) {
    $script:stepNum++
    Write-Host ""
    Write-Host "[$script:stepNum/$script:stepTotal] $msg" -ForegroundColor Cyan
}

# ============================================================
# 1. Detect Go
# ============================================================
Step "Detect Go toolchain"

function Find-Go {
    $goExe = Get-Command go -ErrorAction SilentlyContinue
    if ($goExe) {
        $v = & $goExe.Source version 2>&1
        Write-Ok "Found in PATH: $($goExe.Source)"
        Write-Host "      $v"
        return @{ Found = $true; BinDir = Split-Path $goExe.Source; Root = Split-Path (Split-Path $goExe.Source) }
    }
    $candidates = @(
        "C:\Go\bin\go.exe",
        "C:\Program Files\Go\bin\go.exe",
        "C:\Program Files (x86)\Go\bin\go.exe",
        "$env:LOCALAPPDATA\Go\bin\go.exe",
        "$env:USERPROFILE\go\bin\go.exe"
    )
    foreach ($p in $candidates) {
        if (Test-Path $p) {
            $v = & $p version 2>&1
            Write-Ok "Found at: $p"
            Write-Host "      $v"
            $bin = Split-Path $p
            return @{ Found = $true; BinDir = $bin; Root = Split-Path $bin }
        }
    }
    $goroot = $env:GOROOT
    if ($goroot -and (Test-Path "$goroot\bin\go.exe")) {
        $v = & "$goroot\bin\go.exe" version 2>&1
        Write-Ok "Found via GOROOT: $goroot"
        Write-Host "      $v"
        return @{ Found = $true; BinDir = "$goroot\bin"; Root = $goroot }
    }
    return @{ Found = $false }
}

function Install-Go {
    Write-Host ""
    Write-Host "  Go not detected, starting guided install..." -ForegroundColor Yellow

    if ($GoPath -and (Test-Path "$GoPath\bin\go.exe")) {
        Write-Ok "User-specified path valid: $GoPath"
        return @{ Found = $true; BinDir = "$GoPath\bin"; Root = $GoPath }
    }
    if ($GoPath -and (Test-Path "$GoPath\go.exe")) {
        Write-Ok "User-specified path valid: $GoPath"
        return @{ Found = $true; BinDir = $GoPath; Root = Split-Path $GoPath }
    }
    if ($GoPath) {
        Write-Warn2 "Specified path invalid: $GoPath, will try auto-download"
    }

    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
    $url = "https://mirrors.aliyun.com/golang/go$GoVersion.windows-$arch.zip"
    $dest = "$env:TEMP\go-$GoVersion.zip"

    Write-Host "  Downloading Go $GoVersion ($arch)..."
    Write-Host "  Source: $url"

    $js = 'const https=require("https");const http=require("http");const fs=require("fs");const destPath=process.argv[2];const durl=process.argv[3];function fetch(url,redirects){if(redirects>5){console.error("too many redirects");process.exit(1);}const mod=url.startsWith("https")?https:http;const req=mod.get(url,{headers:{"User-Agent":"node"},timeout:300000},(res)=>{if(res.statusCode>=300&&res.statusCode<400&&res.headers.location){res.resume();fetch(res.headers.location,redirects+1);return;}if(res.statusCode!==200){console.error("HTTP "+res.statusCode);process.exit(1);}const total=parseInt(res.headers["content-length"]||0);let received=0;const f=fs.createWriteStream(destPath);res.on("data",(chunk)=>{received+=chunk.length;if(total)process.stderr.write("\r  "+Math.round(received/total*100)+"%");});res.pipe(f);f.on("finish",()=>{f.close();process.stderr.write("\n");console.log("done "+received+" bytes");});});req.on("error",(e)=>{console.error(e.message);process.exit(1);});req.on("timeout",()=>{console.error("timeout");req.destroy();process.exit(1);});}fetch(durl,0);'
    Set-Content -Path "$env:TEMP\gleam-dl.js" -Value $js -Encoding UTF8

    try {
        node "$env:TEMP\gleam-dl.js" $dest $url 2>&1 | Out-Host
    } catch {
        Write-Err "Download failed: $_"
        Write-Host ""
        Write-Host "  Please install Go manually:" -ForegroundColor Yellow
        Write-Host "    1. Visit https://go.dev/dl/ to download Windows installer"
        Write-Host "    2. Run the .msi installer (default: C:\Program Files\Go)"
        Write-Host "    3. Re-run: install.ps1 -GoPath 'C:\Program Files\Go'"
        exit 1
    }

    if (-not (Test-Path $dest)) {
        Write-Err "Downloaded file not found"
        exit 1
    }

    Write-Host "  Extracting..." -ForegroundColor Yellow
    $goRoot = "C:\Go"
    if (Test-Path $goRoot) { Remove-Item $goRoot -Recurse -Force }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::ExtractToDirectory($dest, "C:\")
    Remove-Item $dest -Force

    Write-Ok "Go $GoVersion installed to $goRoot"
    & "$goRoot\bin\go.exe" version

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$goRoot\bin*") {
        [Environment]::SetEnvironmentVariable("Path", "$goRoot\bin;$userPath", "User")
        Write-Ok "Added $goRoot\bin to user PATH"
    }
    $env:Path = "$goRoot\bin;$env:Path"
    $env:GOPROXY = "https://goproxy.cn,direct"
    [Environment]::SetEnvironmentVariable("GOPROXY", "https://goproxy.cn,direct", "User")
    Write-Ok "Set GOPROXY=https://goproxy.cn,direct"

    return @{ Found = $true; BinDir = "$goRoot\bin"; Root = $goRoot }
}

$go = Find-Go
if (-not $go.Found) {
    $go = Install-Go
    if (-not $go.Found) { exit 1 }
} elseif ($ForceGoInstall) {
    Write-Warn2 "Force reinstalling Go"
    $go = Install-Go
}

if ($env:Path -notlike "*$($go.BinDir)*") {
    $env:Path = "$($go.BinDir);$env:Path"
}
if (-not $env:GOPROXY) { $env:GOPROXY = "https://goproxy.cn,direct" }

Write-Ok "Go ready: $($go.BinDir)"

# ============================================================
# 2. Install directory
# ============================================================
Step "Setup Gleam install directory"

if (-not $InstallDir) { $InstallDir = "C:\Gleam" }
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}
Write-Ok "Install dir: $InstallDir"

# ============================================================
# 3. Build Gleam
# ============================================================
if ($SkipBuild) {
    Step "Skipping build (-SkipBuild)"
    $src = Join-Path $PSScriptRoot "..\gleam.exe"
    if (-not (Test-Path $src)) { $src = Join-Path $PSScriptRoot "..\bin\gleam.exe" }
    if (Test-Path $src) {
        Copy-Item $src (Join-Path $InstallDir "gleam.exe") -Force
        Write-Ok "Copied pre-built binary to $InstallDir\gleam.exe"
    } else {
        Write-Warn2 "No pre-built binary found, remove -SkipBuild and retry"
    }
} else {
    Step "Build Gleam"

    $projectRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
    Push-Location $projectRoot

    Write-Host "  go vet..." -ForegroundColor DarkGray
    & go vet ./... 2>&1 | Out-Host

    Write-Host "  go build..." -ForegroundColor DarkGray
    & go build -trimpath -ldflags="-s -w" -o (Join-Path $InstallDir "gleam.exe") ./cmd/gleam 2>&1 | Out-Host
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Build failed"
        Pop-Location
        exit 1
    }

    & go build -trimpath -ldflags="-s -w -H=windowsgui" -o (Join-Path $InstallDir "GleamDesktop.exe") ./cmd/gleam 2>&1 | Out-Host

    Pop-Location
    Write-Ok "Build complete"
}

# ============================================================
# 4. Configure PATH and data dir
# ============================================================
Step "Configure environment"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$userPath", "User")
    Write-Ok "Added $InstallDir to user PATH"
}

$dataDir = Join-Path $env:USERPROFILE ".gleam"
if (-not (Test-Path $dataDir)) {
    New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    Write-Ok "Created data dir: $dataDir"
} else {
    Write-Ok "Data dir exists: $dataDir"
}

# ============================================================
# Done
# ============================================================
Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "  Gleam installed successfully!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "Launch:" -ForegroundColor Cyan
Write-Host "  Desktop:  Double-click $InstallDir\GleamDesktop.exe"
Write-Host "  CLI:      gleam webui             (Web UI)"
Write-Host "  CLI:      gleam app               (Desktop mode)"
Write-Host "  CLI:      gleam goal "your goal"   (quick run)"
Write-Host ""
Write-Host "Note: PATH updated, please reopen terminal to use gleam command." -ForegroundColor Yellow
Write-Host ""
Write-Host "Data dir: $dataDir"
Write-Host "Install dir: $InstallDir"
Write-Host ""

if (-not $SkipBuild) {
    Write-Host "Launch now?" -ForegroundColor Cyan
    $choice = Read-Host "  [Y/n]"
    if ($choice -eq "" -or $choice -match "^[Yy]") {
        Start-Process (Join-Path $InstallDir "GleamDesktop.exe")
    }
}
