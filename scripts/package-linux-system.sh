#!/usr/bin/env bash
# Build Debian, RPM and Pacman packages using the system mpv runtime.
#
# Usage: package-linux-system.sh <binary> <amd64|arm64|loong64> <version> <output-dir>
set -euo pipefail

binary="${1:?binary required}"
goarch="${2:?goarch required}"
version="${3:?version required}"
outdir="${4:?output dir required}"
name="quaver-astra"
description="Quaver Astra — native QQ Music client"
url="https://github.com/Team-Quaver/quaver-astra"
maintainer="Ne0W0r1d <ne0w0r1d@ne0w0r1d.top>"

case "$goarch" in
  amd64)
    deb_archs=(amd64)
    rpm_arch=x86_64
    pacman_arch=x86_64
    machine="x86-64"
    ;;
  arm64)
    deb_archs=(arm64)
    rpm_arch=aarch64
    pacman_arch=aarch64
    machine="aarch64|ARM aarch64"
    ;;
  loong64)
    # Debian uses loong64; AOSC and RPM use the upstream triplet loongarch64.
    deb_archs=(loong64 loongarch64)
    rpm_arch=loongarch64
    pacman_arch=loong64
    machine="LoongArch"
    ;;
  *) echo "unsupported Linux architecture: $goarch" >&2; exit 2 ;;
esac

[ -f "$binary" ] || { echo "binary not found: $binary" >&2; exit 1; }
file "$binary" | grep -Eiq "$machine"

# RPM and Pacman do not allow hyphens in versions. Debian preserves the exact
# release version; release files below still use the exact version in names.
rpm_version="${version//-/.}"
pacman_version="${version//-/.}"
mkdir -p "$outdir"
outdir="$(cd "$outdir" && pwd)"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
payload="$stage/payload"
mkdir -p \
  "$payload/usr/bin" \
  "$payload/usr/share/applications" \
  "$payload/usr/share/icons/hicolor/512x512/apps" \
  "$payload/usr/share/licenses/$name" \
  "$payload/usr/share/doc/$name"

install -m 0755 "$binary" "$payload/usr/bin/quaver-astra"
install -m 0644 img/app-icon.png "$payload/usr/share/icons/hicolor/512x512/apps/quaver-astra.png"
install -m 0644 LICENSE "$payload/usr/share/licenses/$name/LICENSE"
install -m 0644 LICENSE "$payload/usr/share/doc/$name/copyright"
install -m 0644 README.md "$payload/usr/share/doc/$name/README.md"

# 桌面条目名即应用 ID（反向 DNS）：桌面按它把窗口归到本条目、XDG 门户也按
# 它注册。必须与 internal/appid.Identifier 一致。
cat > "$payload/usr/share/applications/red.0w0.quaver-astra.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Quaver Astra
Comment=Native QQ Music client
Exec=quaver-astra
Icon=quaver-astra
Categories=AudioVideo;Audio;Player;
Keywords=music;qq;player;
Terminal=false
StartupWMClass=red.0w0.quaver-astra
DESKTOP

if command -v desktop-file-validate >/dev/null 2>&1; then
  desktop-file-validate "$payload/usr/share/applications/red.0w0.quaver-astra.desktop"
fi

build_deb() {
  local arch="$1"
  local output="$outdir/Quaver-Astra-${version}-linux-${arch}.deb"
  local root="$stage/deb-$arch"
  rm -rf "$root"
  mkdir -p "$root/DEBIAN"
  cp -a "$payload/." "$root/"
  {
    echo "Package: $name"
    echo "Version: $version"
    echo "Architecture: $arch"
    echo "Maintainer: $maintainer"
    echo "Depends: mpv"
    echo "Section: sound"
    echo "Priority: optional"
    echo "Homepage: $url"
    echo "Installed-Size: $(du -sk "$payload" | awk '{print $1}')"
    echo "Description: $description"
    echo " Native UI client. Playback uses the system mpv."
  } > "$root/DEBIAN/control"
  dpkg-deb --root-owner-group --build "$root" "$output"
  [ "$(dpkg-deb -f "$output" Package)" = "$name" ]
  [ "$(dpkg-deb -f "$output" Version)" = "$version" ]
  [ "$(dpkg-deb -f "$output" Architecture)" = "$arch" ]
  [ "$(dpkg-deb -f "$output" Depends)" = "mpv" ]
  echo "Created $output"
}

