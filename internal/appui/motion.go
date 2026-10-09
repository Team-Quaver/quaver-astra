package appui

import (
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// 动效语言：逐条对齐主项目（桌面版 Quaver）ui/src/style.css 的口径。
//
//	.route > .entering > *   opacity 0→1 + translateY(8px)，.26s
//	                         cubic-bezier(.22,.61,.36,1)，块间延迟 .04s 递增
//	.np                      上滑开合，.34s cubic-bezier(.32,.72,.24,1)
//	.nav a / .side-btn / .row  transition: background .14s
//	.sidebar                 width .24s cubic-bezier(.22,.61,.36,1)
//	.pb-fill                 进度填充跟着位置缓动
//
// MyGo 里能走第一等 API 的都走第一等的：ElementTransition 管开合与位移，
// Element.Animate 管「目标值变化」的缓动。剩下两件事得自己按帧时钟算：
//
//   - 错峰入场：Animate 不支持延迟，而且每个元素各自起算，做不出「依次到达」
//   - 按事件重置的淡入：Animate 的值「从第一个目标开始」，没有「从 0 淡入」
//
// 自己算的那部分统一约定：**start 为零值时进度取 1**。于是首帧、以及任何
// 没跑过动画的渲染环境（测试）拿到的都是终态，不会把内容藏在透明里。

// 主项目的两条缓动曲线（都是 cubic-bezier）。
var (
	// easePage 对应 route-in / 侧栏宽度：起步快、收尾长，界面出现得利落。
	easePage = bezier(0.22, 0.61, 0.36, 1)
	// easePanel 对应正在播放页的开合：更强调尾巴，像面板「弹」到位。
	easePanel = bezier(0.32, 0.72, 0.24, 1)
)

// hoverFade 是悬停/选中底色的过渡（主项目 transition: background .14s）。
var hoverFade = ui.ElementTransition{Colors: true, Duration: 140 * time.Millisecond}

// growFade 给「会异步进出、并且有悬停底色」的列表项用：进出时撑开/收拢
// （主项目 Enter/Exit Motion{Collapse}），底色变化仍走颜色过渡。
var growFade = ui.ElementTransition{
	Duration: 200 * time.Millisecond,
	Ease:     easePage,
	Colors:   true,
	Enter:    &ui.Motion{Collapse: true},
	Exit:     &ui.Motion{Collapse: true},
}

// bezier 返回 CSS cubic-bezier(x1,y1,x2,y2) 对应的缓动函数。
//
// CSS 的 cubic-bezier 是「以 x（时间进度）为自变量」的曲线，不能拿参数 t
// 直接当进度用——必须先按 x 反解出参数 t，再取 y。x1/x2 落在 [0,1] 时曲线
// 单调，牛顿迭代几次就够；不收敛时退回二分，保证任何输入都有结果。
func bezier(x1, y1, x2, y2 float32) ui.Easing {
	ax, bx, cx := bezierCoeffs(x1, x2)
	ay, by, cy := bezierCoeffs(y1, y2)
	sampleX := func(t float32) float32 { return ((ax*t+bx)*t + cx) * t }
	sampleY := func(t float32) float32 { return ((ay*t+by)*t + cy) * t }
	slopeX := func(t float32) float32 { return (3*ax*t+2*bx)*t + cx }

	return func(x float32) float32 {
		switch {
		case x <= 0:
			return 0
		case x >= 1:
			return 1
		}
		t := x
		converged := false
		for i := 0; i < 8; i++ {
			f := sampleX(t) - x
			if f < 1e-5 && f > -1e-5 {
				converged = true
				break
			}
			d := slopeX(t)
			if d < 1e-4 && d > -1e-4 {
				break
			}
			t = clampF32(t - f/d)
		}
		if !converged {
			// 牛顿迭代遇到平段会停在中途：二分把 t 收紧到 ±1e-4。
			lo, hi := float32(0), float32(1)
			for i := 0; i < 20; i++ {
				m := (lo + hi) / 2
				if sampleX(m) < x {
					lo = m
				} else {
					hi = m
				}
			}
			t = (lo + hi) / 2
		}
		return sampleY(t)
	}
}

// bezierCoeffs 把控制点的 x（或 y）分量换成标准三次多项式系数。
func bezierCoeffs(p1, p2 float32) (a, b, c float32) {
	c = 3 * p1
	b = 3*(p2-p1) - c
	a = 1 - c - b
	return a, b, c
}

// motion 是一段按帧时钟推进的动画。
type motion struct {
	start time.Time
	delay time.Duration
	dur   time.Duration
	ease  ui.Easing
}

// at 返回缓动后的进度（0..1）与「是否还在动」。start 为零值时恒为 1
// （见文件头约定）。ease 为零值时按线性处理。
func (m motion) at(now time.Time) (float32, bool) {
	if m.start.IsZero() {
		return 1, false
	}
	ease := m.ease
	if ease == nil {
		ease = ui.EaseOut
	}
	el := now.Sub(m.start) - m.delay
	switch {
	case el <= 0:
		return 0, true
	case el >= m.dur:
		return 1, false
	}
	return ease(float32(el) / float32(m.dur)), true
}

// styleEnter 把元素摆成「从下方 dy 处淡入」的入场姿态。动画结束后不再碰
// 元素——不留残留的透明度与位移。
func styleEnter(c *ui.Context, m motion, e *ui.Element, dy float32) {
	k, moving := m.at(c.Now())
	if moving {
		c.AnimationFrame()
	}
	if k >= 1 {
		return
	}
	e.Opacity(k)
	if dy != 0 {
		e.Top(dy * (1 - k))
	}
}

// styleFade 按进度把透明度从 from 送到 to（给不该位移的装饰层用）。
func styleFade(c *ui.Context, m motion, e *ui.Element, from, to float32) {
	k, moving := m.at(c.Now())
	if moving {
		c.AnimationFrame()
	}
	switch {
	case k >= 1:
		e.Opacity(to)
	case k <= 0:
		e.Opacity(from)
	default:
		e.Opacity(from + (to-from)*k)
	}
}

// enterState 是路由页的错峰入场时钟。
type enterState struct {
	seen bool
	path string
	base time.Time // 零值 = 不播入场（首帧/启动）
}

// routeEnter 维护 {路径 → 入场时钟}：只有真正导航（路径变化）才起算，
// 启动首帧直接落终态，免得开机画面从空白淡进来。
func (a *App) routeEnter(c *ui.Context) *enterState {
	switch p := a.Router.Path(); {
	case !a.enter.seen:
		a.enter.seen, a.enter.path = true, p
	case a.enter.path != p:
		a.enter.path, a.enter.base = p, c.Now()
	}
	return &a.enter
}

// enterBlock 让路由页里的第 i 个区块按主项目 route-in 的节奏入场。
// i 是区块在页面内的序号（0 起），块间 40ms 递增。
func (a *App) enterBlock(c *ui.Context, i int, e *ui.Element) {
	st := a.routeEnter(c)
	styleEnter(c, motion{
		start: st.base,
		delay: time.Duration(i) * 40 * time.Millisecond,
		dur:   260 * time.Millisecond,
		ease:  easePage,
	}, e, 8)
}

// pageEnter 给路由页的最外层容器挂上统一的入场（route-in 的单块版）：
// 整页由下 8px 淡入。首页那种分块错峰的页面自己再按序号调 enterBlock。
func (a *App) pageEnter(c *ui.Context, e *ui.Element) *ui.Element {
	a.enterBlock(c, 0, e)
	return e
}

// ===== 播放位置的视觉平滑 =====

// posSmoother 把低频采样（tick 200ms 一次）的播放位置外推成每帧连续的
// 估计值，供逐字高亮插值使用。
//
// 播放位置的唯一真相源仍然是 mpv 的 time-pos——换曲、暂停、跳转都会立刻
// 重新锚定；外推只作用在「两次采样之间」，误差被下一次采样抹掉，既不累积
// 也不回写给进度条或 seek。
type posSmoother struct {
	key string
	pos float64
	at  time.Time
}

// smooth 返回 now 时刻的位置估计。key 变化（换曲）或暂停时重新锚定。
func (s *posSmoother) smooth(now time.Time, pos float64, playing bool, key string) float64 {
	if key != s.key || !playing {
		s.key, s.pos, s.at = key, pos, now
		return pos
	}
	if pos != s.pos {
		// 新采样到位：重锚（位置回跳也走这里，seek 不会被误当成正常推进）。
		s.pos, s.at = pos, now
		return pos
	}
	el := now.Sub(s.at).Seconds()
	switch {
	case el <= 0:
		return s.pos
	case el > 1:
		// 采样长时间没来（卡住/缓冲）就别再往前推。
		return s.pos + 1
	}
	return s.pos + el
}

// lyricPos 是逐字高亮用的连续播放位置。
func (a *App) lyricPos(c *ui.Context) float64 {
	key := ""
	if cur, ok := a.PL.Current(); ok {
		key = cur.Mid
	}
	return a.pos.smooth(c.Now(), a.PL.Position(), a.PL.Playing(), key)
}

// ===== 逐字（QRC 卡拉OK）配色 =====

// 逐字配色：未唱的偏暗白，正在唱的词在两个色之间插值，唱完为纯白。
//
// 刻意不用封面强调色：亮暖封面下强调色掺白后与背景落进同一亮度区间，对比度
// 会掉到危险区（主项目同样踩过，最后也定成白色）。整行白字叠在左缘加深的
// 遮罩上，最坏情况也够读。
var (
	karaDim = ui.RGBA(255, 255, 255, 0.4)
	karaLit = ui.Hex("#ffffff")
)

func karaokeColor(fill float32) ui.Color {
	switch {
	case fill >= 1:
		return karaLit
	case fill <= 0:
		return karaDim
	}
	return karaDim.Mix(karaLit, fill)
}

// karaokeLine 画当前句的逐字高亮。
//
// 走 Painter 而不是文本元素：MyGo 的文本元素只能整块着色，逐字高亮却要求
// 同一行里每个词各自一个颜色。这里用 RichText 的 span 做，一个词一个 span。
//
// 宽度必须由视图算好传进来（npLyricWidth），量高度和画用的是同一个宽度，
// 换行才会一致；Box 没有子元素就没有内在高度，高度也得这么量出来。
//
// 帧驱动：正在唱的这句每帧都要重算，所以叫的是 view 层的 c.AnimationFrame()
// ——p.AnimationFrame() 只重绘不重跑视图，span 颜色会冻在上一帧。
func (a *App) karaokeLine(c *ui.Context, ql player.QrcLine, size float32, width float32) {
	pos := a.lyricPos(c)
	// 只在「正在播且这句还没唱完」时逐帧推进：暂停时位置不再变化，再叫帧
	// 就是白烧 CPU（窗口会一直以为自己有东西在动）。
	if a.PL.Playing() && pos < ql.End() {
		c.AnimationFrame()
	}
	fills := ql.WordFills(pos, a.karaBuf[:0])
	a.karaBuf = fills

	spans := a.karaSpans[:0]
	for i, w := range ql.Words {
		if w.Text == "" {
			continue
		}
		spans = append(spans, ui.Span{
			Text:   w.Text,
			Font:   a.lyricFontFamily(),
			Size:   fz(size),
			Weight: 800,
			Color:  karaokeColor(fills[i]),
		})
	}
	a.karaSpans = spans
	if len(spans) == 0 {
		return
	}
	_, h := c.MeasureText(width, spans...)

	// 逐字行是纯绘制，没有文本元素了——读屏与测试就靠 Label 认出这一行，
	// 别让逐字高亮把歌词从无障碍树里抹掉。
	ui.Box(c).FillWidth().Height(h).Role(ui.RoleText).Label(ql.Text).
		Draw(func(p *ui.Painter, r ui.Rect) {
			p.RichText(r.X, r.Y, width, spans...)
		})
}
