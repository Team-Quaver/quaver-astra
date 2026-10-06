// Quaver Astra（Native）—— 轻量化的 Quaver Music 客户端。
// 全 Go：MyGo 原生 UI（无 WebView）+ Typhoeus-go 后端进程内嵌。
package main

import (
	"os"
	"path/filepath"

	"github.com/Team-Quaver/quaver-astra/internal/appui"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/vault"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
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
