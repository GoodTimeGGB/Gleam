# 生成 Gleam 应用图标（Windows 入口）。
# 图形只有一个来源：assets/gleam-logo.svg 里的折角标记；栅格化在 scripts/make-icon.go 里，
# 这里不再自己画图，免得两份图形走样。
# 产物：assets/gleam.ico、assets/icons/*.png、assets/gleam.icns；
# 若装了 rsrc（go install github.com/akavel/rsrc@v0.10.2），顺手刷新 exe 内嵌图标资源。
$ErrorActionPreference = 'Stop'
Push-Location "$PSScriptRoot\.."
try {
    go run scripts/make-icon.go
    if (Get-Command rsrc -ErrorAction SilentlyContinue) {
        rsrc -ico assets/gleam.ico -arch amd64 -o cmd/gleam/rsrc_windows_amd64.syso
    } else {
        Write-Host "提示：未找到 rsrc，cmd/gleam/rsrc_windows_amd64.syso 没有刷新"
    }
    Write-Host "OK: $((Get-Item assets\gleam.ico).Length) bytes"
} finally {
    Pop-Location
}
