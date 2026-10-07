package appui

import (
	"sync"

	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"

	"github.com/egoist/mygo/ui"
)

// Quaver 调色板（对齐主项目 ui/src/style.css 的 CSS 变量）。
type palette struct {
	bg, card, side   ui.Color
	ink, ink2        ui.Color
	acc, hover, line ui.Color
	heart            ui.Color
	npBase           ui.Color
}

var (
	palLight = palette{
		bg:    ui.Hex("#f7f7f8"),
		card:  ui.Hex("#ffffff"),
		side:  ui.Hex("#ececec"),
		ink:   ui.Hex("#1f2329"),
		ink2:  ui.Hex("#6b7280"),
		acc:   ui.Hex("#2f7d5c"),
		hover: ui.Hex("#e3e3e4"),
		line:  ui.Hex("#d9d9db"),
		heart: ui.Hex("#e8465a"),
	}
	palDark = palette{
		bg:    ui.Hex("#131417"),
		card:  ui.Hex("#232529"),
		side:  ui.Hex("#1b1d21"),
		ink:   ui.Hex("#e6e8ec"),
		ink2:  ui.Hex("#a6adb8"),
		acc:   ui.Hex("#6cc79c"),
		hover: ui.Hex("#2e3138"),
		line:  ui.Hex("#3a3d45"),
		heart: ui.Hex("#e8465a"),
	}
)

// tintState 是封面取色的动态主题色。
type tintState struct {
	mu     sync.Mutex
	ok     bool
	res    colorprobe.Result
	accent ui.Color
	glow   ui.Color
}

func (t *tintState) set(r colorprobe.Result, dark bool) {
	rr, gg, bb := colorprobe.Accent(r, dark)
	g1, g2, g3 := colorprobe.Glow(r)
	t.mu.Lock()
	t.ok = true
	t.res = r
	t.accent = ui.RGB(rr, gg, bb)
	t.glow = ui.RGB(g1, g2, g3)
	t.mu.Unlock()
}

func (t *tintState) result() (colorprobe.Result, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.res, t.ok
}

func (t *tintState) get() (accent, glow ui.Color, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.accent, t.glow, t.ok
}

func (t *tintState) reset() {
	t.mu.Lock()
	t.ok = false
	t.mu.Unlock()
}

// baseFontSize 是应用设计的基准字号（DIP）：界面上写死的字号都以它为参照。
const baseFontSize = 14

// fontScale 是系统字号设置对应用字号的缩放，每帧随主题更新：桌面界面
// 字号（GTK gtk-font-name 的磅值）相对基准字号，再乘桌面的文字缩放
// （GNOME 的 text-scaling-factor）。字号跟着系统设置走，fz 负责折算。
var fontScale float32 = 1

func fz(v float32) float32 { return v * fontScale }

// buildTheme 组装 Quaver 风格主题（跟随系统明暗 + 系统字号 + 封面色覆盖）。
func (a *App) buildTheme(c *ui.Context) *ui.Theme {
	base := c.Theme()
	dark := base.Dark
	p := palLight
	if dark {
		p = palDark
	}
	t := *base
	t.Background = p.bg
	t.Surface = p.card
	t.SurfaceHover = p.hover
	t.Border = p.line
	t.Text = p.ink
	t.TextMuted = p.ink2
	t.Accent = p.acc
	t.AccentText = ui.Hex("#ffffff")
	t.Danger = p.heart
	t.Radius = 8
	prefs := c.Preferences()
	fontScale = prefs.TextScale
	if ui := prefs.UIFontSize; ui > 0 {
		fontScale *= ui / baseFontSize
	}
	if fontScale <= 0 {
		fontScale = 1
	}
	t.FontSize = fz(baseFontSize)
	if accent, glow, ok := a.tint.get(); ok {
		t.Accent = accent
		a.glowNow = glow
	} else {
		a.glowNow = ui.Hex("#19c2d8")
	}
	return &t
}
