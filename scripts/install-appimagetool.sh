#!/usr/bin/env bash
# Install appimagetool from its rolling continuous release.
#
# The release asset is mutable by design, so its SHA256 cannot be pinned to a
# constant in the repository. Prefer the digest reported by GitHub for the
# downloaded asset, and allow an explicit APPIMAGETOOL_SHA256 override.
set -euo pipefail

dest="${1:?destination required}"
name="appimagetool-x86_64.AppImage"
url="https://github.com/AppImage/appimagetool/releases/download/continuous/$name"
mkdir -p "$dest"

echo "Downloading $url"
curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 -o "$dest/appimagetool" "$url"

got="$(sha256sum "$dest/appimagetool" | awk '{print $1}')"
expected="${APPIMAGETOOL_SHA256:-}"

# GitHub's release API reports the asset's content digest. Use it when available
# so a truncated/modified download still fails, without pinning a mutable asset
# to a hash that becomes stale whenever continuous is rebuilt.
if [ -z "$expected" ] && command -v jq >/dev/null 2>&1; then
  auth=()
  if [ -n "${GH_TOKEN:-}" ]; then auth=(-H "Authorization: Bearer $GH_TOKEN"); fi
  expected="$(curl -fsSL --retry 2 "${auth[@]}" \
    https://api.github.com/repos/AppImage/appimagetool/releases/tags/continuous |
    jq -r --arg name "$name" '.assets[] | select(.name == $name) | .digest // empty' |
    sed -n 's/^sha256://p' || true)"
fi

if [ -n "$expected" ]; then
  [ "$got" = "$expected" ] || {
    echo "sha256 mismatch: expected $expected, got $got" >&2
    exit 1
  }
  echo "sha256 OK: $got"
else
  echo "::warning::No upstream digest available for $name; downloaded sha256: $got"
fi

chmod +x "$dest/appimagetool"
"$dest/appimagetool" --appimage-extract-and-run --version
