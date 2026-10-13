// splash.go —— 启动画面。不建独立窗口：直接在主窗口内容上盖一层
// Overlay，动画播完（或按 Escape 跳过）后整体淡出撤层，露出后面的界面。
//
// 编排（自窗口 Attach 起算）：
//
//	0.10s  无星 Logo 淡入（带一点上飘）
//	0.85s  星星亮起（缩放 + 淡入）
//	1.55s  “Quaver” 从左侧淡入、右移滑进字标位
//	2.10s  手写体 “Astra” 在其右侧浮现
//	2.70s  底部 “Built by Team Quaver” 淡入
//	4.00s  整体淡出，4.60s 撤层
package appui

import (
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/img"

	"github.com/egoist/mygo/ui"
)

// 启动画面的四个矢量素材。星星与无星 Logo 共用 512 视图，叠加对位；
// 两个转曲字标以 Icon 方式绘制，颜色跟随 TextColor。
var (
	splashLogo = ui.MustParseSVG(appicon.QuaverAstraStarlessSVG)
	// 深色只提亮品牌色；保留母版的路径、叠放和透明背景，解析仅在启动时做一次。
	splashLogoDark = ui.MustParseSVG([]byte(strings.NewReplacer(
		"#0047d6", "#9ba5eb", "#525da1", "#9ba5eb",
		"#2f823d", "#a4c77d", "#73a152", "#a4c77d",
	).Replace(string(appicon.QuaverAstraStarlessSVG))))
	splashStar   = ui.MustParseSVG(appicon.QuaverAstraStarSVG)
	splashQuaver = ui.MustParseSVG(appicon.QuaverWordmarkSVG)
	splashAstra  = ui.MustParseSVG(appicon.AstraWordmarkSVG)
)

// 沿用 buildTheme 的结果：强制浅色、强制深色和跟随系统都走同一入口。
// 浅色保留奶油纸感，深色与主界面同底，星星和手写字提亮为暖铜色。
type splashPalette struct {
	bg, ink, accent, credits ui.Color
	logo                     *ui.SVG
}

func splashColors(dark bool) splashPalette {
	if dark {
		return splashPalette{
			bg: palDark.bg, ink: ui.Hex("#f3eee5"),
			accent: ui.Hex("#dba88f"), credits: ui.Hex("#b1aaa1"),
			logo: splashLogoDark,
		}
	}
	return splashPalette{
		bg: ui.Hex("#fbf1d8"), ink: ui.Hex("#33302b"),
		accent: ui.Hex("#a16752"), credits: ui.Hex("#796a57"),
		logo: splashLogo,
	}
}

// 时间轴常量。淡出开始后 fade 进度乘在背景与内容列上（Painter 的
// opacity 沿子树累乘），各阶段动画只管自己的透明度与位移。Escape 可随时
// 跳过（拨到淡出起点）。
const (
	splashLogoDelay    = 100 * time.Millisecond
	splashLogoDur      = 600 * time.Millisecond
	splashStarDelay    = 850 * time.Millisecond
	splashStarDur      = 550 * time.Millisecond
	splashQuaverDelay  = 1550 * time.Millisecond
	splashQuaverDur    = 550 * time.Millisecond
	splashAstraDelay   = 2100 * time.Millisecond
	splashAstraDur     = 600 * time.Millisecond
	splashCreditDelay  = 2700 * time.Millisecond
	splashCreditDur    = 600 * time.Millisecond
	splashFadeDelay    = 4000 * time.Millisecond
	splashFadeDur      = 600 * time.Millisecond
	splashSlide        = 64 // “Quaver” 右移滑入的行程（DIPs）
	splashLogoSize     = 180
	splashQuaverHeight = 40
	splashAstraHeight  = 46
	splashWordGap      = 16
)

// splashShow 报告本帧是否还需要启动画面；时间到后顺手把 splashDone
// 置真，后续帧直接跳过。splashAt 为零值（测试里直接构造 App、未走
// Attach）时恒不显示——与 motion/enterState「零值 = 不播」的约定一致。
func (a *App) splashShow(c *ui.Context) bool {
	if a.splashDone || a.splashAt.IsZero() {
		return false
	}
	if c.Now().Sub(a.splashAt) >= splashFadeDelay+splashFadeDur {
		a.splashDone = true
		return false
	}
	return true
}

