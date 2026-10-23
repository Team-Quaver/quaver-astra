// Quaver Astra（Native）—— 轻量化的 Quaver Astra 客户端。
// 全 Go：MyGo 原生 UI（无 WebView）+ Typhoeus-go 后端进程内嵌。
package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
	_ "unsafe"

	"github.com/Team-Quaver/quaver-astra/internal/appid"
	"github.com/Team-Quaver/quaver-astra/internal/appui"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/buildinfo"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/vault"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// MyGo 只从打包元数据/链接期变量读取系统侧应用 ID（Windows 资源、macOS
// bundle、Linux XDG / MPRIS 注册共用）；裸 go build 也要保持稳定，因此这里
// 在 init 前把它写入 MyGo 的包级元数据。值见 internal/appid。
//
//go:linkname mygoPackageIdentifier github.com/egoist/mygo.packageIdentifier
var mygoPackageIdentifier string

// version 由发布 CI 注入（取自 git tag）；本地 go build 保留 dev 标记，
// 由 buildinfo.Resolve 回落到工作区里的 git tag。
var version = "dev"

func init() { mygoPackageIdentifier = appid.Identifier }

// startupShowGrace 是窗口等首帧的上限。原生 UI 的窗口在隐藏状态下，
// 某些平台会推迟绘制；超过这个时间就显示窗口并让系统先画窗口底色，
// 不能因为「等就绪」把启动变成永远不出现的进程。启动动画约 4.6s，
// 保险丝不能抢在它前面把还没画完的第一帧显示出来。
const startupShowGrace = 5 * time.Second

func main() {
	// 必须早于 GTK/OpenGL 首次加载；见 gpu_linux.go。
	configureGPUEnvironment()

	// 桌面壳/二进制注册名统一为 Quaver Astra（应用菜单、托盘与系统注册表）。
	mygo.App.SetName("Quaver Astra")
	mygo.App.SetVersion(version)

	// 单实例锁：第二实例把命令行转交给首实例后直接退出。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	cfgDir := configDir()
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		fatal(err)
	}

	// 内嵌后端（必须在构造前设好 QUAVER_CONFIG_DIR）；凭证经管道交接、加密落盘
	srv, err := backend.Start(cfgDir, vault.Open(cfgDir))
	if err != nil {
		fatal(err)
	}
	defer srv.Shutdown()

	store := conf.Open(cfgDir)
	app := appui.New(srv.BaseURL(), store, vault.Open(cfgDir), buildinfo.Resolve(version))
	// 正常退出与异常返回两条路径都收口 mpv；OnQuit 在事件循环结束后执行，
	// 不会被窗口关闭到托盘误触发。
	defer app.Close()
	mygo.App.OnQuit(app.Close)

	var mainWin *mygo.Window

	// 第二实例启动时把已有窗口拉回前台。
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		if mainWin != nil {
			mainWin.Show()
			mainWin.Focus()
		}
	})

	mygo.App.WhenReady(func() {
		// 应用/窗口图标使用产品图标；托盘继续使用 tray.go 自己的模板，
		// 两者职责不同，不能互相替换。
		_ = mygo.App.Dock.SetIcon(appui.AppIconPNG())
		mainWin = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Quaver Astra",
			Width:     1280,
			Height:    800,
			MinWidth:  940,
			MinHeight: 600,
			Frameless: true,
			Hidden:    true,
			// 首帧前不能是一块纯白：Windows 的 DWM/显卡驱动初始化与
			// macOS 的 Metal drawable 都可能晚于窗口创建。按配置直接
			// 铺主题底色，系统明暗则跟随 MyGo 的 light-dark 选项。
			BackgroundColor: appui.WindowBackground(store),
			StateKey:        "main",
			Content:         ui.View(app.View),
		})
		_ = mainWin.SetIcon(appui.AppIconPNG())
		app.ApplyThemeSource()
		app.Attach(mainWin)
		// 保险丝：首帧始终没来（平台不画隐藏窗口、驱动/合成器异常）
		// 时也要把窗口交还给用户，而不是留一个没有窗口的后台进程。
		// 正常首帧先到就只显示一次；超时后主线程才执行 Show。
		var shown atomic.Bool
		show := startupShowOnce(&shown, func() {
			// 平台没有发首帧时，OnReadyToShow 不会来；在显示前
			// 再校准一次底色（尤其配置为深色时）。
			_ = app.ApplyWindowBackground()
			mainWin.Show()
		})
		mainWin.OnReadyToShow(show)
		time.AfterFunc(startupShowGrace, show)
	})
	if err := mygo.App.Run(); err != nil {
		// fatal 会直接 os.Exit，普通 defer 不再有机会执行；先收掉 mpv。
		app.Close()
		fatal(err)
	}
}

// startupShowOnce 返回只会执行一次 fn 的显示回调。首帧与超时保险丝
// 可能同时到达；Show 虽幂等，但重复激活会抢焦点，这里只放行一次。
func startupShowOnce(shown *atomic.Bool, fn func()) func() {
	return func() {
		if shown.CompareAndSwap(false, true) {
			fn()
		}
	}
}

func configDir() string {
	if d := os.Getenv("QUAVER_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "quaver-astra")
	}
	return filepath.Join(base, "quaver-astra")
}

func fatal(err error) {
	os.Stderr.WriteString("quaver-astra: " + err.Error() + "\n")
	os.Exit(1)
}
