#!/usr/bin/env bash
# Package the LoongArch build as a portable binary tarball.
#
# Usage: package-tarball.sh <binary> <version> <output>
set -euo pipefail
binary="${1:?binary required}"
version="${2:?version required}"
output="${3:?output required}"
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
name="quaver-astra-${version}-linux-loong64"
mkdir -p "$root/$name"
install -m 0755 "$binary" "$root/$name/quaver-astra"
install -m 0644 LICENSE README.md "$root/$name/"
mkdir -p "$(dirname "$output")"
tar -C "$root" -czf "$output" "$name"
echo "Created $output"
