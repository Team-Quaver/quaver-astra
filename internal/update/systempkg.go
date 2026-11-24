package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PackageKind 是系统包安装使用的包格式。
type PackageKind int

const (
	// PackageNone 表示无法识别系统包格式。
	PackageNone PackageKind = iota
	// PackageDeb 是 Debian / Ubuntu / AOSC 等 dpkg 系发行版的 .deb。
	PackageDeb
	// PackageRPM 是 Fedora / openSUSE / RHEL 等 rpm 系发行版的 .rpm。
	PackageRPM
	// PackagePacman 是 Arch Linux 及其衍生发行版的 pacman 包。
	PackagePacman
)

// String 返回包格式名称。
func (k PackageKind) String() string {
	switch k {
	case PackageDeb:
		return "deb"
	case PackageRPM:
		return "rpm"
	case PackagePacman:
		return "pacman"
	default:
		return "未知"
	}
}

// ManagerHint 返回该包格式使用的包管理器说明，用于设置页展示。
func (k PackageKind) ManagerHint() string {
	switch k {
	case PackageDeb:
		return "apt / oma"
	case PackageRPM:
		return "zypper / dnf"
	case PackagePacman:
		return "pacman"
	default:
		return ""
	}
}

// PackageKindFromAsset 从发行产物名推断包格式。
func PackageKindFromAsset(name string) PackageKind {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasSuffix(name, ".deb"):
		return PackageDeb
	case strings.HasSuffix(name, ".rpm"):
		return PackageRPM
	case strings.HasSuffix(name, ".pkg.tar.zst"),
		strings.HasSuffix(name, ".pkg.tar.xz"),
		strings.HasSuffix(name, ".pkg.tar.gz"):
		return PackagePacman
	default:
		return PackageNone
	}
}

// DetectPackageKind 判断系统目录里的可执行文件由哪种系统包安装。先查包
// 数据库；数据库工具都存在时以查询结果为准，避免把手工放置的程序当成系统
// 包，只有完全查不了时才回落到发行版标识。
func DetectPackageKind(exe string) PackageKind {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return PackageNone
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	probed := false
	if _, err := exec.LookPath("dpkg-query"); err == nil {
		probed = true
		if commandOK("dpkg-query", "-S", exe) {
			return PackageDeb
		}
	}
	if _, err := exec.LookPath("rpm"); err == nil {
		probed = true
		if commandOK("rpm", "-qf", exe) {
			return PackageRPM
		}
	}
	if _, err := exec.LookPath("pacman"); err == nil {
		probed = true
		if commandOK("pacman", "-Qo", exe) {
			return PackagePacman
		}
	}
	if probed {
		return PackageNone
	}
	return packageKindFromDistro()
}

func commandOK(name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run() == nil
}

func packageKindFromDistro() PackageKind {
	switch {
	case fileExists("/etc/arch-release"), dirExists("/var/lib/pacman/local"):
		return PackagePacman
	case fileExists("/etc/debian_version"):
		return PackageDeb
	case fileExists("/etc/fedora-release"),
		fileExists("/etc/redhat-release"),
		fileExists("/etc/SuSE-release"),
		dirExists("/etc/zypp"):
		return PackageRPM
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		return packageKindFromOSRelease(string(data))
	}
	return PackageNone
}

func packageKindFromOSRelease(data string) PackageKind {
	var id, like string
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "ID":
			id = strings.ToLower(value)
		case "ID_LIKE":
			like = strings.ToLower(value)
		}
	}
	for _, field := range strings.Fields(id + " " + like) {
		switch field {
		case "arch", "archlinux", "manjaro", "endeavouros", "garuda":
			return PackagePacman
		case "debian", "ubuntu", "linuxmint", "pop", "deepin", "kali", "aosc", "openkylin":
			return PackageDeb
		case "fedora", "rhel", "centos", "rocky", "almalinux",
			"opensuse", "opensuse-leap", "opensuse-tumbleweed", "suse", "sled", "sles",
			"amzn", "mageia", "oracle":
			return PackageRPM
		}
	}
	return PackageNone
}

