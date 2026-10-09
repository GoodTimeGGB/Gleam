#!/usr/bin/env bash
# Build Gleam multi-platform binaries (pure Go, zero cgo, cross-compile friendly):
#   bin/gleam.exe               Windows console CLI (with icon)
#   bin/GleamDesktop.exe        Windows Desktop (windowsgui + tray + icon)
#   bin/gleam-darwin-amd64      macOS Intel
#   bin/gleam-darwin-arm64      macOS Apple Silicon
#   bin/gleam-linux-amd64       Linux x86_64
#
# Icon resource: cmd/gleam/rsrc_windows_amd64.syso (committed; auto-linked on Windows).
# Regenerate icon: go run scripts/make-icon.go && rsrc -ico assets/gleam.ico -arch amd64 -o cmd/gleam/rsrc_windows_amd64.syso
set -euo pipefail
cd "$(dirname "$0")/.."

# Version owner is internal/buildinfo.Version. Read the default and also pass -X so
# release binaries cannot silently embed a stale value if the source default drifts
# from the intended tag. Override with: VERSION=1.0.2 bash scripts/build-desktop.sh
VERSION="${VERSION:-$(sed -n 's/^[[:space:]]*var Version = "\([^"]*\)".*/\1/p' internal/buildinfo/buildinfo.go | head -n1)}"
if [[ -z "${VERSION}" ]]; then
  echo "failed to resolve Version from internal/buildinfo/buildinfo.go" >&2
  exit 1
fi
LDFLAGS_COMMON="-s -w -X gleam/internal/buildinfo.Version=${VERSION}"
echo "embedding version ${VERSION} via -X"

mkdir -p bin

echo "[1/4] go vet ./..."
go vet ./...

echo "[2/4] Windows (console CLI + Desktop windowsgui)"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam.exe ./cmd/gleam
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON -H=windowsgui" -o bin/GleamDesktop.exe ./cmd/gleam

echo "[3/4] macOS (Intel amd64 + Apple Silicon arm64)"
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-darwin-amd64 ./cmd/gleam
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-darwin-arm64 ./cmd/gleam

echo "[4/4] Linux x86_64"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS_COMMON" -o bin/gleam-linux-amd64 ./cmd/gleam

ls -la bin/
echo "done. macOS first run: chmod +x gleam-darwin-arm64 && ./gleam-darwin-arm64 app"
echo "unsigned binaries may need System Settings → Privacy & Security → Allow."
