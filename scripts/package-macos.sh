#!/usr/bin/env bash
# Package an arm64 macOS app bundle and DMG with the official mpv runtime.
#
# Usage: package-macos.sh <binary> <mpv-root> <version> <output.dmg>
set -euo pipefail
binary="${1:?binary required}"
mpv_root="${2:?mpv root required}"
version="${3:?version required}"
output="${4:?output required}"
[ "$(uname -s)" = Darwin ] || { echo "package-macos.sh must run on macOS" >&2; exit 2; }
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
app="$root/Quaver Astra.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
install -m 0755 "$binary" "$app/Contents/MacOS/quaver-astra"
cp -a "$mpv_root" "$app/Contents/Resources/mpv"
install -m 0644 internal/appui/assets/tray-icon-light-shell.png "$app/Contents/Resources/quaver-astra.png"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Quaver Astra</string>
  <key>CFBundleExecutable</key><string>quaver-astra</string>
  <key>CFBundleIdentifier</key><string>red.0w0.quaver-astra</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Quaver Astra</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
</dict></plist>
PLIST
mkdir -p "$(dirname "$output")"
hdiutil create -volname "Quaver Astra" -srcfolder "$root" -ov -format UDZO "$output"
echo "Created $output"
