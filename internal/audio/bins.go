package audio

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// mpv 可执行文件的定位策略，与 Quaver Music 的 bins.ts 保持同一语义：
//
//	显式路径（QAA_MPV）→ QAA_MPV_DIR → 可执行文件旁的随包 mpv/ → PATH
//
// 发行包里的 mpv 来源与 Quaver Music 相同：Linux 使用 pkgforge 的 anylinux
// AppImage 展开目录，Windows/macOS 使用 mpv 官方 release。Linux 那份必须由
// 包内 loader 直启 shared/bin/mpv；直接把它塞进 LD_LIBRARY_PATH 会让宿主
// loader 混载包内 libc，也不能走上游 AppRun（其 hooks 会下载/更新组件）。
type mpvExecutable struct {
	// Path 是实际载荷。设置页展示它，便于用户确认 mpv 来源。
	Path string
	// Origin 是来源：explicit / bundled / PATH。
	Origin string
	// command 是完整 spawn argv；Linux quick-sharun 的 argv[0] 是包内 loader。
	command []string
	// bundledQuickSharun 表示需要隔离宿主 LD_LIBRARY_PATH。
	bundledQuickSharun bool
}

func (b mpvExecutable) args(extra ...string) []string {
	args := append([]string(nil), b.command[1:]...)
	return append(args, extra...)
}

// bundledCandidates 是随包目录下的候选相对路径（前者优先）。
var bundledCandidates = []string{
	filepath.Join("shared", "bin", "mpv"), // Linux quick-sharun AppImage 目录
	filepath.Join("mpv.app", "Contents", "MacOS", "mpv"),
	"mpv.exe",
	filepath.Join("AppRun"), // 旧式 Linux AppImage 解包布局
	"mpv",
}

// bundledRoots 返回随包 mpv/ 目录的候选父目录。QAA_MPV_DIR 里的内容直接就是
// mpv 运行时；其余布局把运行时放在名为 mpv 的子目录中。
func bundledRoots() []string {
	var roots []string
	if dir := strings.TrimSpace(os.Getenv("QAA_MPV_DIR")); dir != "" {
		roots = append(roots, dir)
	}
	exe, err := os.Executable()
	if err != nil {
		return roots
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	roots = append(roots,
		filepath.Join(dir, "mpv"),
		filepath.Join(dir, "..", "Resources", "mpv"),
		filepath.Join(dir, "usr", "share", "quaver-astra", "mpv"),
		filepath.Join(dir, "..", "share", "quaver-astra", "mpv"),
	)
	return roots
}

// resolveBundled 在一个随包运行时目录中定位可用载荷。
func resolveBundled(root string) (mpvExecutable, bool) {
	for _, rel := range bundledCandidates {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if !isExecFile(p) {
			continue
		}
		if filepath.ToSlash(rel) == "shared/bin/mpv" {
			command, ok := quickSharunCommand(root, p)
			if ok {
				return mpvExecutable{
					Path:               p,
					Origin:             "bundled",
					command:            command,
					bundledQuickSharun: true,
				}, true
			}
			continue
		}
		return mpvExecutable{Path: p, Origin: "bundled", command: []string{p}}, true
	}
	return mpvExecutable{}, false
}

// quickSharunCommand 构造 pkgforge AppImage 目录的安全启动命令：
// 包内 loader + 包内 lib.path + shared/bin/mpv。
func quickSharunCommand(root, payload string) ([]string, bool) {
	libDir := filepath.Join(root, "lib")
	entries, err := os.ReadDir(libDir)
	if err != nil {
		return nil, false
	}
	loader := ""
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "ld-linux") && strings.Contains(name, ".so") {
			loader = filepath.Join(libDir, name)
			break
		}
	}
	if loader == "" || !isExecFile(loader) {
		return nil, false
	}

	paths := []string{libDir}
	if data, err := os.ReadFile(filepath.Join(libDir, "lib.path")); err == nil {
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if !strings.HasPrefix(line, "+") {
				continue
			}
			sub := strings.TrimPrefix(line, "+")
			sub = strings.TrimPrefix(sub, "/")
			if sub != "" {
				paths = append(paths, filepath.Join(libDir, filepath.FromSlash(sub)))
			}
		}
	}
	return []string{loader, "--library-path", strings.Join(paths, string(os.PathListSeparator)), payload}, true
}

