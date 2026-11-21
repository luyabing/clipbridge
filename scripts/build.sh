#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GO="${GO:-go}"
VERSION="${VERSION:-1.0.0}"
LDFLAGS="-s -w -X main.version=$VERSION"

mkdir -p dist

echo "==> 运行测试"
"$GO" test ./...

echo "==> 构建 Linux amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  "$GO" build -trimpath -ldflags "$LDFLAGS" \
  -o dist/clipbridge_linux_amd64 .

echo "==> 构建 Windows amd64"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  "$GO" build -trimpath -ldflags "$LDFLAGS -H windowsgui" \
  -o dist/clipbridge_windows_amd64.exe .

if command -v dpkg-deb >/dev/null 2>&1; then
  echo "==> 构建 deb"
  VERSION="$VERSION" "$ROOT/scripts/build-deb.sh"
fi

echo
echo "构建完成："
ls -lh dist