// SystemAssetFor 按包格式挑出系统包产物。发行包名形如
// Quaver-Astra-<version>-linux-<arch>.<ext>，rpm 与 pacman 使用各自的
// 架构名，不能直接复用 AppImage 的 assetTarget。
func (r *Release) SystemAssetFor(goarch string, kind PackageKind) (Asset, bool) {
	if r == nil {
		return Asset{}, false
	}
	for _, suffix := range systemAssetSuffixes(goarch, kind) {
		for _, a := range r.Assets {
			if strings.HasSuffix(strings.ToLower(a.Name), suffix) {
				return a, true
			}
		}
	}
	return Asset{}, false
}

func systemAssetSuffixes(goarch string, kind PackageKind) []string {
	switch kind {
	case PackageDeb:
		switch goarch {
		case "amd64":
			return []string{"-linux-amd64.deb"}
		case "arm64":
			return []string{"-linux-arm64.deb"}
		case "loong64":
			return []string{"-linux-loong64.deb", "-linux-loongarch64.deb"}
		}
	case PackageRPM:
		switch goarch {
		case "amd64":
			return []string{"-linux-x86_64.rpm"}
		case "arm64":
			return []string{"-linux-aarch64.rpm"}
		case "loong64":
			return []string{"-linux-loongarch64.rpm"}
		}
	case PackagePacman:
		switch goarch {
		case "amd64":
			return []string{"-linux-x86_64.pkg.tar.zst", "-linux-x86_64.pkg.tar.xz"}
		case "arm64":
			return []string{"-linux-aarch64.pkg.tar.zst", "-linux-aarch64.pkg.tar.xz"}
		case "loong64":
			return []string{"-linux-loong64.pkg.tar.zst", "-linux-loong64.pkg.tar.xz"}
		}
	}
	return nil
}

// systemInstallCommand 返回安装本地系统包所需的命令与参数。deb 优先用
// oma（AOSC OS），没有 oma 时用 apt；rpm 优先 zypper，其次 dnf。
func systemInstallCommand(kind PackageKind, assetPath string, look func(string) (string, error)) (string, []string, error) {
	if look == nil {
		look = exec.LookPath
	}
	find := func(names ...string) string {
		for _, name := range names {
			if path, err := look(name); err == nil && path != "" {
				return path
			}
		}
		return ""
	}
	switch kind {
	case PackageDeb:
		if bin := find("oma"); bin != "" {
			return bin, []string{"install", "--no-refresh", "-y", assetPath}, nil
		}
		if bin := find("apt"); bin != "" {
			return bin, []string{"install", "-y", "--allow-downgrades", assetPath}, nil
		}
		return "", nil, errors.New("未找到 apt 或 oma，无法安装 deb 更新")
	case PackageRPM:
		if bin := find("zypper"); bin != "" {
			return bin, []string{"--non-interactive", "--no-gpg-checks", "install", assetPath}, nil
		}
		if bin := find("dnf"); bin != "" {
			return bin, []string{"install", "-y", "--nogpgcheck", assetPath}, nil
		}
		return "", nil, errors.New("未找到 zypper 或 dnf，无法安装 rpm 更新")
	case PackagePacman:
		if bin := find("pacman"); bin != "" {
			return bin, []string{"-U", "--noconfirm", assetPath}, nil
		}
		return "", nil, errors.New("未找到 pacman，无法安装 Arch 更新")
	default:
		return "", nil, fmt.Errorf("无法识别安装包格式：%s", filepath.Base(assetPath))
	}
}

// installSystemPackage 用 pkexec 提权，把下载好的系统包交给包管理器安装。
func installSystemPackage(ctx context.Context, assetPath string, kind PackageKind) error {
	bin, args, err := systemInstallCommand(kind, assetPath, exec.LookPath)
	if err != nil {
		return err
	}
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return errors.New("未找到 pkexec，无法提权安装更新")
	}
	cmd := exec.CommandContext(ctx, pkexec, append([]string{bin}, args...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("%s 安装失败：%w", filepath.Base(bin), err)
		}
		return fmt.Errorf("%s 安装失败：%w\n%s", filepath.Base(bin), err, detail)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
