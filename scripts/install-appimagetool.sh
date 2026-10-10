#!/usr/bin/env bash
# Install the pinned appimagetool build used to package Linux AppImages.
set -euo pipefail

dest="${1:?destination required}"
url="https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage"
sha="a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0"
mkdir -p "$dest"
curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 -o "$dest/appimagetool" "$url"
echo "$sha  $dest/appimagetool" | sha256sum -c -
chmod +x "$dest/appimagetool"
"$dest/appimagetool" --appimage-extract-and-run --version
