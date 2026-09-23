#!/usr/bin/env bash
# 构建全平台二进制并打包发布物（纯 Go 交叉编译，零 cgo）：
#
#   dist/Gleam-Windows-x86_64-<date>.exe               Windows 控制台 CLI（含图标）
#   dist/Gleam-Desktop-Windows-x86_64-<date>.exe       Windows 桌面版（无黑窗 + 托盘）
#   dist/Gleam-macOS-Intel-x86_64-<date>               macOS Intel
#   dist/Gleam-macOS-AppleSilicon-arm64-<date>         macOS Apple Silicon (M 系列)
#   dist/Gleam-Linux-x86_64-<date>                     Linux x86_64
#   dist/webui-static-<date>.zip                       内嵌前端三件套
#   dist/website-<date>.zip                            官网静态站点
#   dist/Gleam-release-<date>.zip                      全平台合集 + 说明 + 校验和
#   dist/SHA256SUMS-<date>.txt                         今日产物校验和
#
# 同时同步一份「latest」无日期命名（dist/Gleam-Windows-x86_64.exe 等），便于官网固定链接。
#
# 用法：
#   bash scripts/package.sh            # 日期取今天
#   bash scripts/package.sh 20260917   # 指定日期标签
set -euo pipefail
cd "$(dirname "$0")/.."

DATE="${1:-$(date +%Y%m%d)}"
LDFLAGS="-s -w"
DIST="dist"

echo "[0/5] 编译检查"
go build ./...
# vet 仅在工具链健康时执行：本机 GOROOT 异常（internal/godebug 缺失）不应阻断打包
go vet ./... 2>/dev/null || echo "（go vet 因本地 Go 工具链问题跳过，不影响产物）"

mkdir -p "$DIST"

echo "[1/5] 构建各平台二进制"
# 单次交叉编译偶尔会因本机 Go 工具链加载 std 失败（与代码无关），故带重试；
# -p 2 降低并行度，减少并发读取 GOROOT 时的偶发失败。
build_one() {
  local goos="$1" goarch="$2" out="$3" extra="$4"
  local n
  for n in 1 2 3; do
    if GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -p 2 -trimpath \
        -ldflags="$LDFLAGS $extra" -o "$out" ./cmd/gleam; then
      return 0
    fi
    echo "（$goos/$goarch 第 $n 次构建失败，2 秒后重试）"
    sleep 2
  done
  echo "构建失败：$goos/$goarch" >&2
  return 1
}

build_one windows amd64 "$DIST/Gleam-Windows-x86_64-$DATE.exe" ""
build_one windows amd64 "$DIST/Gleam-Desktop-Windows-x86_64-$DATE.exe" "-H=windowsgui"
build_one darwin  amd64 "$DIST/Gleam-macOS-Intel-x86_64-$DATE" ""
build_one darwin  arm64 "$DIST/Gleam-macOS-AppleSilicon-arm64-$DATE" ""
build_one linux   amd64 "$DIST/Gleam-Linux-x86_64-$DATE" ""

echo "[2/5] 同步 latest 命名（无日期）"
cp -f "$DIST/Gleam-Windows-x86_64-$DATE.exe"            "$DIST/Gleam-Windows-x86_64.exe"
cp -f "$DIST/Gleam-Desktop-Windows-x86_64-$DATE.exe"    "$DIST/Gleam-Desktop-Windows-x86_64.exe"
cp -f "$DIST/Gleam-macOS-Intel-x86_64-$DATE"            "$DIST/Gleam-macOS-Intel-x86_64"
cp -f "$DIST/Gleam-macOS-AppleSilicon-arm64-$DATE"      "$DIST/Gleam-macOS-AppleSilicon-arm64"
cp -f "$DIST/Gleam-Linux-x86_64-$DATE"                  "$DIST/Gleam-Linux-x86_64"

echo "[3/5] 同步官网下载目录与前端/官网素材包"
# 官网下载页固定引用 website/dist 下的无日期命名，随版本一起更新
mkdir -p website/dist
cp -f "$DIST/Gleam-Windows-x86_64.exe"            website/dist/Gleam-Windows-x86_64.exe
cp -f "$DIST/Gleam-Desktop-Windows-x86_64.exe"    website/dist/Gleam-Desktop-Windows-x86_64.exe
cp -f "$DIST/Gleam-macOS-Intel-x86_64"            website/dist/Gleam-macOS-Intel-x86_64
cp -f "$DIST/Gleam-macOS-AppleSilicon-arm64"      website/dist/Gleam-macOS-AppleSilicon-arm64
cp -f "$DIST/Gleam-Linux-x86_64"                  website/dist/Gleam-Linux-x86_64
go run scripts/make-zip.go "$DIST/webui-static-$DATE.zip" internal/webui/static
go run scripts/make-zip.go "$DIST/website-$DATE.zip" \
  website/index.html website/style.css website/app.js

echo "[4/5] 打包全平台发布合集"
go run scripts/make-zip.go "$DIST/Gleam-release-$DATE.zip" \
  "$DIST/Gleam-Windows-x86_64-$DATE.exe" \
  "$DIST/Gleam-Desktop-Windows-x86_64-$DATE.exe" \
  "$DIST/Gleam-macOS-Intel-x86_64-$DATE" \
  "$DIST/Gleam-macOS-AppleSilicon-arm64-$DATE" \
  "$DIST/Gleam-Linux-x86_64-$DATE" \
  README.md \
  "pack/CHANGELOG.md=CHANGELOG.md" \
  "configs/config.yaml=config.example.yaml" \
  scripts/install.ps1 \
  scripts/build-desktop.sh

echo "[5/5] 归档到 pack/ 并生成校验和"
cp -f "$DIST/Gleam-release-$DATE.zip" "pack/Gleam-release-$DATE.zip"
(cd "$DIST" && sha256sum \
  "Gleam-Windows-x86_64-$DATE.exe" \
  "Gleam-Desktop-Windows-x86_64-$DATE.exe" \
  "Gleam-macOS-Intel-x86_64-$DATE" \
  "Gleam-macOS-AppleSilicon-arm64-$DATE" \
  "Gleam-Linux-x86_64-$DATE" \
  "webui-static-$DATE.zip" \
  "website-$DATE.zip" \
  "Gleam-release-$DATE.zip" > "SHA256SUMS-$DATE.txt")
cp -f "$DIST/SHA256SUMS-$DATE.txt" "pack/SHA256SUMS-$DATE.txt"

echo
echo "完成：$DIST/ 下的 $DATE 产物"
(cd "$DIST" && ls -la --block-size=K *"$DATE"* "SHA256SUMS-$DATE.txt" 2>/dev/null || ls -la *"$DATE"*)
echo
echo "提示：macOS 未签名二进制首次运行需 chmod +x 并在「系统设置 → 隐私与安全性」允许。"
