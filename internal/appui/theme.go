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

// npBase 是正在播放页的兜底底色，npScrim* 是它的压暗遮罩（上→下）。
//
// 这一页恒为「模糊封面 + 深色遮罩」，不随明暗主题翻面（同主项目 .np /
// .np-scrim 的口径）：封面底色任意，深色遮罩 + 白字永远压得住。
// 遮罩只做轻度压暗——底图已经铺满模糊封面，压太狠会把封面氛围色也吃掉。
var (
	npBase                = ui.Hex("#0b0e19")
	npScrimTop            = ui.RGBA(0, 0, 0, 0.2)
	npScrimBottom         = ui.RGBA(0, 0, 0, 0.52)
	npBlurOpacity float32 = 0.55
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

// appearance 解析本帧的明暗外观：Style.Theme 是唯一真相源，只有 system
// 才跟随桌面（c.Theme().Dark）。
//
// 为什么不直接信 mygo 的 Theme.IsDark：mygo 的 Linux 后端 SetSource("light")
// 只清了 GTK 的 gtk-application-prefer-dark-theme，IsDark() 紧接着又去问
// XDG portal 的 color-scheme —— 深色桌面上 portal 说是深色，于是「强制浅色」
// 被 portal 覆盖，浅色模式完全无法生效（详见 setTheme 的注释）。
// 这里自己拿主意，跨平台行为一致。
func (a *App) appearance(c *ui.Context) bool {
	switch a.Conf.String("Style.Theme", "system") {
	case "light":
		return false
	case "dark":
		return true
	}
	return c.Theme().Dark
}

// baseFontSize 是应用设计的基准字号（DIP）：界面上写死的字号都以它为参照。
const baseFontSize = 14

// fontScale 是系统字号设置对应用字号的缩放，每帧随主题更新：桌面界面
// 字号（GTK gtk-font-name 的磅值）相对基准字号，再乘桌面的文字缩放
// （GNOME 的 text-scaling-factor）。字号跟着系统设置走，fz 负责折算。
var fontScale float32 = 1

func fz(v float32) float32 { return v * fontScale }

// styleAccent 把强调色派生成悬停/按下态：深色提亮、浅色压暗。
func styleAccent(t *ui.Theme, accent ui.Color, dark bool) {
	t.Accent = accent
	t.Selection = accent.Alpha(0.25)
	t.Focus = accent.Alpha(0.55)
	if dark {
		t.AccentHover = accent.Mix(ui.Hex("#ffffff"), 0.18)
		t.AccentPressed = accent.Mix(ui.Hex("#ffffff"), 0.3)
	} else {
		t.AccentHover = accent.Mix(ui.Hex("#000000"), 0.12)
		t.AccentPressed = accent.Mix(ui.Hex("#000000"), 0.22)
	}
}

// heartStyle 返回红心按钮该用的图标名与颜色。
//
// 未收藏（「无红心」状态）用正文色，不用 TextMuted：红心是一枚只有轮廓的线框
// 图标，笔画在 16~17px 下只有 1.2px 上下，抗锯齿会把覆盖率再砍一半；底色上再
// 降一档亮度就没有可读性了（用户报的「无红心状态的红心在深色模式无可读性」）。
// 正文色在浅/深两套主题下都是 10:1 以上的对比度（有回归测试守着）。
//
// 悬停时预演红心色（告诉用户点下去会变成什么），已收藏则是实心红心。
func heartStyle(loved, hover bool, t *ui.Theme) (icon string, col ui.Color) {
	switch {
	case loved:
		return "heartFill", t.Danger
	case hover:
		return "heart", t.Danger
	}
	return "heart", t.Text
}

// buildTheme 组装 Quaver 风格主题（明暗按 Style.Theme + 系统字号 + 封面色）。
func (a *App) buildTheme(c *ui.Context) *ui.Theme {
	base := c.Theme()
	dark := a.appearance(c)
	a.dark.Store(dark)
	p := palLight
	if dark {
		p = palDark
	}
	t := *base
	t.Dark = dark
	t.Background = p.bg
	t.Surface = p.card
	t.SurfaceHover = p.hover
	t.Border = p.line
	t.Text = p.ink
	t.TextMuted = p.ink2
	t.AccentText = ui.Hex("#ffffff")
	t.Danger = p.heart
	t.Radius = 8
	// 强调色（含悬停/按下/选择/焦点环）——封面色覆盖时同样重算，
	// 否则按钮悬停会跳回 mygo 默认蓝。
	accent := p.acc
	glow := ui.Hex("#19c2d8")
	if tinted, g, ok := a.tint.get(); ok {
		accent, glow = tinted, g
	}
	styleAccent(&t, accent, dark)
	a.glowNow = glow

	prefs := c.Preferences()
	fontScale = prefs.TextScale
	if ui := prefs.UIFontSize; ui > 0 {
		fontScale *= ui / baseFontSize
	}
	if fontScale <= 0 {
		fontScale = 1
	}
	t.FontSize = fz(baseFontSize)
	return &t
}
