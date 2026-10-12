package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrManualInstall 表示当前安装形态不能原地升级，需要用户手动安装。
var ErrManualInstall = errors.New("该安装形态不支持自动更新，请手动安装")

// InstallForm 描述当前运行实例的安装形态。
type InstallForm int

const (
	// FormUnknown 无法判断。
	FormUnknown InstallForm = iota
	// FormAppImage Linux AppImage（$APPIMAGE 指向 .AppImage 单文件）。
	FormAppImage
	// FormBinary 单文件可执行（便携 tarball / 手动放置的二进制）。
	FormBinary
	// FormWindowsZip Windows 便携目录（exe 与 mpv/ 同级）。
	FormWindowsZip
	// FormSystemPackage 由 deb/rpm/pacman 安装进系统目录。
	FormSystemPackage
	// FormDarwinApp macOS .app 包。
	FormDarwinApp
)

// String 返回安装形态的中文说明，用于设置页展示。
func (f InstallForm) String() string {
	switch f {
	case FormAppImage:
		return "AppImage"
	case FormBinary:
		return "便携二进制"
	case FormWindowsZip:
		return "Windows 便携版"
	case FormSystemPackage:
		return "系统包（deb/rpm/pacman）"
	case FormDarwinApp:
		return "macOS 应用包"
	default:
		return "未知"
	}
}

// SelfUpdatable 报告该形态能否原地升级。
func (f InstallForm) SelfUpdatable() bool {
	switch f {
	case FormAppImage, FormBinary, FormWindowsZip:
		return true
	default:
		return false
	}
}

// systemPrefixes 是包管理器安装位置的常见前缀；命中即认为非便携安装。
var systemPrefixes = []string{"/usr/", "/opt/", "/bin/", "/sbin/", "/lib/", "/nix/store/", "/var/lib/"}

// Detect 判断当前安装形态并返回需要被替换的可执行文件路径。
func Detect(goos string) (InstallForm, string) {
	exe, err := os.Executable()
	if err != nil {
		return FormUnknown, ""
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	switch goos {
	case "linux":
		if img := strings.TrimSpace(os.Getenv("APPIMAGE")); img != "" {
			return FormAppImage, img
		}
		if underSystemPrefix(exe) {
			return FormSystemPackage, exe
		}
		return FormBinary, exe
	case "windows":
		return FormWindowsZip, exe
	case "darwin":
		if strings.Contains(exe, ".app/Contents/MacOS/") {
			return FormDarwinApp, exe
		}
		return FormBinary, exe
	default:
		return FormUnknown, exe
	}
}

func underSystemPrefix(exe string) bool {
	slash := filepath.ToSlash(exe)
	for _, p := range systemPrefixes {
		if strings.HasPrefix(slash, p) {
			return true
		}
	}
	return false
}

// Apply 用已下载到 assetPath 的产物原地升级目标可执行文件。该安装形态不
// 支持原地升级时返回 ErrManualInstall。
func Apply(form InstallForm, exe, assetPath string, a Asset) error {
	if exe == "" || assetPath == "" {
		return errors.New("更新路径为空")
	}
	switch form {
	case FormAppImage:
		return swapExecutable(assetPath, exe)
	case FormBinary:
		if strings.HasSuffix(strings.ToLower(a.Name), ".tar.gz") {
			bin, err := extractTarGzBinary(assetPath)
			if err != nil {
				return err
			}
			defer os.Remove(bin)
			return swapExecutable(bin, exe)
		}
		return swapExecutable(assetPath, exe)
	case FormWindowsZip:
		return applyWindowsZip(assetPath, exe)
	default:
		return ErrManualInstall
	}
}

// swapExecutable 把 src 复制到 dst 同目录的临时文件后原子改名覆盖，保证覆盖
// 中途失败也不会留下半截可执行文件。
func swapExecutable(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".quaver-astra-update-*")
	if err != nil {
		return fmt.Errorf("无法写入 %s：%w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o755)
	if info.Mode().Perm() != 0 {
		mode = info.Mode().Perm() | 0o111
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	tmpName = "" // 改名成功，不再清理
	return nil
}

// applyWindowsZip 处理 Windows 便携目录：Windows 允许重命名正在运行的 exe，
// 因此先把旧 exe 挪开，再放入新 exe 并覆盖随包的 mpv/。
func applyWindowsZip(assetPath, exe string) error {
	root, err := extractZipRoot(assetPath)
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	newExe := filepath.Join(root, "quaver-astra.exe")
	if _, err := os.Stat(newExe); err != nil {
		return fmt.Errorf("安装包内缺少 quaver-astra.exe：%w", err)
	}

	destDir := filepath.Dir(exe)
	stale := exe + ".old"
	_ = os.Remove(stale)
	if err := os.Rename(exe, stale); err != nil {
		return fmt.Errorf("无法替换正在运行的程序：%w", err)
	}
	if err := copyFile(newExe, exe, 0o755); err != nil {
		_ = os.Rename(stale, exe) // 回滚
		return err
	}
	// mpv 运行时随包分发，尽力同步；失败不致命（用户可能自定义了 QAA_MPV）。
	if src := filepath.Join(root, "mpv"); dirExists(src) {
		_ = copyTree(src, filepath.Join(destDir, "mpv"))
	}
	return nil
}

// CleanupStale 清理上一次 Windows 热替换留下的 .old 文件；其它平台无操作。
// 应在启动时调用一次。
func CleanupStale(exe string) {
	if runtime.GOOS != "windows" || exe == "" {
		return
	}
	_ = os.Remove(exe + ".old")
}

// extractTarGzBinary 从便携 tarball 里取出 quaver-astra 可执行文件到临时目录。
func extractTarGzBinary(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	out, err := os.CreateTemp("", "quaver-astra-bin-*")
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if name != "quaver-astra" {
			continue
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		if err := out.Close(); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		if err := os.Chmod(out.Name(), 0o755); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
	out.Close()
	os.Remove(out.Name())
	return "", fmt.Errorf("tarball 内未找到 quaver-astra 可执行文件")
}

// extractZipRoot 把 zip 解到临时目录并返回唯一顶层目录（发行包结构）。
func extractZipRoot(archivePath string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	dest, err := os.MkdirTemp("", "quaver-astra-zip-*")
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if err := extractZipEntry(dest, f); err != nil {
			os.RemoveAll(dest)
			return "", err
		}
	}
	// 发行包顶层是单个目录，zip 解出的根即该目录。
	entries, err := os.ReadDir(dest)
	if err != nil {
		os.RemoveAll(dest)
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(dest, e.Name()), nil
		}
	}
	return dest, nil
}

func extractZipEntry(dest string, f *zip.File) error {
	target, err := zipSafeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// zipSafeJoin 拒绝 ../ 越界条目，避免 zip-slip。
func zipSafeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	if target != dest && !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
		return "", fmt.Errorf("压缩包条目不安全：%s", name)
	}
	return target, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
