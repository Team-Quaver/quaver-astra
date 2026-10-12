package appui

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/egoist/mygo/ui"
)

func TestAccentModes(t *testing.T) {
	app := &App{Conf: conf.Open(t.TempDir())}
	app.Conf.Set("Style.Theme", "light")

	var got *ui.Theme
	tester := ui.NewTester(func(c *ui.Context) {
		got = app.buildTheme(c)
		c.SetTheme(got)
	}, 320, 200)
	frame := func() *ui.Theme {
		t.Helper()
		got = nil
		tester.Frame()
		if got == nil {
			t.Fatal("视图没有运行")
		}
		return got
	}

	if th := frame(); th.Accent != defaultAccent || app.glowNow != defaultAccent {
		t.Fatalf("默认高亮色 = %v / %v，期望青色 %v", th.Accent, app.glowNow, defaultAccent)
	}
	if th := got; th.AccentText != ui.Hex("#18181b") {
		t.Errorf("默认青色上的文字 = %v，期望深色文字", th.AccentText)
	}

	cover := colorprobe.Result{H: 18, S: 0.58, L: 0.42}
	app.tint.set(cover, false)
	wantCover, wantGlow, _ := app.tint.get()
	app.Conf.Set(accentModeKey, "cover")
	if th := frame(); th.Accent != wantCover || app.glowNow != wantGlow {
		t.Errorf("封面高亮色 = %v / %v，期望 %v / %v", th.Accent, app.glowNow, wantCover, wantGlow)
	}

	system := ui.RGBA(250, 204, 21, 0.4)
	app.Conf.Set(accentModeKey, "system")
	tester.SetPreferences(ui.Preferences{Accent: system})
	if got.Accent != ui.RGB(250, 204, 21) || app.glowNow != ui.RGB(250, 204, 21) {
		t.Errorf("系统高亮色 = %v / %v，期望不透明的系统色", got.Accent, app.glowNow)
	}
	if got.AccentText != ui.Hex("#18181b") {
		t.Errorf("亮系统色上的文字 = %v，期望深色文字", got.AccentText)
	}

	app.Conf.Set(accentModeKey, "custom")
	app.Conf.Set(accentCustomKey, "#7c3aed80")
	if th := frame(); th.Accent != ui.Hex("#7c3aed") || app.glowNow != ui.Hex("#7c3aed") {
		t.Errorf("自定义高亮色 = %v / %v，期望 #7c3aed", th.Accent, app.glowNow)
	}

	app.Conf.Set(accentCustomKey, "not-a-color")
	if th := frame(); th.Accent != defaultAccent || app.glowNow != defaultAccent {
		t.Errorf("损坏的自定义色没有回退默认青色：%v / %v", th.Accent, app.glowNow)
	}

	app.Conf.Set(accentModeKey, "unknown")
	if th := frame(); th.Accent != defaultAccent {
		t.Errorf("未知高亮模式没有回退默认青色：%v", th.Accent)
	}
}

func TestSystemAccentFallsBackWhenDesktopHasNone(t *testing.T) {
	app := &App{Conf: conf.Open(t.TempDir())}
	app.Conf.Set(accentModeKey, "system")
	var got ui.Color
	tester := ui.NewTester(func(c *ui.Context) { got, _ = app.configuredAccent(c) }, 200, 100)
	tester.SetPreferences(ui.Preferences{})
	if got != defaultAccent {
		t.Fatalf("无系统高亮色时得到 %v，期望 %v", got, defaultAccent)
	}
}

func TestParseAccentColor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ui.Color
		ok   bool
	}{
		{"#0cf", ui.Hex("#00ccff"), true},
		{"abcd", ui.Hex("#aabbcc"), true},
		{"112233", ui.Hex("#112233"), true},
		{"#11223344", ui.Hex("#112233"), true},
		{"#12xz", ui.Color{}, false},
		{"#112233zz", ui.Color{}, false},
		{"", ui.Color{}, false},
	} {
		got, ok := parseAccentColor(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAccentColor(%q) = %v, %v；期望 %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestSettingsAccentSection 锁住设置页「高亮配色」区块：四种来源按钮都在，
// 点击会写入配置并渲染出各自的附加信息（系统色读取结果 / 自定义色值），
// 整页渲染不 panic。
func TestSettingsAccentSection(t *testing.T) {
	app, tester := sideTestApp(t, false)
	// 自定义草稿只在启动时从配置读一次；写入后强制重载，专用分支才能显示它。
	app.Conf.Set(accentCustomKey, "#7c3aed")
	app.settings.draftsLoaded = false

	tester.Frame()
	for _, label := range []string{"高亮配色", "默认青色", "封面颜色", "系统颜色", "自定义"} {
		mustFind(t, tester, label)
	}

	// 点击「系统颜色」：写入配置，并在没有系统色时给出回退提示。
	if err := tester.Click("系统颜色"); err != nil {
		t.Fatalf("点击「系统颜色」失败：%v", err)
	}
	tester.SetPreferences(ui.Preferences{})
	tester.Frame()
	if got := app.Conf.String(accentModeKey, ""); got != "system" {
		t.Fatalf("点击后 Style.AccentMode = %q，期望 system", got)
	}
	mustFind(t, tester, "当前系统/GTK 主题未提供高亮色，已回退到默认青色。")

	// 桌面给出系统色时，显示读到的（不透明）色值。
	tester.SetPreferences(ui.Preferences{Accent: ui.RGB(0xfa, 0xcc, 0x15)})
	tester.Frame()
	mustFind(t, tester, "已读取系统/GTK 高亮色 #facc15")

	// 点击「自定义」：出现颜色井与当前色值。
	if err := tester.Click("自定义"); err != nil {
		t.Fatalf("点击「自定义」失败：%v", err)
	}
	tester.Frame()
	mustFind(t, tester, "自定义颜色")
	mustFind(t, tester, "#7c3aed")
}
