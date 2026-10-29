package appui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"

	"github.com/egoist/mygo/ui"
)

// Quaver 调色板：暖灰纸面 + 墨色文字，不再沿用系统设置页那种冷白玻璃感。
// 这是 Astra 自己的视觉口径：像一张放在工作台上的唱片封套，而不是一块
// 悬浮的 macOS 面板。
type palette struct {
	bg, card, side     ui.Color
	ink, ink2          ui.Color
	hover, line, heart ui.Color
}

var (
	palLight = palette{
		bg:    ui.Hex("#f3efe6"),
		card:  ui.Hex("#fbf8f1"),
		side:  ui.Hex("#e9e3d7"),
		ink:   ui.Hex("#1d201c"),
		ink2:  ui.Hex("#686a61"),
		hover: ui.Hex("#e7e1d5"),
		line:  ui.Hex("#d4cdbf"),
		heart: ui.Hex("#d94b54"),
	}
	palDark = palette{
		bg:    ui.Hex("#101412"),
		card:  ui.Hex("#1a201c"),
		side:  ui.Hex("#151a16"),
		ink:   ui.Hex("#ece8dc"),
		ink2:  ui.Hex("#9d9f94"),
		hover: ui.Hex("#252c27"),
		line:  ui.Hex("#374039"),
		heart: ui.Hex("#ef666f"),
	}
)

const (
	accentModeKey   = "Style.AccentMode"
	accentCustomKey = "Style.AccentCustom"
)

// defaultAccent 是不依赖封面、桌面主题或用户配置的 Quaver 默认青色。
var defaultAccent = ui.Hex("#19c2d8")

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

// npPop* 是正在播放页浮层（⋮ 菜单、音频流信息）的深玻璃口径，对齐主项目
// .np-menu / .np-qinfo：这一页恒为深色，浮层跟着走白字，不随明暗主题翻面。
var (
	npPopBg     = ui.RGBA(0x10, 0x13, 0x1c, 0.85) // #10131cd9
	npPopBorder = ui.RGBA(255, 255, 255, 0.1)     // #ffffff1a
	npPopText   = ui.Hex("#ffffff")               // 悬停/强调字
	npPopDim    = ui.RGBA(255, 255, 255, 0.73)    // #fffb 常规项字
	npPopText2  = ui.RGBA(255, 255, 255, 0.53)    // #fff8 次级字
	npPopHover  = ui.RGBA(255, 255, 255, 0.08)    // #ffffff14 项悬停底
	npPopLine   = ui.RGBA(255, 255, 255, 0.1)     // 头行下的分隔线
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

// fontScale 是系统文字缩放对应用字号的缩放，每帧随主题更新；字号跟着
// 系统设置走，fz 负责折算。
var fontScale float32 = 1

func fz(v float32) float32 { return v * fontScale }

// styleAccent 把强调色派生成悬停/按下态：深色提亮、浅色压暗。
func styleAccent(t *ui.Theme, accent ui.Color, dark bool) {
	accent.A = 255
	t.Accent = accent
	t.Selection = accent.Alpha(0.25)
	t.Focus = accent.Alpha(0.55)
	// 自定义色和系统色可能很亮（例如黄色、青色），固定白字会失去可读性。
	// 在黑白中选择 WCAG 对比度更高的一边。
	black, white := ui.Hex("#18181b"), ui.Hex("#ffffff")
	if colorLuminance(accent) > 0.179 { // 黑/白对比度相等的亮度分界
		t.AccentText = black
	} else {
		t.AccentText = white
	}
	if dark {
		t.AccentHover = accent.Mix(white, 0.18)
		t.AccentPressed = accent.Mix(white, 0.3)
	} else {
		t.AccentHover = accent.Mix(black, 0.12)
		t.AccentPressed = accent.Mix(black, 0.22)
	}
}

// colorLuminance 返回 WCAG 相对亮度。
func colorLuminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// parseAccentColor 解析配置中的 #rgb/#rgba/#rrggbb/#rrggbbaa。强调色始终
// 按不透明色使用；损坏或手工写错的配置不会让 ui.Hex panic。
func parseAccentColor(s string) (ui.Color, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(h) {
	case 3, 4:
		var rgb [3]uint8
		for i := range len(h) {
			v, err := strconv.ParseUint(h[i:i+1], 16, 8)
			if err != nil {
				return ui.Color{}, false
			}
			if i < len(rgb) {
				rgb[i] = uint8(v) * 17
			}
		}
		return ui.RGB(rgb[0], rgb[1], rgb[2]), true
	case 6, 8:
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return ui.Color{}, false
		}
		if len(h) == 8 {
			v >>= 8 // 配置可兼容 #rrggbbaa，但强调色固定不透明。
		}
		return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v)), true
	default:
		return ui.Color{}, false
	}
}

func accentHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// accentMode 把未知配置收口到默认青色，便于旧版本和手工编辑配置升级。
func (a *App) accentMode() string {
	mode := a.Conf.String(accentModeKey, "default")
	switch mode {
	case "default", "cover", "system", "custom":
		return mode
	default:
		return "default"
	}
}

// configuredAccent 返回本帧的强调色和播放进度辅色。封面模式保留取色器
// 调出的 glow；其余模式让进度条与控件使用同一个用户选择的颜色。
func (a *App) configuredAccent(c *ui.Context) (accent, glow ui.Color) {
	accent = defaultAccent
	glow = defaultAccent
	switch a.accentMode() {
	case "cover":
		if tinted, g, ok := a.tint.get(); ok {
			return tinted, g
		}
	case "system":
		if system := c.Preferences().Accent; system.A > 0 {
			system.A = 255
			return system, system
		}
	case "custom":
		if custom, ok := parseAccentColor(a.Conf.String(accentCustomKey, "")); ok {
			return custom, custom
		}
	}
	return accent, glow
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

// fontModeNames 是字体设置的模式值与展示名。system 使用 Fontconfig 的默认
// sans-serif，避免 MyGo 的 system-ui 展开把 CJK fallback 错选到日文字体。
var fontModeNames = []struct{ key, label string }{
	{"system", "系统"},
	{"serif", "衬线"},
	{"sans-serif", "非衬线"},
	{"monospace", "等宽"},
	{"custom", "自定义"},
}

// configuredFont 把设置里的字体模式解析成传给 MyGo 的 family list。
// 自定义模式允许写 family list（逗号分隔），空值回到系统默认。
func (a *App) configuredFont(modeKey, customKey string) string {
	switch a.Conf.String(modeKey, "system") {
	case "serif":
		return "serif"
	case "sans-serif":
		return "sans-serif"
	case "monospace":
		return "monospace"
	case "custom":
		if family := strings.TrimSpace(a.Conf.String(customKey, "")); family != "" {
			return family
		}
	}
	return "sans-serif"
}

// lyricFontFamily 是歌词文字（包括翻译和逐字高亮）的 family list。
func (a *App) lyricFontFamily() string {
	return a.configuredFont("Style.LyricFontMode", "Style.LyricFontCustom")
}

// buildTheme 组装 Quaver 风格主题（明暗、系统字号和可选强调色）。
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
	t.Danger = p.heart
	t.Radius = 10
	// 强调色（含悬停/按下/选择/焦点环）统一从用户选择解析，避免某些
	// 状态退回 MyGo 默认蓝。
	accent, glow := a.configuredAccent(c)
	styleAccent(&t, accent, dark)
	a.glowNow = glow

	prefs := c.Preferences()
	fontScale = prefs.TextScale
	if fontScale <= 0 {
		fontScale = 1
	}
	t.Font = a.configuredFont("Style.FontMode", "Style.FontCustom")
	t.FontSize = fz(baseFontSize)
	return &t
}
