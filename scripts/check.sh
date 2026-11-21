#!/usr/bin/env bash
# 本地检查：Go 测试、go vet、Shell 语法检查。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! command -v go >/dev/null 2>&1; then
  echo "未找到 go。请安装 Go 1.25 或更新版本：https://go.dev/dl/" >&2
  exit 1
fi

echo "==> go test ./..."
go test ./...

echo "==> go vet ./..."
go vet ./...

echo "==> Shell 语法检查"
for file in scripts/build.sh scripts/build-deb.sh scripts/install-linux.sh scripts/check.sh; do
  bash -n "$file"
  echo "  ✓ $file"
done

echo "全部通过。"
