package audio

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// mpv 可执行文件的定位策略，与 Quaver Astra 本体
// （ui/electron/audio/dist/bins.js）保持一致：
//
//	显式路径（QAA_MPV）→ 随包目录（QAA_MPV_DIR）→ PATH
//
// 之所以要「随包目录」这一层：发行版可以自带 mpv（AppImage 解包后是
// mpv/AppRun + mpv/lib 的目录形态），用户无需另装。只查 PATH 会漏掉它。
//
// 单独成文件而不塞进 mpv.go：定位逻辑要同时服务于「启动」「设置页展示」
// 「探测」，放在引擎文件里会被引擎的实现细节绑住。

// mpvExecutable 是一个已定位的 mpv 可执行文件。
type mpvExecutable struct {
	Path string // 绝对路径，或 PATH 中的裸名字
	// Origin 是来源：explicit / bundled / PATH。设置页要展示，
	// 让用户知道用的是自带还是系统的。
	Origin string
}

// bundledCandidates 是随包目录下的候选相对路径（前者优先）。
//
// mpv 有两种随包形态：官方 AppImage 解包后是 mpv/AppRun + mpv/lib/*
// （目录形态），单文件构建则是 mpv/mpv。两种都留着以免上游换布局。
var bundledCandidates = []string{
	filepath.Join("mpv", "AppRun"), // Linux AppImage 解包
	filepath.Join("mpv", "mpv"),    // 单文件构建
	"mpv.exe",                      // Windows 随包
	"mpv",                          // 直接就是 mpv 可执行文件
}

// 查找顺序：配置 → 随包目录 → 默认名字交给 PATH。
//
// 显式指定（QAA_MPV 含路径分隔符）时**不替他纠错**：让 spawn 把真实错误
// 报上来，比悄悄回落到 PATH 更能帮用户定位问题。
func resolveMPV() (mpvExecutable, error) {
	configured := strings.TrimSpace(os.Getenv("QAA_MPV"))
	if configured == "" {
		configured = defaultMPVName()
	}
	if strings.ContainsAny(configured, `/\`) {
		return mpvExecutable{Path: configured, Origin: "explicit"}, nil
	}

	if dir := strings.TrimSpace(os.Getenv("QAA_MPV_DIR")); dir != "" {
		for _, rel := range bundledCandidates {
			p := filepath.Join(dir, rel)
			if isExecFile(p) {
				return mpvExecutable{Path: p, Origin: "bundled"}, nil
			}
		}
		// 随包目录存在却没有可执行文件：继续回落 PATH。
		// AppImage 的挂载点是只读的，运行时 chmod 必然失败，
		// 与其 spawn 一个注定 EACCES 的路径，不如用系统的。
	}

	// 裸名字：交给 PATH 解析。若本机确实没有，这里返回的 Path 仍可用，
	// 真正的「找不到」由 spawn/exec 报出来（错误信息更准确）。
	return mpvExecutable{Path: configured, Origin: "PATH"}, nil
}

// defaultMPVName 是各平台的可执行文件名。
func defaultMPVName() string {
	if runtime.GOOS == "windows" {
		return "mpv.com" // .com 版才会把输出写到 stdout，.exe 是 GUI 版
	}
	return "mpv"
}

// isExecFile 判断 p 是否为可执行文件。
func isExecFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // Windows 上没有执行位，靠扩展名
	}
	return st.Mode()&0o111 != 0
}

// childEnv 构造子进程环境。
//
// 随包 mpv 常是「目录形态」（自带 codec/UI 库），要把它的 lib 目录追加进
// LD_LIBRARY_PATH。关键在**追加在既有路径之后**：音频输出（pipewire/pulse）
// 必须用宿主机的那些库——带上自己那份最容易出现「能解码但没声音」。
func childEnv(binPath string) []string {
	if !strings.ContainsAny(binPath, `/\`) {
		return nil // PATH 里的裸名字，用宿主环境即可
	}
	dir := filepath.Dir(binPath)
	candidates := []string{
		filepath.Join(dir, "lib"),        // resources/mpv/AppRun → resources/mpv/lib
		filepath.Join(dir, "usr", "lib"), // AppImage 的另一种内部布局
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

	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	env := os.Environ()
	prev := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "LD_LIBRARY_PATH=") {
			prev = strings.TrimPrefix(kv, "LD_LIBRARY_PATH=")
		}
	}
	merged := strings.Join(extra, sep)
	if prev != "" {
		merged = prev + sep + merged
	}
	return append(env, "LD_LIBRARY_PATH="+merged)
}

// errNotFound 表示本机找不到可用的 mpv。
var errNotFound = errors.New("未找到 mpv")

// lookPath 查 PATH（委托给 os/exec，行为与系统一致）。
func lookPath(name string) (string, error) { return exec.LookPath(name) }

// ProbeMPV 探测 mpv 是否可用，返回可展示的状态。设置页用它显示
// 「用哪个 mpv、能不能跑起来」。
func ProbeMPV() (mpvExecutable, string, error) {
	bin, err := resolveMPV()
	if err != nil {
		return bin, "", err
	}
	// 裸名字交给 LookPath 确认；绝对路径直接验可执行。
	path := bin.Path
	if !strings.ContainsAny(path, `/\`) {
		lp, lerr := lookPath(path)
		if lerr != nil {
			return bin, "未安装 mpv", errNotFound
		}
		path = lp
	} else if !isExecFile(path) {
		return bin, "未安装 mpv", errNotFound
	}
	bin.Path = path
	return bin, path, nil
}