// 查找顺序：显式配置 → 随包目录 → PATH。
//
// 显式指定（QAA_MPV 含路径分隔符）时不替用户纠错：让 spawn 把真实错误报出来。
func resolveMPV() (mpvExecutable, error) {
	configured := strings.TrimSpace(os.Getenv("QAA_MPV"))
	if configured != "" {
		if strings.ContainsAny(configured, `/\`) {
			return mpvExecutable{Path: configured, Origin: "explicit", command: []string{configured}}, nil
		}
		if path, err := lookPath(configured); err == nil {
			return mpvExecutable{Path: path, Origin: "explicit", command: []string{path}}, nil
		}
		return mpvExecutable{Path: configured, Origin: "explicit", command: []string{configured}}, nil
	}

	for _, root := range bundledRoots() {
		if bin, ok := resolveBundled(root); ok {
			return bin, nil
		}
	}

	name := defaultMPVName()
	return mpvExecutable{Path: name, Origin: "PATH", command: []string{name}}, nil
}

// defaultMPVName 是各平台 PATH 中的可执行文件名。
func defaultMPVName() string {
	if runtime.GOOS == "windows" {
		return "mpv.com" // .com 版才会把输出写到 stdout
	}
	return "mpv"
}

func isExecFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

// childEnv 构造 mpv 子进程环境。
//
// quick-sharun 必须由包内 loader 直启，宿主 LD_LIBRARY_PATH 要清掉；旧式
// AppRun 目录布局则把 lib 追加到既有路径末尾，优先使用宿主音频输出库。
func childEnv(bin mpvExecutable) []string {
	env := os.Environ()
	if bin.bundledQuickSharun {
		return removeEnv(env, "LD_LIBRARY_PATH")
	}

	path := bin.Path
	if !strings.ContainsAny(path, `/\`) {
		return nil
	}
	dir := filepath.Dir(path)
	candidates := []string{
		filepath.Join(dir, "lib"),
		filepath.Join(dir, "usr", "lib"),
		filepath.Join(filepath.Dir(dir), "lib"),
	}
	var extra []string
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			extra = append(extra, p)
		}
	}
	if len(extra) == 0 {
		return nil
	}

	prev := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "LD_LIBRARY_PATH=") {
			prev = strings.TrimPrefix(kv, "LD_LIBRARY_PATH=")
		}
	}
	merged := strings.Join(extra, string(os.PathListSeparator))
	if prev != "" {
		merged = prev + string(os.PathListSeparator) + merged
	}
	return append(removeEnv(env, "LD_LIBRARY_PATH"), "LD_LIBRARY_PATH="+merged)
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}

// errNotFound 表示本机找不到可用的 mpv。
var errNotFound = errors.New("未找到 mpv")

func lookPath(name string) (string, error) { return exec.LookPath(name) }

// ProbeMPV 解析 mpv，并确认载荷可执行。启动兼容性由引擎首次 spawn 验证。
func ProbeMPV() (mpvExecutable, string, error) {
	bin, err := resolveMPV()
	if err != nil {
		return bin, "", err
	}
	path := bin.Path
	if !strings.ContainsAny(path, `/\`) {
		lp, lerr := lookPath(path)
		if lerr != nil {
			return bin, "未安装 mpv", errNotFound
		}
		path = lp
		bin.Path = path
		bin.command = []string{path}
	} else if !isExecFile(path) {
		return bin, "未安装 mpv", errNotFound
	}
	return bin, path, nil
}
