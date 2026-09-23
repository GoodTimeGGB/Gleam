# ============================================================
# Go toolchain detection & guided install (standalone)
# Usage: powershell -ExecutionPolicy Bypass -File scripts\go-check.ps1
#        powershell -ExecutionPolicy Bypass -File scripts\go-check.ps1 -GoPath "C:\Go"
# ============================================================

[CmdletBinding()]
param(
    [string]$GoPath = "",
    [string]$GoVersion = "1.23.4",
    [switch]$Install,
    [switch]$Quiet
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Write-Ok($msg)  { if (-not $Quiet) { Write-Host "  [OK] $msg" -ForegroundColor Green } }
function Write-Warn2($msg) { if (-not $Quiet) { Write-Host "  [!]  $msg" -ForegroundColor Yellow } }
function Write-Err($msg) { Write-Host "  [X]  $msg" -ForegroundColor Red }

function Find-Go {
    $goExe = Get-Command go -ErrorAction SilentlyContinue
    if ($goExe) {
        return @{ Found = $true; Path = $goExe.Source; Version = (& $goExe.Source version 2>&1); BinDir = Split-Path $goExe.Source }
    }
    $candidates = @(
        "C:\Go\bin\go.exe",
        "C:\Program Files\Go\bin\go.exe",
        "C:\Program Files (x86)\Go\bin\go.exe",
        "$env:LOCALAPPDATA\Go\bin\go.exe"
    )
    foreach ($p in $candidates) {
        if (Test-Path $p) {
            return @{ Found = $true; Path = $p; Version = (& $p version 2>&1); BinDir = Split-Path $p }
        }
    }
    if ($env:GOROOT -and (Test-Path "$env:GOROOT\bin\go.exe")) {
        $p = "$env:GOROOT\bin\go.exe"
        return @{ Found = $true; Path = $p; Version = (& $p version 2>&1); BinDir = "$env:GOROOT\bin" }
    }
    return @{ Found = $false }
}

function Download-Go {
    param([string]$Version, [string]$Dest)
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
    $url = "https://mirrors.aliyun.com/golang/go$Version.windows-$arch.zip"
    if (-not $Quiet) { Write-Host "  Downloading Go $Version ($arch) from $url ..." -ForegroundColor Yellow }
    $dl = 'const https=require("https");const http=require("http");const fs=require("fs");const d=process.argv[2];const u=process.argv[3];function f(url,r){if(r>5){process.exit(1);}const m=url.startsWith("https")?https:http;const q=m.get(url,{headers:{"User-Agent":"node"},timeout:300000},s=>{if(s.statusCode>=300&&s.statusCode<400&&s.headers.location){s.resume();f(s.headers.location,r+1);return;}if(s.statusCode!==200){process.exit(1);}const t=parseInt(s.headers["content-length"]||0);let rec=0;const fl=fs.createWriteStream(d);s.on("data",c=>{rec+=c.length;if(t)process.stderr.write("\r  "+Math.round(rec/t*100)+"%");});s.pipe(fl);fl.on("finish",()=>{fl.close();process.stderr.write("\n");});});q.on("error",()=>process.exit(1));q.on("timeout",()=>{q.destroy();process.exit(1);});}f(u,0);'
    Set-Content -Path "$env:TEMP\go-dl.js" -Value $dl -Encoding UTF8
    node "$env:TEMP\go-dl.js" $Dest $url 2>&1 | Out-Null
    return (Test-Path $Dest)
}

function Install-GoAuto {
    param([string]$Version)
    $dest = "$env:TEMP\go-$Version.zip"
    if (-not (Download-Go -Version $Version -Dest $dest)) {
        Write-Err "Download failed"
        return $false
    }
    $goRoot = "C:\Go"
    if (Test-Path $goRoot) { Remove-Item $goRoot -Recurse -Force }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::ExtractToDirectory($dest, "C:\")
    Remove-Item $dest -Force
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$goRoot\bin*") {
        [Environment]::SetEnvironmentVariable("Path", "$goRoot\bin;$userPath", "User")
    }
    [Environment]::SetEnvironmentVariable("GOPROXY", "https://goproxy.cn,direct", "User")
    $env:Path = "$goRoot\bin;$env:Path"
    $env:GOPROXY = "https://goproxy.cn,direct"
    Write-Ok "Go $Version installed to $goRoot"
    & "$goRoot\bin\go.exe" version
    return $true
}

# Main
$go = Find-Go
if ($go.Found) {
    Write-Ok "Go detected: $($go.Path)"
    Write-Host "  $($go.Version)"
    Write-Host "  BinDir: $($go.BinDir)"
    if (-not $Quiet) { Write-Host "GO_FOUND=$($go.Path)" }
    exit 0
}

if ($GoPath -and (Test-Path "$GoPath\bin\go.exe")) {
    $env:Path = "$GoPath\bin;$env:Path"
    $v = & "$GoPath\bin\go.exe" version 2>&1
    Write-Ok "Go found at user-specified path: $GoPath"
    Write-Host "  $v"
    Write-Host "GO_FOUND=$GoPath\bin\go.exe"
    exit 0
}

Write-Warn2 "Go not found"
if (-not $Install -and -not $Quiet) {
    Write-Host ""
    Write-Host "Options:" -ForegroundColor Cyan
    Write-Host "  1. Auto-download and install Go $GoVersion"
    Write-Host "  2. I already have Go, let me specify the folder"
    Write-Host "  3. Exit"
    $choice = Read-Host "Choose [1/2/3]"
    if ($choice -eq "1") { $Install = $true }
    elseif ($choice -eq "2") {
        $folder = Read-Host "Enter Go installation folder (e.g. C:\Program Files\Go)"
        if (Test-Path "$folder\bin\go.exe") {
            $env:Path = "$folder\bin;$env:Path"
            $v = & "$folder\bin\go.exe" version 2>&1
            Write-Ok "Go found: $v"
            Write-Host "GO_FOUND=$folder\bin\go.exe"
            exit 0
        } else {
            Write-Err "go.exe not found in $folder\bin"
            exit 1
        }
    } else { exit 1 }
}

if ($Install) {
    if (Install-GoAuto -Version $GoVersion) {
        Write-Host "GO_INSTALLED=C:\Go\bin\go.exe"
        exit 0
    } else {
        exit 1
    }
}

Write-Host "GO_FOUND="
exit 1
