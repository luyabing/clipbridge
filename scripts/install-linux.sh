#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="${1:-$ROOT/dist/clipbridge_linux_amd64}"

if [[ ! -f "$SOURCE" ]]; then
  echo "找不到二进制：$SOURCE" >&2
  echo "请先运行 ./scripts/build.sh，或把已构建的 Linux 二进制路径作为第一个参数传入。" >&2
  exit 1
fi

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}"
APP_DIR="$DATA_DIR/applications"
ICON_DIR="$DATA_DIR/icons/hicolor/256x256/apps"

mkdir -p "$BIN_DIR" "$APP_DIR" "$ICON_DIR"

install -m 0755 "$SOURCE" "$BIN_DIR/clipbridge"
install -m 0644 "$ROOT/assets/icon.png" "$ICON_DIR/clipbridge.png"

cat >"$APP_DIR/clipbridge.desktop" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=剪贴桥
Name[en]=ClipBridge
Comment=局域网剪贴板共享
Exec="$BIN_DIR/clipbridge"
Icon=clipbridge
Terminal=false
Categories=Network;
StartupNotify=false
EOF

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
fi

echo "已安装："
echo "  程序   $BIN_DIR/clipbridge"
echo "  菜单项 $APP_DIR/clipbridge.desktop"
echo "  图标   $ICON_DIR/clipbridge.png"
echo
echo "现在可以从应用列表启动“剪贴桥”，或直接运行："
echo "  $BIN_DIR/clipbridge"
echo
echo "如果 $BIN_DIR 不在 PATH 中，请把它加入 PATH，或使用上面的完整路径。"
