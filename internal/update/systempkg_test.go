package update

import (
	"errors"
	"slices"
	"testing"
)

func TestPackageKindFromAsset(t *testing.T) {
	cases := map[string]PackageKind{
		"Quaver-Astra-1.2.3-linux-amd64.deb":          PackageDeb,
		"Quaver-Astra-1.2.3-linux-x86_64.rpm":         PackageRPM,
		"Quaver-Astra-1.2.3-linux-x86_64.pkg.tar.zst": PackagePacman,
		"Quaver-Astra-1.2.3-linux-amd64.AppImage":     PackageNone,
	}
	for name, want := range cases {
		if got := PackageKindFromAsset(name); got != want {
			t.Errorf("PackageKindFromAsset(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSystemAssetFor(t *testing.T) {
	r := &Release{Assets: []Asset{
		{Name: "Quaver-Astra-9.2-linux-amd64.AppImage"},
		{Name: "Quaver-Astra-9.2-linux-amd64.deb"},
		{Name: "Quaver-Astra-9.2-linux-arm64.deb"},
		{Name: "Quaver-Astra-9.2-linux-loongarch64.deb"},
		{Name: "Quaver-Astra-9.2-linux-x86_64.rpm"},
		{Name: "Quaver-Astra-9.2-linux-aarch64.rpm"},
		{Name: "Quaver-Astra-9.2-linux-loongarch64.rpm"},
		{Name: "Quaver-Astra-9.2-linux-x86_64.pkg.tar.zst"},
		{Name: "Quaver-Astra-9.2-linux-aarch64.pkg.tar.zst"},
		{Name: "Quaver-Astra-9.2-linux-loong64.pkg.tar.zst"},
	}}
	cases := []struct {
		goarch string
		kind   PackageKind
		want   string
		ok     bool
	}{
		{"amd64", PackageDeb, "Quaver-Astra-9.2-linux-amd64.deb", true},
		{"arm64", PackageDeb, "Quaver-Astra-9.2-linux-arm64.deb", true},
		{"loong64", PackageDeb, "Quaver-Astra-9.2-linux-loongarch64.deb", true},
		{"amd64", PackageRPM, "Quaver-Astra-9.2-linux-x86_64.rpm", true},
		{"arm64", PackageRPM, "Quaver-Astra-9.2-linux-aarch64.rpm", true},
		{"loong64", PackageRPM, "Quaver-Astra-9.2-linux-loongarch64.rpm", true},
		{"amd64", PackagePacman, "Quaver-Astra-9.2-linux-x86_64.pkg.tar.zst", true},
		{"arm64", PackagePacman, "Quaver-Astra-9.2-linux-aarch64.pkg.tar.zst", true},
		{"loong64", PackagePacman, "Quaver-Astra-9.2-linux-loong64.pkg.tar.zst", true},
		{"riscv64", PackageDeb, "", false},
		{"amd64", PackageNone, "", false},
	}
	for _, c := range cases {
		got, ok := r.SystemAssetFor(c.goarch, c.kind)
		if ok != c.ok || got.Name != c.want {
			t.Errorf("SystemAssetFor(%s, %v) = %q,%v want %q,%v", c.goarch, c.kind, got.Name, ok, c.want, c.ok)
		}
	}
}

func TestSystemInstallCommand(t *testing.T) {
	lookWith := func(available ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			if slices.Contains(available, name) {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		name   string
		kind   PackageKind
		look   func(string) (string, error)
		want   string
		args   []string
		wantOK bool
	}{
		{"oma preferred", PackageDeb, lookWith("oma", "apt"), "/usr/bin/oma", []string{"install", "--no-refresh", "-y", "/tmp/a.deb"}, true},
		{"apt fallback", PackageDeb, lookWith("apt"), "/usr/bin/apt", []string{"install", "-y", "--allow-downgrades", "/tmp/a.deb"}, true},
		{"zypper", PackageRPM, lookWith("zypper", "dnf"), "/usr/bin/zypper", []string{"--non-interactive", "--no-gpg-checks", "install", "/tmp/a.rpm"}, true},
		{"dnf", PackageRPM, lookWith("dnf"), "/usr/bin/dnf", []string{"install", "-y", "--nogpgcheck", "/tmp/a.rpm"}, true},
		{"pacman", PackagePacman, lookWith("pacman"), "/usr/bin/pacman", []string{"-U", "--noconfirm", "/tmp/a.pkg.tar.zst"}, true},
		{"missing manager", PackageDeb, lookWith(), "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := "/tmp/a.deb"
			if c.kind == PackageRPM {
				path = "/tmp/a.rpm"
			} else if c.kind == PackagePacman {
				path = "/tmp/a.pkg.tar.zst"
			}
			bin, args, err := systemInstallCommand(c.kind, path, c.look)
			if (err == nil) != c.wantOK {
				t.Fatalf("err = %v, wantOK = %v", err, c.wantOK)
			}
			if bin != c.want || !slices.Equal(args, c.args) {
				t.Fatalf("command = %q %v, want %q %v", bin, args, c.want, c.args)
			}
		})
	}
}

func TestPackageKindFromOSRelease(t *testing.T) {
	cases := map[string]PackageKind{
		"ID=arch\n":                   PackagePacman,
		"ID=ubuntu\nID_LIKE=debian\n": PackageDeb,
		"ID=aosc\n":                   PackageDeb,
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"suse\"\n": PackageRPM,
		"ID=fedora\n": PackageRPM,
		"ID=alpine\n": PackageNone,
	}
	for input, want := range cases {
		if got := packageKindFromOSRelease(input); got != want {
			t.Errorf("packageKindFromOSRelease(%q) = %v, want %v", input, got, want)
		}
	}
}