build_rpm() {
  local output="$outdir/Quaver-Astra-${version}-linux-${rpm_arch}.rpm"
  local tree="$stage/rpmbuild"
  rm -rf "$tree"
  mkdir -p "$tree"/{BUILD,RPMS,SOURCES,SPECS,SRPMS} "$stage/rpm-tmp"
  cp "$binary" "$tree/SOURCES/quaver-astra"
  cp "$payload/usr/share/applications/red.0w0.quaver-astra.desktop" "$tree/SOURCES/"
  cp "$payload/usr/share/icons/hicolor/512x512/apps/quaver-astra.png" "$tree/SOURCES/"
  cp LICENSE "$tree/SOURCES/"

  cat > "$tree/SPECS/$name.spec" <<SPEC
Name: $name
Version: $rpm_version
Release: 1
Summary: Native QQ Music client
License: AGPL-3.0-only
URL: $url
Source0: quaver-astra
Source1: red.0w0.quaver-astra.desktop
Source2: quaver-astra.png
Source3: LICENSE
AutoReqProv: no
Requires: mpv

%description
Native UI client for Quaver Astra. Playback uses the system mpv.

%global debug_package %{nil}
%global __os_install_post %{nil}

%install
install -Dm0755 "%{SOURCE0}" "%{buildroot}/usr/bin/quaver-astra"
install -Dm0644 "%{SOURCE1}" "%{buildroot}/usr/share/applications/red.0w0.quaver-astra.desktop"
install -Dm0644 "%{SOURCE2}" "%{buildroot}/usr/share/icons/hicolor/512x512/apps/quaver-astra.png"
install -Dm0644 "%{SOURCE3}" "%{buildroot}/usr/share/licenses/%{name}/LICENSE"

%files
/usr/bin/quaver-astra
/usr/share/applications/red.0w0.quaver-astra.desktop
/usr/share/icons/hicolor/512x512/apps/quaver-astra.png
%license /usr/share/licenses/%{name}/LICENSE
SPEC

  rpmbuild --quiet -bb \
    --define "_topdir $tree" \
    --define "_tmppath $stage/rpm-tmp" \
    --target "$rpm_arch" \
    "$tree/SPECS/$name.spec"
  built=$(find "$tree/RPMS" -type f -name '*.rpm' -print -quit)
  [ -n "$built" ] || { echo "RPM build produced no package" >&2; exit 1; }
  mv "$built" "$output"
  [ "$(rpm -qp --qf '%{NAME}' "$output")" = "$name" ]
  [ "$(rpm -qp --qf '%{ARCH}' "$output")" = "$rpm_arch" ]
  [ "$(rpm -qp --qf '%{LICENSE}' "$output")" = "AGPL-3.0-only" ]
  rpm -qp --requires "$output" | grep -Fx 'mpv'
  echo "Created $output"
}

build_pacman() {
  local output="$outdir/Quaver-Astra-${version}-linux-${pacman_arch}.pkg.tar.zst"
  python3 scripts/package-pacman.py \
    --name "$name" \
    --version "$pacman_version" \
    --arch "$pacman_arch" \
    --desc "$description" \
    --url "$url" \
    --license "AGPL-3.0-only" \
    --depend "mpv" \
    --payload "$payload" \
    --output "$stage/package.tar"
  zstd -q -f -T0 "$stage/package.tar" -o "$output"
  rm -f "$stage/package.tar"
  tar --zstd -xOf "$output" .PKGINFO | grep -Fx 'pkgname = quaver-astra'
  tar --zstd -xOf "$output" .PKGINFO | grep -Fx "arch = $pacman_arch"
  tar --zstd -xOf "$output" .PKGINFO | grep -Fx 'license = AGPL-3.0-only'
  tar --zstd -xOf "$output" .PKGINFO | grep -Fx 'depend = mpv'
  tar --zstd -tf "$output" | grep -Fx 'usr/bin/quaver-astra'
  echo "Created $output"
}

for arch in "${deb_archs[@]}"; do
  build_deb "$arch"
done
build_rpm
build_pacman
