package appui

import (
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// 歌单 / 我喜欢这类歌曲列表右下角的两个悬浮动作：定位到正在播放的
// 那一行，以及回到列表顶部。按钮浮在内容之上，不占列表的布局空间。
const (
	listActionSize   = 38
	listActionRight  = 22
	listActionBottom = 22
	listActionGap    = 10

	// songRowHeight 与 songRow 的行高同一口径：虚拟列表按固定行高估算
	// 目标位置，改一处必须改另一处。
	songRowHeight = 54
	// listScrollDur 是定位/回顶的缓动时长，跟页面入场同一节奏。
	listScrollDur = 320 * time.Millisecond
)

// listScrollAnim 是一场由应用层驱动的列表滚动。ListState.ScrollTo 只能按行
// 瞬跳；这里改走 ScrollToOffset 的像素偏移，由 advanceSongListScroll 每帧
// 推进，虚拟列表也能像页面 Scroll 一样平滑。
type listScrollAnim struct {
	active   bool
	from, to float64
	start    time.Time
}

// songRowIndex 返回当前播放曲目在 songs 里的下标；不在列表里返回 -1。
// 和 songRow 的高亮口径一致，只认 Mid：没有 Mid 的条目（例如尚未落库的
// 本地占位）本来也无法稳定定位。
func (a *App) songRowIndex(songs []player.Song) int {
	cur, ok := a.PL.Current()
	if !ok || cur.Mid == "" {
		return -1
	}
	for i := range songs {
		if songs[i].Mid == cur.Mid {
			return i
		}
	}
	return -1
}

// scrollSongListToRow 平滑滚到第 row 行并把它居中；行高固定，目标偏移可以
// 直接算出来，不需要先瞬跳再回头校准。
func (a *App) scrollSongListToRow(c *ui.Context, st *songListState, row int) {
	if row < 0 {
		return
	}
	h := float64(st.viewport)
	if h <= 0 {
		// 首帧还没量到视口：退回瞬跳，至少不会不动。
		st.list.ScrollTo(row, ui.Center)
		return
	}
	maxY := float64(len(st.songs))*songRowHeight - h
	target := float64(row)*songRowHeight - (h-songRowHeight)/2
	a.scrollSongListTo(c, st, max(0, min(target, maxY)))
}

// scrollSongListToTop 平滑回到列表顶部。
func (a *App) scrollSongListToTop(c *ui.Context, st *songListState) {
	a.scrollSongListTo(c, st, 0)
}

func (a *App) scrollSongListTo(c *ui.Context, st *songListState, y float64) {
	if c.Preferences().ReduceMotion {
		st.list.ScrollToOffset(max(0, y))
		st.scrollAnim = listScrollAnim{}
		return
	}
	st.scrollAnim = listScrollAnim{
		active: true,
		from:   st.list.Offset(),
		to:     max(0, y),
		start:  c.Now(),
	}
	c.AnimationFrame()
}

// advanceSongListScroll 在每帧建列表前把偏移推到缓动曲线的当前位置。它必须
// 在 ui.List 之前调用：偏移请求要在本帧布局时被列表消费。
func (a *App) advanceSongListScroll(c *ui.Context, st *songListState) {
	an := &st.scrollAnim
	if !an.active {
		return
	}
	k := float32(c.Now().Sub(an.start)) / float32(listScrollDur)
	if k >= 1 {
		st.list.ScrollToOffset(an.to)
		an.active = false
		return
	}
	e := easePage(k)
	st.list.ScrollToOffset(an.from + (an.to-an.from)*float64(e))
	c.AnimationFrame()
}

// songListActions 在页面右下角铺两个圆钮。locateIndex < 0 时只留「回到顶部」，
// atTop 为真时只留定位钮；两个都没必要时整组不建，免得空占一层命中区。
func (a *App) songListActions(c *ui.Context, locateIndex int, atTop bool, locate, top func()) {
	if locateIndex < 0 && atTop {
		return
	}
	ui.Column(c).Absolute().Right(listActionRight).Bottom(listActionBottom).
		Gap(listActionGap).AlignItems(ui.End).Children(func() {
		// 回到顶部在定位钮上方：它随滚动出现/消失时，定位钮的位置不动，
		// 不会出现「点完定位、按钮从指针下溜走」的错位。
		if locateIndex >= 0 {
			if !atTop {
				a.listActionButton(c, "toTop", "回到顶部", top)
			}
			a.listActionButton(c, "locate", "定位到正在播放", locate)
		} else if !atTop {
			a.listActionButton(c, "toTop", "回到顶部", top)
		}
	})
}

// listActionButton 是右下角的单枚玻璃圆钮。文字标签供 tooltip 与读屏使用，
// 视觉上只留图标，避免在列表内容上压出一块文字浮层。
func (a *App) listActionButton(c *ui.Context, icon, label string, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(listActionSize, listActionSize).Radius(listActionSize/2).
		Center().Border(1, t.Border.Alpha(0.8)).FocusRing(false).Transition(hoverFade)
	b.Label(label)
	b.Tooltip(label)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(17)).TextColor(t.Text).AlignSelf(ui.Center)
	})

	tint := t.Surface.Alpha(0.72)
	if b.Hovered() {
		tint = t.SurfaceHover.Alpha(0.92)
		b.Border(1, t.Border)
	}
	tint = b.AnimateColor("song-list-action-"+icon, tint, ui.Animation{Duration: hoverFade.Duration})
	b.Material(navFrost{blur: 12, tint: tint})
	b.Shadow(0, 8, 22, 0, ui.RGBA(0, 0, 0, 0.22))

	if b.Clicked() {
		onClick()
	}
}
