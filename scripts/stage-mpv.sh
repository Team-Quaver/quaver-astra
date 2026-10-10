#!/usr/bin/env bash
# Download and stage the same mpv runtime assets used by Quaver Music.
#
# Usage: scripts/stage-mpv.sh <linux-amd64|linux-arm64|windows-amd64|windows-arm64|macos-arm64> <destination>
# The runtime is always installed at <destination>/mpv.
set -euo pipefail

target="${1:?target required}"
dest_root="${2:?destination required}"
dest="$dest_root/mpv"
work="$dest_root/.work-mpv"

MPV_VERSION="0.41.0"
LINUX_TAG="v0.41.0%402026-09-07_1788787125"
LINUX_BASE="https://github.com/pkgforge-dev/mpv-AppImage/releases/download/${LINUX_TAG}"
OFFICIAL_BASE="https://github.com/mpv-player/mpv/releases/download/v${MPV_VERSION}"

url=""
sha=""
case "$target" in
  linux-amd64)
    url="$LINUX_BASE/mpv-v${MPV_VERSION}-anylinux-x86_64.AppImage"
    sha="bb52fb49c54e83155891bfb97578e7ee40575a306d0dddc96fc603be20db8214"
    ;;
  linux-arm64)
    url="$LINUX_BASE/mpv-v${MPV_VERSION}-anylinux-aarch64.AppImage"
    sha="55c5642226ffdcf464a8783ffbacfdf3b6c64e7a77e85ba89e97196421b81321"
    ;;
  windows-amd64)
    url="$OFFICIAL_BASE/mpv-v${MPV_VERSION}-x86_64-w64-mingw32.zip"
    sha="a49811c0752c108b8260636f9c6f6fcb97406641c98b30f1e7b500dfb20177de"
    ;;
  windows-arm64)
    url="$OFFICIAL_BASE/mpv-v${MPV_VERSION}-aarch64-pc-windows-msvc.zip"
    sha="a822abeffd0ac88951f4084f3425f949842aa17d616f880637ebe9041e482e97"
    ;;
  macos-arm64)
    url="$OFFICIAL_BASE/mpv-v${MPV_VERSION}-macos-14-arm.zip"
    sha="5c96f9b21355fc0a11d2e2161ad65f33031070e9fb3f6bd9865fb459b94587e6"
    ;;
  *)
    echo "unsupported target: $target" >&2
    exit 2
    ;;
esac

rm -rf "$work" "$dest"
mkdir -p "$work" "$dest_root"
trap 'rm -rf "$work"' EXIT

asset="$work/${url##*/}"
echo "Downloading $url"
curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 -o "$asset" "$url"
if command -v sha256sum >/dev/null 2>&1; then
  echo "$sha  $asset" | sha256sum -c -
else
  got=$(shasum -a 256 "$asset" | awk '{print $1}')
  [ "$got" = "$sha" ] || { echo "sha256 mismatch: expected $sha, got $got" >&2; exit 1; }
fi

case "$target" in
  linux-*)
    # 7z 能直接读取 AppImage 内嵌的 SquashFS，跨架构也能解包；只有在
    # runner 没有 7z 时才退回 AppImage 自带的 --appimage-extract（后者要求
    # 目标架构与 runner 一致）。
    if command -v 7z >/dev/null 2>&1; then
      mkdir -p "$work/extracted"
      7z x -y -o"$work/extracted" "$asset" >/dev/null
      src="$work/extracted"
    else
      chmod +x "$asset"
      (cd "$work" && "./${asset##*/}" --appimage-extract >/dev/null)
      src="$work/squashfs-root"
      [ -d "$src" ] || src="$work/AppDir"
    fi
    [ -d "$src" ] || { echo "mpv AppImage extraction produced no AppDir" >&2; exit 1; }
    cp -aL "$src/." "$dest/"
    [ -x "$dest/shared/bin/mpv" ] || { echo "missing $dest/shared/bin/mpv" >&2; exit 1; }
    compgen -G "$dest/lib/ld-linux*.so*" >/dev/null || { echo "missing bundled ELF loader" >&2; exit 1; }
    ;;
  windows-*)
    mkdir -p "$work/unpack"
    unzip -q "$asset" -d "$work/unpack"
    nested=$(find "$work/unpack" -maxdepth 1 -type f -name '*.zip' -print -quit)
    if [ -n "$nested" ]; then unzip -q "$nested" -d "$work/unpack"; fi
    cp -a "$work/unpack/." "$dest/"
    find "$dest" -type f \( -name '*.pdb' -o -name '*.zip' -o -name '*.bat' \) -delete
    [ -f "$dest/mpv.exe" ] || { echo "missing $dest/mpv.exe" >&2; exit 1; }
    ;;
  macos-arm64)
    mkdir -p "$work/unpack"
    unzip -q "$asset" -d "$work/unpack"
    nested=$(find "$work/unpack" -maxdepth 1 -type f -name '*.tar.gz' -print -quit)
    if [ -n "$nested" ]; then tar -xzf "$nested" -C "$work/unpack" --no-same-owner; fi
    app=$(find "$work/unpack" -type d -name mpv.app -print -quit)
    [ -n "$app" ] || { echo "missing mpv.app" >&2; exit 1; }
    mkdir -p "$dest"
    cp -a "$app" "$dest/mpv.app"
    [ -x "$dest/mpv.app/Contents/MacOS/mpv" ] || { echo "missing mpv payload" >&2; exit 1; }
    ;;
esac

echo "Staged mpv $MPV_VERSION at $dest"
