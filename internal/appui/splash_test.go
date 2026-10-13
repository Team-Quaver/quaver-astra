package appui

import (
	"image/png"

	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSplashHiddenWithoutAttach：测试里直接构造的 App 未走 Attach，
// splashAt 为零值，启动画面恒不显示——首帧与既有定妆照不能被遮盖。
func TestSplashHiddenWithoutAttach(t *testing.T) {
	app, tester := sideTestApp(t, false)
	tester.Frame()
	if app.splashDone {
		t.Fatal("splashAt 为零值时不应把 splashDone 置真")
	}
	if _, found := tester.Find("启动画面"); found {
		t.Fatal("splashAt 为零值时不应渲染启动画面")
	}
}

// TestSplashLifecycle：起算后进入时间轴（找得到启动画面层与署名字样），
// 时间轴走完后 splashDone 置真、层彻底撤掉。
func TestSplashLifecycle(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.splashAt = time.Now().Add(-3 * time.Second) // 3.0s：字标与署名均已出现，未到淡出
	tester.Frame()
	if _, found := tester.Find("启动画面"); !found {
		t.Fatal("时间轴中段应渲染启动画面层")
	}
	if !tester.HasText("Built by Team Quaver") {
		t.Fatal("时间轴中段应出现 Built by Team Quaver")
	}
	if app.splashDone {
		t.Fatal("3.0s 处不应已结束（淡出 4.0s 才开始）")
	}

	app.splashAt = time.Now().Add(-(splashFadeDelay + splashFadeDur + time.Second))
	tester.Frame()
	if !app.splashDone {
		t.Fatal("时间轴走完后应把 splashDone 置真")
	}
	if _, found := tester.Find("启动画面"); found {
		t.Fatal("splashDone 后不应再渲染启动画面层")
	}
}

// TestSplashSkip：Escape 把时钟拨到淡出起点，跳过后续动画。
func TestSplashSkip(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.splashAt = time.Now().Add(-time.Second) // 1.0s：动画进行中
	tester.Frame()
	if _, found := tester.Find("启动画面"); !found {
		t.Fatal("应找到启动画面层")
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := time.Since(app.splashAt); got < splashFadeDelay {
		t.Fatalf("Escape 跳过后时钟应在淡出起点，实际已过 %v", got)
	}
}

// TestSplashShots 把启动画面的几个关键帧导出成 PNG 定妆照（与
// TestUIShots 同一环境变量门控）：淡入中、星星亮起、字标形成、完整画面、淡出中。
func TestSplashShots(t *testing.T) {
	dir := os.Getenv("QUAVER_SHOTS_DIR")
	if dir == "" {
		t.Skip("设置 QUAVER_SHOTS_DIR 以导出启动画面定妆照")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	frames := []struct {
		name   string
		offset time.Duration
	}{
		{"splash-1-logo.png", 450 * time.Millisecond},
		{"splash-2-star.png", 1200 * time.Millisecond},
		{"splash-3-wordmark.png", 2600 * time.Millisecond},
		{"splash-4-full.png", 3500 * time.Millisecond},
		{"splash-5-fade.png", 4300 * time.Millisecond},
	}
	for _, mode := range []string{"light", "dark"} {
		for _, f := range frames {
			app, _ := sideTestApp(t, false)
			app.Conf.Set("Style.Theme", mode)
			// 固定相对帧时钟，慢机器上也不会把关键帧渲染成后续阶段。
			tester := ui.NewTester(func(c *ui.Context) {
				app.splashAt = c.Now().Add(-f.offset)
				app.View(c)
			}, 1000, 700)
			out, err := os.Create(filepath.Join(dir, mode+"-"+f.name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(out, tester.Image()); err != nil {
				out.Close()
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSplashThemeModes(t *testing.T) {
	app, _ := sideTestApp(t, false)
	tester := ui.NewTester(func(c *ui.Context) {
		app.splashAt = c.Now().Add(-3500 * time.Millisecond)
		app.View(c)
	}, 1000, 700)
	// 在同一个视图内切换，既覆盖强制主题优先级，也覆盖系统实时变化。
	for _, tc := range []struct {
		mode                 string
		systemDark, wantDark bool
	}{
		{"light", true, false}, {"dark", false, true},
		{"system", true, true}, {"system", false, false},
	} {
		app.Conf.Set("Style.Theme", tc.mode)
		tester.SetDark(tc.systemDark)
		tester.Frame()
		want := splashColors(tc.wantDark)
		if got := tester.Image().RGBAAt(4, 4); got.R != want.bg.R || got.G != want.bg.G || got.B != want.bg.B {
			t.Fatalf("mode=%s systemDark=%v 背景=%v，期望 %v", tc.mode, tc.systemDark, got, want.bg)
		}
		if app.dark.Load() != tc.wantDark {
			t.Fatal("Splash 与应用主题不同步")
		}
		if !tester.HasText("Built by Team Quaver") {
			t.Fatal("主题切换丢失署名")
		}
	}
}

func TestSplashPaletteContrastAndLogo(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := splashColors(dark)
		for name, color := range map[string]ui.Color{"title": p.ink, "credit": p.credits} {
			if ratio := contrastRatio(color, p.bg); ratio < 4.5 {
				t.Errorf("dark=%v %s 对比度=%.2f，期望至少 4.5", dark, name, ratio)
			}
		}
		if ratio := contrastRatio(p.accent, p.bg); ratio < 3 {
			t.Errorf("dark=%v 手写字/星星对比度=%.2f，期望至少 3", dark, ratio)
		}
		im := ui.Render(func(c *ui.Context) {
			ui.Box(c).Fill().Background(p.bg).Children(func() {
				ui.Image(c, p.logo).Size(512, 512)
			})
		}, 512, 512, 1)
		// 母版 (60,60) 落在奶油底板内部，透明派生图不能在此留下色块。
		for _, xy := range [][2]int{{60, 60}, {256, 450}} {
			got := im.RGBAAt(xy[0], xy[1])
			if got.R != p.bg.R || got.G != p.bg.G || got.B != p.bg.B {
				t.Errorf("dark=%v 图标残留底板：(%d,%d)=%v", dark, xy[0], xy[1], got)
			}
		}
		// 音符实心区：检查深色图标确实被提亮，而非只改了窗口背景。
		got := im.RGBAAt(150, 290)
		want := ui.Hex("#525da1")
		if dark {
			want = ui.Hex("#9ba5eb")
		}
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Errorf("dark=%v 音符=%v，期望 %v", dark, got, want)
		}
	}
}

func TestSplashWordmarkScale(t *testing.T) {
	app, _ := sideTestApp(t, false)
	tester := ui.NewTester(func(c *ui.Context) {
		app.splashAt = c.Now().Add(-3500 * time.Millisecond)
		app.View(c)
	}, 940, 600)
	for _, scale := range []float32{1, 1.5, 2} {
		tester.SetPreferences(ui.Preferences{TextScale: scale})
		q, qOK := tester.Find("Quaver")
		a, aOK := tester.Find("Astra")
		if !qOK || !aOK {
			t.Fatal("缺少字标")
		}
		if q.X < 0 || q.X+q.W > a.X || a.X+a.W > 940 {
			t.Errorf("scale=%v 字标重叠或越界：Quaver=%v Astra=%v", scale, q, a)
		}
	}
}
