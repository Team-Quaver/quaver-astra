#!/usr/bin/env bash
# Assemble a Quaver Astra AppImage from a cross-compiled binary and staged mpv.
#
# Usage: package-appimage.sh <x86_64|aarch64> <binary> <mpv-root> <version> <output>
set -euo pipefail

arch="${1:?arch required}"
binary="${2:?binary required}"
mpv_root="${3:?mpv root required}"
version="${4:?version required}"
output="${5:?output required}"
case "$arch" in x86_64|aarch64) ;; *) echo "unsupported AppImage arch: $arch" >&2; exit 2 ;; esac

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
appdir="$root/Quaver Astra.AppDir"
mkdir -p "$appdir/usr/bin" "$appdir/usr/share/quaver-astra" "$appdir/usr/share/applications" "$appdir/usr/share/icons/hicolor/256x256/apps"

install -m 0755 "$binary" "$appdir/usr/bin/quaver-astra"
cp -a "$mpv_root" "$appdir/usr/share/quaver-astra/mpv"
install -m 0644 internal/appui/assets/tray-icon-light-shell.png "$appdir/usr/share/icons/hicolor/256x256/apps/quaver-astra.png"
install -m 0644 internal/appui/assets/tray-icon-light-shell.png "$appdir/quaver-astra.png"

cat > "$appdir/quaver-astra.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Quaver Astra
Comment=Native QQ Music client
Exec=quaver-astra
Icon=quaver-astra
Categories=AudioVideo;Audio;Player;
Terminal=false
StartupWMClass=quaver-astra
DESKTOP

# Keep the bundled quick-sharun mpv isolated from the host loader. The launcher
# is intentionally not the upstream AppRun: its hooks can update/download tools.
cat > "$appdir/AppRun" <<'RUN'
#!/bin/sh
HERE="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
export QAA_MPV_DIR="$HERE/usr/share/quaver-astra/mpv"
exec "$HERE/usr/bin/quaver-astra" "$@"
RUN
chmod +x "$appdir/AppRun"

mkdir -p "$(dirname "$output")"
runtime_args=()
if [ -n "${APPIMAGETOOL_RUNTIME:-}" ]; then
  runtime_args=(--runtime-file "$APPIMAGETOOL_RUNTIME")
fi
ARCH="$arch" appimagetool --appimage-extract-and-run --no-appstream "${runtime_args[@]}" "$appdir" "$output"
chmod +x "$output"
echo "Created $output"
