package appui

import (
	"strings"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/update"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2 KiB"},
		{3 * 1024 * 1024, "3 MiB"},
		{1536 * 1024, "1.5 MiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUpdateStatusText(t *testing.T) {
	rel := &update.Release{Tag: "v1.5.0", Version: "1.5.0", Channel: update.Stable, HTMLURL: "https://example.com"}

	a := &App{Version: "1.4.0"}
	a.upd = updateState{release: rel, relation: update.RelationNewer}
	if got := a.updateStatusText(); !strings.Contains(got, "发现新版本 1.5.0") {
		t.Errorf("newer status = %q", got)
	}

	a.upd.relation = update.RelationSame
	if got := a.updateStatusText(); !strings.Contains(got, "已是最新版本") {
		t.Errorf("same status = %q", got)
	}

	a.upd.relation = update.RelationSwitch
	if got := a.updateStatusText(); !strings.Contains(got, "可切换到 1.5.0") {
		t.Errorf("switch status = %q", got)
	}

	a.upd.err = "网络错误"
	if got := a.updateStatusText(); got != "网络错误" {
		t.Errorf("error status = %q", got)
	}

	a.upd.err = ""
	a.upd.installed = "1.5.0"
	if got := a.updateStatusText(); !strings.Contains(got, "已更新到 1.5.0") {
		t.Errorf("installed status = %q", got)
	}
}

// TestSettingsUpdateShowsInstall 断言「关于」页在发现可升级版本时给出安装入口，
// 且当前无可用更新时不显示。
func TestSettingsUpdateShowsInstall(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.settings.tab = aboutTab
	app.Version = "1.4.0"
	app.Upd = update.NewClient()
	app.upd = updateState{
		form:      update.FormAppImage,
		relation:  update.RelationNewer,
		release:   &update.Release{Tag: "v1.5.0", Version: "1.5.0", Channel: update.Stable, HTMLURL: "https://example.com"},
		checkedAt: time.Now(),
	}
	tester.Frame()
	if !tester.HasText("下载并安装") {
		t.Fatalf("about page should offer install; texts=%v", tester.Texts())
	}

	app.upd = updateState{form: update.FormAppImage, relation: update.RelationSame, release: app.upd.release}
	tester.Frame()
	if tester.HasText("下载并安装") {
		t.Fatalf("up-to-date build should not offer install")
	}
}

// TestSettingsUpdateSystemPackageOpensRelease 断言系统包安装形态下按钮改为
// 「打开发行页」，不做原地替换。
func TestSettingsUpdateSystemPackageOpensRelease(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.settings.tab = aboutTab
	app.Upd = update.NewClient()
	app.upd = updateState{
		form:     update.FormSystemPackage,
		relation: update.RelationNewer,
		release:  &update.Release{Tag: "v1.5.0", Version: "1.5.0", Channel: update.Stable, HTMLURL: "https://example.com"},
	}
	tester.Frame()
	if !tester.HasText("打开发行页") {
		t.Fatalf("system package should fall back to release page; texts=%v", tester.Texts())
	}
	if tester.HasText("下载并安装") {
		t.Fatalf("system package must not offer in-place install")
	}
}

// TestSettingsAboutShowsVersion 断言「关于」页标题旁显示当前版本号。
func TestSettingsAboutShowsVersion(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.settings.tab = aboutTab
	app.Version = "1.2.3"
	tester.Frame()
	if !tester.HasText("Quaver Astra（1.2.3）") && !tester.HasText("（1.2.3）") {
		t.Fatalf("about should show version next to title; texts=%v", tester.Texts())
	}
}
