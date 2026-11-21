#!/usr/bin/env bash
set -euo pipefail
umask 022

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-1.0.0}"
ARCH="${ARCH:-amd64}"
SOURCE="${1:-$ROOT/dist/clipbridge_linux_amd64}"
OUT="${OUT:-$ROOT/dist/clipbridge_${VERSION}_${ARCH}.deb}"

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "找不到 dpkg-deb，请在 Debian/Ubuntu 上运行此脚本。" >&2
  exit 1
fi
if [[ ! -f "$SOURCE" ]]; then
  echo "找不到二进制：$SOURCE" >&2
  echo "请先运行 ./scripts/build.sh 或传入已构建的 Linux 二进制路径。" >&2
  exit 1
fi

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
PKGROOT="$STAGE/clipbridge_${VERSION}_${ARCH}"
APPDIR="$PKGROOT/usr/share/applications"
ICONDIR="$PKGROOT/usr/share/icons/hicolor/256x256/apps"
DOCDIR="$PKGROOT/usr/share/doc/clipbridge"

install -Dm755 "$SOURCE" "$PKGROOT/usr/bin/clipbridge"
install -Dm644 "$ROOT/assets/icon.png" "$ICONDIR/clipbridge.png"
install -Dm644 "$ROOT/README.md" "$DOCDIR/README.md"
install -Dm644 "$ROOT/LICENSE" "$DOCDIR/copyright"

mkdir -p "$APPDIR" "$PKGROOT/DEBIAN"
cat >"$APPDIR/clipbridge.desktop" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=剪贴桥
Name[en]=ClipBridge
Comment=局域网剪贴板共享
Comment[en]=Share the clipboard over your local network
Exec=/usr/bin/clipbridge
Icon=clipbridge
Terminal=false
Categories=Network;
StartupNotify=false
EOF

INSTALLED_SIZE="$(du -sk "$PKGROOT" | cut -f1)"
cat >"$PKGROOT/DEBIAN/control" <<EOF
Package: clipbridge
Version: $VERSION
Architecture: $ARCH
Maintainer: ClipBridge <clipbridge@localhost>
Installed-Size: $INSTALLED_SIZE
Section: net
Priority: optional
Recommends: xdg-utils
Suggests: zenity
Description: 局域网剪贴板共享工具
 ClipBridge 在 Ubuntu 与 Windows 之间同步文本、PNG 图片和文件。
 支持局域网自动发现、手动 IP 连接、托盘运行、开机自启和本地控制面板。
 不设置配对码时流量为明文；建议两端设置相同配对码以启用认证和加密。
EOF

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
dpkg-deb --build --root-owner-group "$PKGROOT" "$OUT" >/dev/null

echo "deb 已生成：$OUT"
dpkg-deb --info "$OUT" | sed -n '1,20p'
