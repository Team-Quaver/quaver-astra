package appui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/update"
)

func TestUpdateStatusText(t *testing.T) {
	checked := time.Date(2026, 11, 11, 12, 30, 0, 0, time.Local)
	cases := []struct {
		name      string
		available string
		lastCheck time.Time
		installed string
		want      string
	}{
		{"available", "1.5.0", checked, "", "发现新版本 1.5.0"},
		{"up to date", "", checked, "", "已是最新版本"},
		{"not checked", "", time.Time{}, "", "尚未检查"},
		{"installed", "1.5.0", checked, "1.5.0", "已更新到 1.5.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := updateStatusText(c.available, c.lastCheck, c.installed); !strings.Contains(got, c.want) {
				t.Fatalf("updateStatusText = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSettingsUpdateShowsWindowEntry 断言「关于」页把更新操作交给插件窗口，
// 且没有可用更新时不再显示「查看更新」。
func TestSettingsUpdateShowsWindowEntry(t *testing.T) {
	app, tester := testApp(t)
	app.settings.tab = aboutTab
	app.Version = "1.4.0"
	app.upd = updateState{
		form:      update.FormAppImage,
		available: "1.5.0",
		lastCheck: time.Now(),
	}
	tester.Frame()
	if !tester.HasText("检查更新") || !tester.HasText("查看更新") {
		t.Fatalf("about page should offer the updater window; texts=%v", tester.Texts())
	}

	app.upd.available = ""
	tester.Frame()
	if tester.HasText("查看更新") {
		t.Fatalf("up-to-date build should not offer an update window entry")
	}
}

// TestSettingsUpdateSystemPackageOpensRelease 断言系统包安装形态下只提供
// 发行页入口，不做原地替换。
func TestSettingsUpdateSystemPackageOpensRelease(t *testing.T) {
	app, tester := testApp(t)
	app.settings.tab = aboutTab
	app.upd = updateState{form: update.FormSystemPackage}
	tester.Frame()
	if !tester.HasText("打开发行页") {
		t.Fatalf("system package should fall back to release page; texts=%v", tester.Texts())
	}
	if tester.HasText("查看更新") {
		t.Fatalf("system package must not offer the in-place updater window")
	}
}

// TestSettingsAboutShowsVersion 断言「关于」页标题旁显示当前版本号。
func TestSettingsAboutShowsVersion(t *testing.T) {
	app, tester := testApp(t)
	app.settings.tab = aboutTab
	app.Version = "1.2.3"
	tester.Frame()
	if !tester.HasText("Quaver Astra（1.2.3）") && !tester.HasText("（1.2.3）") {
		t.Fatalf("about should show version next to title; texts=%v", tester.Texts())
	}
}

// TestUpdaterCheckAdaptersRelease 断言插件检查拿到的是当前渠道的 GitHub
// Release，并带回 Markdown 说明与可调用的安装闭包。
func TestUpdaterCheckAdaptersRelease(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("fixture names a linux-amd64 asset")
	}
	app, _ := testApp(t)
	app.Version = "9.1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+update.Repo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(update.Release{
			Tag:     "v9.2",
			Name:    "Quaver Astra 9.2",
			Notes:   "## 更新\n\n![截图](https://example.com/a.png)",
			HTMLURL: "https://example.com/releases/9.2",
			Assets: []update.Asset{{
				Name: "Quaver-Astra-9.2-linux-amd64.AppImage",
				URL:  "https://example.com/app.AppImage",
			}},
		})
	}))
	defer srv.Close()
	app.Upd = &update.Client{Repo: update.Repo, APIBase: srv.URL, HTTP: srv.Client()}

	rel, err := app.updaterCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel == nil || rel.Version != "9.2" || rel.Notes == "" || rel.Install == nil {
		t.Fatalf("updaterCheck = %+v", rel)
	}
}
