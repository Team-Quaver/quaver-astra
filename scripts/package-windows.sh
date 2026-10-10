#!/usr/bin/env bash
# Package a Windows cross-build with the official mpv runtime.
#
# Usage: package-windows.sh <binary.exe> <mpv-root> <version> <arch> <output.zip>
set -euo pipefail
binary="${1:?binary required}"
mpv_root="${2:?mpv root required}"
version="${3:?version required}"
arch="${4:?arch required}"
output="${5:?output required}"
mkdir -p "$(dirname "$output")"
output="$(cd "$(dirname "$output")" && pwd)/$(basename "$output")"
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
name="quaver-astra-${version}-windows-${arch}"
mkdir -p "$root/$name/mpv"
install -m 0755 "$binary" "$root/$name/quaver-astra.exe"
cp -a "$mpv_root/." "$root/$name/mpv/"
install -m 0644 LICENSE README.md "$root/$name/"
(cd "$root" && zip -qr "$output" "$name")
echo "Created $output"
