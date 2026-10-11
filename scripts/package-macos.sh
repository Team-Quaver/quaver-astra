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
# icns 从同一张母图现场生成（iconset 全尺寸缩放 + iconutil 打包），
# 避免仓库里存一份与源图脱节的二进制 icns。macOS runner 自带 sips/iconutil。
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
icon_png="$repo_root/img/app-icon.png"
[ -f "$icon_png" ] || { echo "missing $icon_png" >&2; exit 1; }
iconset="$root/app-icon.iconset"
mkdir -p "$iconset"
gen_icon() { sips -z "$2" "$2" "$icon_png" --out "$iconset/$1" >/dev/null; }
gen_icon icon_16x16.png 16
gen_icon icon_16x16@2x.png 32
gen_icon icon_32x32.png 32
gen_icon icon_32x32@2x.png 64
gen_icon icon_128x128.png 128
gen_icon icon_128x128@2x.png 256
gen_icon icon_256x256.png 256
gen_icon icon_256x256@2x.png 512
gen_icon icon_512x512.png 512
gen_icon icon_512x512@2x.png 1024
iconutil -c icns "$iconset" -o "$app/Contents/Resources/quaver-astra.icns"
install -m 0644 LICENSE "$app/Contents/Resources/LICENSE"
install -m 0644 README.md "$app/Contents/Resources/README.md"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Quaver Astra</string>
  <key>CFBundleExecutable</key><string>quaver-astra</string>
  <key>CFBundleIdentifier</key><string>red.0w0.quaver-astra</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleIconFile</key><string>quaver-astra.icns</string>
  <key>CFBundleName</key><string>Quaver Astra</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
</dict></plist>
PLIST
# Ad-hoc 签名：没有签名的 app 在新版 macOS（尤其 Sequoia+）下载后会被
# Gatekeeper 直接判「已损坏」。ad-hoc 不能替代公证，但能把「已损坏」
# 降级为可右键打开的「无法验证开发者」提示。
codesign --force --deep --sign - "$app"
mkdir -p "$(dirname "$output")"
hdiutil create -volname "Quaver Astra" -srcfolder "$root" -ov -format UDZO "$output"
echo "Created $output"
