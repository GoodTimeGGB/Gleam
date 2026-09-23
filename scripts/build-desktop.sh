#!/usr/bin/env bash
# 构建 Gleam 多平台可执行程序（纯 Go 零 cgo，可在任意平台交叉编译）：
#   bin/gleam.exe               Windows 控制台 CLI（含图标）
#   bin/GleamDesktop.exe        Windows 桌面版（原生 WebView2 窗口 + 托盘常驻 + 图标）
#   bin/gleam-darwin-amd64      macOS Intel（终端运行 ./gleam-darwin-amd64 app）
#   bin/gleam-darwin-arm64      macOS Apple Silicon (M 系列)
#   bin/gleam-linux-amd64       Linux x86_64
#
# 图标资源：cmd/gleam/rsrc_windows_amd64.syso（已提交，链接时自动嵌入 Windows 构建）。
# 重新生成图标：go run scripts/make-icon.go && rsrc -ico assets/gleam.ico -arch amd64 -o cmd/gleam/rsrc_windows_amd64.syso
set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p bin
LDFLAGS_COMMON="-s -w"

echo "[1/4] go vet ./..."
go vet ./...

echo "[2/4] Windows（控制台 CLI + 桌面版 windowsgui）"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam.exe ./cmd/gleam
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON -H=windowsgui" -o bin/GleamDesktop.exe ./cmd/gleam

echo "[3/4] macOS（Intel amd64 + Apple Silicon arm64）"
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-darwin-amd64 ./cmd/gleam
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-darwin-arm64 ./cmd/gleam

echo "[4/4] Linux x86_64"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-linux-amd64 ./cmd/gleam

ls -la bin/
echo "完成。macOS 首次运行需在终端执行（例：chmod +x gleam-darwin-arm64 && ./gleam-darwin-arm64 app），"
echo "未签名二进制首次打开需在「系统设置 → 隐私与安全性」中允许。"
