// Quaver Astra（Native）—— 轻量化的 Quaver Astra 客户端。
// 全 Go：MyGo 原生 UI（无 WebView）+ Typhoeus-go 后端进程内嵌。
package main

import (
	"os"
	"path/filepath"
	_ "unsafe"

	"github.com/Team-Quaver/quaver-astra/internal/appui"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/vault"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// appIdentifier 是系统侧应用 ID（Windows 资源、macOS bundle、Linux XDG
// 注册共用）。MyGo 只从打包元数据/链接期变量读取它，裸 go build 也要保持
// 稳定，因此这里在 init 前写入 MyGo 的包级元数据。
const appIdentifier = "red.0w0.quaver-astra"

//go:linkname mygoPackageIdentifier github.com/egoist/mygo.packageIdentifier
var mygoPackageIdentifier string

// version 由发布 CI 注入；本地 go build 保留 dev 标记。
var version = "dev"

func init() { mygoPackageIdentifier = appIdentifier }

func main() {
	// 必须早于 GTK/OpenGL 首次加载；见 gpu_linux.go。
	configureGPUEnvironment()

	// 桌面壳/二进制注册名统一为 Quaver Astra（应用菜单、托盘与系统注册表）。
	mygo.App.SetName("Quaver Astra")
	mygo.App.SetVersion(version)

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
	app := appui.New(srv.BaseURL(), store, vault.Open(cfgDir))
	// 正常退出与异常返回两条路径都收口 mpv；OnQuit 在事件循环结束后执行，
	// 不会被窗口关闭到托盘误触发。
	defer app.Close()
	mygo.App.OnQuit(app.Close)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Quaver Astra",
			Width:     1280,
			Height:    800,
			MinWidth:  940,
			MinHeight: 600,
			Frameless: true,
			StateKey:  "main",
			Content:   ui.View(app.View),
		})
		app.ApplyThemeSource()
		app.Attach(win)
	})
	if err := mygo.App.Run(); err != nil {
		// fatal 会直接 os.Exit，普通 defer 不再有机会执行；先收掉 mpv。
		app.Close()
		fatal(err)
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