// splash 构建整层启动画面，只在 splashShow 为真时由 View 调用。
func (a *App) splash(c *ui.Context) {
	now := c.Now()
	colors := splashColors(c.Theme().Dark)
	fk, fading := (motion{start: a.splashAt, delay: splashFadeDelay, dur: splashFadeDur, ease: ui.EaseOut}).at(now)
	if fading {
		c.AnimationFrame()
	}

	ui.Overlay(c, func() {
		// Escape 跳过（在 View 里处理）：把时钟拨到淡出起点，前面各阶段
		// 全部落终态。
		ui.Box(c).Absolute().Left(0).Top(0).Fill().Background(colors.bg).
			Opacity(1 - fk).Label("启动画面")

		col := ui.Column(c).Absolute().Left(0).Top(0).Fill().AlignItems(ui.Center).Opacity(1 - fk)
		col.Children(func() {
			ui.Spacer(c)
			a.splashMark(c, now, colors)
			a.splashWordmark(c, now, colors)
			ui.Spacer(c)
			a.splashCredit(c, now, colors)
		})
	})
}

// splashMark 是图标部分：无星 Logo 先淡入（带上飘），随后星星以缩放
// 亮起。星星用 Painter 直接画——单色素材，便于按进度缩放逐帧对位。
func (a *App) splashMark(c *ui.Context, now time.Time, colors splashPalette) {
	lk, lmoving := (motion{start: a.splashAt, delay: splashLogoDelay, dur: splashLogoDur, ease: easePage}).at(now)
	sk, smoving := (motion{start: a.splashAt, delay: splashStarDelay, dur: splashStarDur, ease: ui.EaseOut}).at(now)
	if lmoving || smoving {
		c.AnimationFrame()
	}

	stack := ui.Box(c).Size(splashLogoSize, splashLogoSize).Opacity(lk)
	if lk < 1 {
		stack.Top(14 * (1 - lk))
	}
	stack.Children(func() {
		ui.Image(c, colors.logo).Width(splashLogoSize).Height(splashLogoSize)
		ui.Box(c).Absolute().Left(0).Top(0).Size(splashLogoSize, splashLogoSize).
			Draw(func(p *ui.Painter, r ui.Rect) {
				if sk <= 0 {
					return
				}
				s := splashLogoSize * (0.72 + 0.28*sk)
				d := (splashLogoSize - s) / 2
				p.Icon(splashStar, ui.Rect{X: r.X + d, Y: r.Y + d, W: s, H: s},
					colors.accent.Alpha(sk))
			})
	})
}

// splashWordmark 是 “Quaver Astra” 字标：先 “Quaver” 从左侧右移滑入
// 占位，随后手写体 “Astra” 在其右侧浮现。轨道宽度一开始就把两个词都
// 算上，整体始终在窗口水平居中。
func (a *App) splashWordmark(c *ui.Context, now time.Time, colors splashPalette) {
	qk, qmoving := (motion{start: a.splashAt, delay: splashQuaverDelay, dur: splashQuaverDur, ease: easePage}).at(now)
	ak, amoving := (motion{start: a.splashAt, delay: splashAstraDelay, dur: splashAstraDur, ease: ui.EaseOut}).at(now)
	if qmoving || amoving {
		c.AnimationFrame()
	}

	qws, qhs := splashQuaver.Size()
	qw := fz(splashQuaverHeight) * qws / qhs
	aws, ahs := splashAstra.Size()
	aw := fz(splashAstraHeight) * aws / ahs

	track := ui.Box(c).Width(qw + fz(splashWordGap) + aw).Height(fz(58))
	track.Children(func() {
		ui.Icon(c, splashQuaver).FontSize(fz(splashQuaverHeight)).TextColor(colors.ink).
			Absolute().Top(fz(10)).Left(-splashSlide * (1 - qk)).Opacity(qk).Label("Quaver")
		ui.Icon(c, splashAstra).FontSize(fz(splashAstraHeight * (0.9 + 0.1*ak))).TextColor(colors.accent).
			Absolute().Top(0).Left(qw + fz(splashWordGap)).Opacity(ak).Label("Astra")
	})
}

// splashCredit 是底部的 “Built by Team Quaver”。
func (a *App) splashCredit(c *ui.Context, now time.Time, colors splashPalette) {
	ck, cmoving := (motion{start: a.splashAt, delay: splashCreditDelay, dur: splashCreditDur, ease: easePage}).at(now)
	if cmoving {
		c.AnimationFrame()
	}
	ui.Text(c, "Built by Team Quaver").FontSize(fz(13)).TextColor(colors.credits).
		Padding(0, 0, 30, 0).Opacity(ck)
}
