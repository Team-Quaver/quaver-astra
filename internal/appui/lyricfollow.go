package appui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// lyricFollow 是歌词列表的换行跟随：当前句变了，列表从原位置缓动到把新句
// 放在可视区中心，而不是 ListState.ScrollTo 那样一步跳过去。
//
// 主项目的行级歌词用的是 `lyrics.scrollTo({ top, behavior: "smooth" })`，
// 这里做同一件事，只是偏移由 App 自己按帧推进（npScroll.Y），这样目标可
// 以用上一帧量到的行/列表位置现算，不必依赖列表内部的锚点状态。
type lyricFollow struct {
	// pending 表示新当前句已经确定，但还没在列表里建出来量到位置。
	// 行建出来时调 followLyricRow 才真正起步；一直没建出来（跳转很远）
	// 就在列表构建完后退回瞬间居中。
	pending bool
	active  bool
	line    int
	from    float32
	to      float32
	start   time.Time
}

// lyricFollowDur 比行高亮的 260ms 略长：位移走完时下一句的强调也刚好落定。
const lyricFollowDur = 320 * time.Millisecond

// advanceLyricFollow 每帧建视图前推进跟随动画。npShown 必须在这之前记下：
// 行与列表的 LayoutBounds 都是上一帧布局的结果，只有配上上一帧的偏移，
// 目标位置才算得准。
func (a *App) advanceLyricFollow(c *ui.Context) {
	a.npShown = a.npScroll.Y
	f := &a.npFollow
	if !f.active {
		return
	}
	m := motion{start: f.start, dur: lyricFollowDur, ease: easePage}
	k, moving := m.at(c.Now())
	a.npScroll.Y = f.from + (f.to-f.from)*k
	if moving {
		c.AnimationFrame()
		return
	}
	f.active = false
}

// followLyricLine 记下「当前句变了」，目标偏移等这一行建出来再算。
func (a *App) followLyricLine(line int) {
	a.npFollow = lyricFollow{pending: true, line: line}
}

// followLyricRow 在构建当前句时调用：用上一帧的行/列表位置把它滚到中心。
// 目标偏移 = 上一帧偏移 + (行中心 − 列表中心)，与主项目 followCurrent 同式。
//
// 每帧都重算一次：当前句被强调后行高会变，目标要跟着修正；而滚动本身让
// 行中心移动，重算结果会收敛到同一个位置，不会来回摆。
func (a *App) followLyricRow(c *ui.Context, row ui.Element, line int) {
	f := &a.npFollow
	if f.line != line || !(f.pending || f.active) {
		return
	}
	rr, lr := row.LayoutBounds(), a.npListRect
	if rr.H <= 0 || lr.H <= 0 {
		return // 这一行上一帧还不在视图里，交给瞬间跳转兜底
	}
	to := a.npShown + (rr.Y + rr.H/2) - (lr.Y + lr.H/2)
	if f.pending {
		f.pending = false
		f.active = true
		f.from = a.npScroll.Y
		f.to = to
		f.start = c.Now()
		return
	}
	f.to = to
}
