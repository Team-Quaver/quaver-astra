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

// lyricFollowPause 是用户滚过歌词列表后暂停自动跟随的时长。滚轮/拖动
// 滚动条时列表必须停在用户放下的地方，否则换行会把正在找的那一句拽走；
// 停手 3 秒后再缓动回当前句。每次滚动都会续期，所以计的是「停手后」。
const lyricFollowPause = 3 * time.Second

// lyricFollowPaused 报告此刻是否正处于用户滚动的静默期。
func (a *App) lyricFollowPaused(now time.Time) bool {
	return !a.npFollowPause.IsZero() && now.Before(a.npFollowPause)
}

// pauseLyricFollow 记下用户刚滚过列表：停掉还在路上的跟随动画，并把恢复
// 时间推到 now+lyricFollowPause。偏移保持用户滚到的位置，不在这里改。
func (a *App) pauseLyricFollow(now time.Time) {
	a.npFollow = lyricFollow{}
	a.npFollowPause = now.Add(lyricFollowPause)
}

// advanceLyricFollow 每帧建视图前推进跟随动画，并记下本趟 build 开始时的
// 偏移（npPassOff）：行与列表的 LayoutBounds 都是上一次布局的结果，而那次
// 布局正是按这个偏移摆的。
//
// 一帧里视图可能建不止一趟，中间还可能重新布局：同一帧里行位置会变一次，
// 而偏移每趟都在推进。所以基准偏移只在行几何变化时更新——用同一趟的
// 偏移硬配上一趟的几何，目标就会被反复修正、越修越偏，换行时来回抽。
//
// 帧与帧之间偏移若变了，可能是用户滚了列表：滚轮和拖动滚动条都发生在
// 建视图之前，而列表布局对偏移的修正发生在建视图之后。于是拿它当「用户
// 滚动」的信号，暂停跟随；自己发起的 ScrollTo（跳太远时的瞬间居中）会把
// npScrollAbsorb 置上，下一帧不计。
//
// 跟随动画自己推动偏移时不能这样判断：布局会把写进去的值再校正一点，
// 差值会被误认成用户滚动，动画刚起步就被取消——表现就是当前句越走越
// 往下、却不回正中。所以 pending/active 或上一帧刚写过偏移时一律不识别。
func (a *App) advanceLyricFollow(c *ui.Context) {
	now := c.Now()
	f := &a.npFollow
	own := a.npWrote || f.pending || f.active
	a.npWrote = false
	switch {
	case a.npScrollAbsorb:
		// 自己发起的 ScrollTo 在上一帧布局时才落位：第一趟新帧吸收它，
		// 同一帧后面的几趟不吸收（那时偏移还没被布局改）。
		if now.After(a.npScrollAbsorbAt) {
			a.npScrollAbsorb = false
		}
	case !own && a.npScrollSeenOK:
		if d := a.npScroll.Y - a.npScrollSeen; d > 0.5 || d < -0.5 {
			a.pauseLyricFollow(now)
		}
	}
	a.npPassOff = a.npScroll.Y

	// 静默期结束：让本次构建重新跟随当前句。期间 lastLyricLine 照常
	// 更新，所以换行本身不触发动画；这里清成哨兵才会强制跟一次。
	//
	// 静默期里画面可能完全静止（暂停播放、没有输入）：到期时得醒一次，
	// 才能把歌词模糊恢复、把跟随接回当前句。同一个截止时间只排一次。
	if !a.npFollowPause.IsZero() {
		switch {
		case !now.Before(a.npFollowPause):
			a.npFollowPause = time.Time{}
			a.npPauseWake = time.Time{}
			a.lastLyricLine = -2
			a.npResume = true
		case a.npPauseWake != a.npFollowPause:
			a.npPauseWake = a.npFollowPause
			c.After(time.Until(a.npFollowPause))
		}
	}

	if !f.active {
		a.npScrollSeen, a.npScrollSeenOK = a.npScroll.Y, true
		return
	}
	m := motion{start: f.start, dur: lyricFollowDur, ease: easePage}
	k, moving := m.at(now)
	a.npScroll.Y = f.from + (f.to-f.from)*k
	a.npScrollSeen, a.npScrollSeenOK = a.npScroll.Y, true
	a.npWrote = true
	if moving {
		c.AnimationFrame()
		return
	}
	f.active = false
}

// resetLyricFollowOnSongChange 换曲时把跟随状态清零：上一首的偏移、
// 还在路上的动画和当前行号都不能带进新歌。新歌从列表顶部开始，第一句
// 出来时它已经在附近，跟随只需走一小段——否则第一句会把列表从上一首
// 的深处猛地拽回来，看着就是抽搐。
func (a *App) resetLyricFollowOnSongChange(key string) {
	if key == a.npSongKey {
		return
	}
	a.npSongKey = key
	a.npFollow = lyricFollow{}
	a.npFollowPause = time.Time{}
	a.npPauseWake = time.Time{}
	a.lastLyricLine = -2
	a.npShown, a.npScroll.Y = 0, 0
	a.npScrollSeen, a.npScrollSeenOK = 0, true
	a.npScrollAbsorb, a.npWrote = false, false
	a.npResume, a.npSnapAt = false, time.Time{}
	a.npRowLine, a.npRowRect = -1, ui.Rect{}
	a.npList.ScrollTo(0, ui.Start)
}

// followLyricLine 记下「当前句变了」，目标偏移等这一行建出来再算。
func (a *App) followLyricLine(line int) {
	a.npFollow = lyricFollow{pending: true, line: line}
}

// followLyricRow 在构建当前句时调用：用最近一次布局的行/列表位置把它滚到中心。
// 目标偏移 = 那份几何对应的偏移 + (行中心 − 列表中心)，与主项目
// followCurrent 同式。
//
// 每帧都重算：当前句被强调后行高会变，目标要跟着修正。重算时用的偏移必须
// 是行几何布局时的那个（见 advanceLyricFollow），否则同一份几何会被不断
// 当成别的偏移去换算，目标来回跳，歌词就抽。
func (a *App) followLyricRow(c *ui.Context, row ui.Element, line int) {
	f := &a.npFollow
	if f.line != line || !(f.pending || f.active) {
		return
	}
	a.npResume = false // 当前句建出来了：走正常缓动，不需要淡入兜底
	rr, lr := row.LayoutBounds(), a.npListRect
	if rr.H <= 0 || lr.H <= 0 {
		return // 这一行上一帧还不在视图里，交给瞬间跳转兜底
	}
	// 行几何换了，说明中间又布局过：这一份几何对应的偏移就是本趟开始时
	// 的那个。几何没变时沿用上次记下的偏移，它们仍然是同一份。
	if line != a.npRowLine || rr != a.npRowRect {
		a.npRowLine, a.npRowRect = line, rr
		a.npShown = a.npPassOff
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
