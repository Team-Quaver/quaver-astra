package appui

import (
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// 首页右侧快捷跳转：一份悬浮在内容之上的区块目录。
//
// 它只在指针靠近右侧中部时展开，离开后自动收回，避免与首页的封面和卡片
// 抢注意力。跳转交给滚动容器平滑滚到对应区块；无限电台一项回到整页顶部，
// 不会打断正在播放的电台。
const (
	homeNavWidth  = 148
	homeNavEdge   = 16
	homeNavDelay  = 420 * time.Millisecond
	homeNavFade   = 180 * time.Millisecond
	homeGripWidth = 26
	homeGripH     = 96
	homeNavBlur   = 10
)

// homeNavItem 是快捷跳转的一项：显示名 + 目标区块。
type homeNavItem struct {
	label  string
	anchor ui.Element
	// top 表示这一项跳回页面顶部，而不是把某个区块顶到顶部。
	top bool
}

// navFrost 是按钮自己的磨砂背景：先把按钮下方的页面内容糊掉，再罩一层
// 随状态变化的主题色。底色和边框仍在同一层里画，文字不会被一起糊掉。
type navFrost struct {
	blur float32
	tint ui.Color
}

func (f navFrost) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	glass.Blur{Radius: f.blur}.PaintMaterial(p, box, radii)
	p.FillRadii(box, radii, f.tint)
}

// homeQuickNav 构建右侧快捷跳转。navShown 由前几帧的指针位置维护：
// 收起时只留一条很窄的把手，指针碰到把手或已经展开的目录时展开。
func (a *App) homeQuickNav(c *ui.Context, items []homeNavItem, active int) {
	st := &a.home
	now := c.Now()
	t := c.Theme()
	wasShown := st.navShown

	// 把手先建，展开后的目录在它上面：两者右侧有几像素重叠，
	// 不能让一层隐形按钮挡住目录最右端的点击。
	grip := ui.ButtonBase(c).Absolute().TopPercent(50).Right(4).
		TranslatePercent(0, -50).Size(homeGripWidth, homeGripH).
		Radius(homeGripWidth / 2).Center().FocusRing(false).Label("快捷跳转")
	if grip.Hovered() {
		grip.Background(t.SurfaceHover.Alpha(0.55))
	}
	grip.Children(func() {
		ui.Box(c).Width(3).Height(30).Radius(2).Background(t.Accent.Alpha(0.6))
	})
	gripHovered := grip.Hovered()
	if grip.Clicked() {
		st.navShown = true
		st.navHideAt = now.Add(2 * time.Second)
	}

	layer := ui.Column(c).Absolute().TopPercent(50).Right(homeNavEdge).
		TranslatePercent(0, -50).Width(homeNavWidth).Gap(7).
		AlignItems(ui.End).IgnoreInput(!wasShown)
	layer.Children(func() {
		for i, it := range items {
			a.homeNavButton(c, t, it, i, i == active)
		}
	})

	// Hovered 读的是上一帧的命中链：展开时目录本身负责接住指针，
	// 收起时 IgnoreInput 会把它从命中链里摘掉，只留下把手。
	hovered := wasShown && layer.Hovered()

	switch {
	case gripHovered || hovered:
		st.navShown = true
		st.navHideAt = now.Add(homeNavDelay)
	case st.navShown && !now.Before(st.navHideAt):
		st.navShown = false
	case st.navShown:
		// After 是逐帧排定的：淡出动画还在跑时如果不再续排，下一帧
		// 就会把已经装好的定时器清掉，鼠标离开后反而不会收起。
		c.After(st.navHideAt.Sub(now))
	}

	vis := float32(0)
	if st.navShown {
		vis = 1
	}
	k := layer.Animate("home-quick-nav", vis, homeNavFade)
	layer.Opacity(k).Translate((1-k)*10, 0).
		IgnoreInput(!st.navShown).AccessHidden(!st.navShown)

	// 收起状态留一根很淡的竖线作为入口；展开后跟着目录一起淡出。
	grip.Opacity(1 - k)
}

// homeNavButton 是目录里的一项。激活项用强调色和更长的一道线标记，
// 与首页区块标题的短横线保持同一套语言。
func (a *App) homeNavButton(c *ui.Context, t *ui.Theme, it homeNavItem, index int, active bool) {
	lineW := float32(12)
	lineCol := t.TextMuted.Alpha(0.6)
	ink := t.Text
	if active {
		lineW = 20
		lineCol = t.Accent
		ink = t.Accent
	}

	b := ui.ButtonBase(c).Height(32).Padding(0, 12).Radius(8).Center().
		Border(1, t.Border.Alpha(0.75)).Transition(hoverFade)
	b.Label("跳转到" + it.label)
	b.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, it.label).FontSize(fz(12.5)).FontWeight(700).TextColor(ink)
			ui.Box(c).Width(lineW).Height(2).Radius(1).Background(lineCol)
		})
	})

	tint := t.Surface.Alpha(0.62)
	switch {
	case active:
		tint = t.Accent.Alpha(0.24)
		b.Border(1, t.Accent.Alpha(0.35))
	case b.Hovered():
		tint = t.SurfaceHover.Alpha(0.78)
		b.Border(1, t.Border)
	}
	tint = b.AnimateColor("home-nav-fill", tint, ui.Animation{Duration: hoverFade.Duration})
	b.Material(navFrost{blur: homeNavBlur, tint: tint})
	b.Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.16))

	if b.Clicked() {
		a.home.navActive = index
		if it.top {
			// 无限电台是首页第一块内容，点它看的是整页开头（问候和日期），
			// 不是把电台卡片顶到最上面。
			a.home.scroll.ScrollTo(0, 0, ui.Smooth)
		} else if it.anchor.Valid() {
			it.anchor.ScrollIntoView(ui.ScrollIntoViewOptions{
				Block:     ui.ScrollStart,
				Animation: &ui.Smooth,
			})
		}
	}
}

// homeActiveSection 按当前滚动位置选出目录里应高亮的区块。
// 同一行的两个入口（无限电台 / 新歌速递）起点相同，保留用户刚点过的那个。
func (a *App) homeActiveSection(items []homeNavItem) int {
	st := &a.home
	active := st.navActive
	if active < 0 || active >= len(items) {
		active = 0
	}
	// 点击后的平滑滚动还没落位：保持用户刚点的项，否则滚动途中
	// 高亮会先退回第一项再一路追过去，看起来像点错了。
	if st.scroll.Scrolling() {
		return active
	}
	threshold := float32(topBarH + 128)
	best, bestY := -1, float32(-1e9)
	for i, it := range items {
		r := it.anchor.LayoutBounds()
		if r.W <= 0 && r.H <= 0 {
			continue
		}
		if r.Y <= threshold && r.Y > bestY {
			best, bestY = i, r.Y
		}
	}
	if best < 0 {
		return active
	}
	if cur := items[active].anchor.LayoutBounds(); cur.W > 0 || cur.H > 0 {
		if d := cur.Y - bestY; d > -1 && d < 1 {
			return active
		}
	}
	return best
}
