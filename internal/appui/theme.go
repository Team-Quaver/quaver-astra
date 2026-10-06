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

// buildTheme 组装 Quaver 风格主题（跟随系统明暗 + 封面色覆盖）。
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
	t.FontSize = 14
	if accent, glow, ok := a.tint.get(); ok {
		t.Accent = accent
		a.glowNow = glow
	} else {
		a.glowNow = ui.Hex("#19c2d8")
	}
	return &t
}
