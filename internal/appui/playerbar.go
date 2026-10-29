package appui

import (
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// qualityShort 是档位短标签。
var qualityShort = map[string]string{
	"auto": "自动", "master": "Master", "atmos71": "全景7.1", "atmos51": "全景5.1",
	"atmos2": "全景声", "flac": "FLAC", "640ogg": "640K", "320ogg": "320K",
	"320": "320K", "128": "128K",
}

func qualityLabel(tier string, degraded bool) string {
	if tier == "" {
		tier = "auto"
	}
	s := qualityShort[tier]
	if s == "" {
		s = tier
	}
	if degraded {
		return "↓" + s
	}
	return s
}

// barH 是底部播放条的高度。正在播放页上的队列浮窗要用它算可用高度
// （浮窗与播放条不重叠）。
const barH = 84.0

// barHits 收集这一帧播放条里的交互控件。整条 bar 现在同时是进度条：
// 按下空白处开始 seek，之后指针越过任何按钮都仍由 bar 捕获；按下按钮
// 则把手势原样留给按钮。这样既恢复“整条控制条即进度条”，也不会重演
// Issue 1——拖过控件后丢掉 pressed、离不开 0:00。
type barHits struct {
	rects []ui.Rect
}

func (h *barHits) add(els ...ui.Element) {
	for _, el := range els {
		if el != (ui.Element{}) {
			h.rects = append(h.rects, el.Bounds())
		}
	}
}

func (h *barHits) over(x, y float32) bool {
	for _, r := range h.rects {
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return true
		}
	}
	return false
}

// playerBar 是底部播放条：整条背景就是进度填充，左边是正在播放的唱片，
// 中间是传输键与时间，右边是音质、音量与队列。布局借用桌面播放器的三段式
// 信息层级，但不使用 macOS 的交通灯、玻璃材质或浮动胶囊。
func (a *App) playerBar(c *ui.Context, t *ui.Theme) {
	cur, hasCur := a.PL.Current()

	// 进度比例（拖拽中用拖拽值预览）
	frac := float32(0)
	if hasCur && a.PL.Duration() > 0 {
		frac = float32(a.PL.Position() / a.PL.Duration())
	}
	if a.seekDragging {
		frac = a.seekFrac
	}

	// 播放条贴满窗口底部：左右顶到窗口边缘，上边紧接内容区/菜单栏。
	// 默认 Stretch 会把容器宽度完整交给条子，不额外留浮动卡片式外边距。
	bar := ui.Row(c).Height(barH).Padding(9, 16).Gap(18).
		AlignItems(ui.Center).Background(a.barBg(t)).Margin(0).Radius(0).
		Focusable().FocusRing(false).Role(ui.RoleSlider).Label("播放进度").
		Cursor(ui.CursorResizeEW)
	// 进度要缓动：位置采样只有 5Hz（player tick 200ms），直接用采样值画
	// 会一格一格跳。拖拽时不做缓动，要跟手。
	fill := frac
	if !a.seekDragging {
		fill = bar.Animate("seek-fill", frac, 260*time.Millisecond)
	}
	bar.Range(0, 1, float64(fill))
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		// 整条控制条的底色就是已播放部分：填充铺满高度，边界处加一条
		// 细信号线，既能一眼看出进度，也不需要在控件下面再藏一条轨道。
		if fill > 0.001 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * fill, H: r.H},
				a.glowNow.Alpha(0.28), 0)
			if fill < 0.999 {
				p.Fill(ui.Rect{X: r.X + r.W*fill - 1, Y: r.Y, W: 2, H: r.H},
					a.glowNow.Alpha(0.58), 0)
			}
		} else if a.PL.Duration() > 0 {
			// At 0:00 the fill is empty, so keep the playhead visible at
			// the very start as the "this whole bar is the progress bar" cue.
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: 2, H: r.H},
				a.glowNow.Alpha(0.58), 0)
		}
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, t.Border, 0)
		p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border.Alpha(0.55), 0)
	})

	var hits barHits
	bar.Children(func() {
		hits.add(a.barTrack(c, t, cur, hasCur)...)

		center := ui.Column(c).Width(440).Shrink(1).MinWidth(300).Gap(3).
			AlignItems(ui.Stretch)
		center.Children(func() {
			hits.add(a.barTransport(c, t)...)
			a.barTimes(c, t)
		})

		hits.add(a.barUtilities(c, t)...)
	})
	barRect := bar.Bounds()
	bar.HandleInput(func(ev ui.InputEvent) bool {
		return a.barSeekInput(ev, barRect, hits)
	})
	a.barSeekKeyboard(c, bar)
}

// barTrack is the left third: one click target for the cover and metadata,
// with the favourite control separated so liking a song never opens the
// now-playing view by accident.
func (a *App) barTrack(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) []ui.Element {
	var hits []ui.Element
	ui.Row(c).Grow(1).Basis(0).MinWidth(150).AlignItems(ui.Center).Gap(10).Children(func() {
		info := ui.ButtonBase(c).Grow(1).Basis(0).MinWidth(0).Gap(11).
			AlignItems(ui.Center).Transition(hoverFade).Label("正在播放")
		info.Children(func() {
			cover := ui.Box(c).Size(52, 52).Shrink(0).Radius(8).Clip().
				Background(t.SurfaceHover)
			cover.Children(func() {
				if art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 300), a.invalidate); art != nil {
					ui.Image(c, art).Fill().Fit(ui.Cover)
				} else {
					ui.Box(c).Fill().Center().Children(func() {
						ui.Icon(c, Icons["note"]).FontSize(fz(20)).
							TextColor(t.TextMuted).AlignSelf(ui.Center)
					})
				}
			})

			title := "Quaver Astra"
			sub := "双击歌曲开始播放"
			if a.PL.Loading() {
				title = "加载中…"
				sub = "正在准备音频"
			} else if e := a.PL.Error(); e != "" {
				title = "播放失败"
				sub = e
			} else if hasCur {
				title = cur.DisplayName()
				sub = cur.Artists
			}
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(fz(13.5)).FontWeight(700).
					SingleLine().Ellipsis("…")
				ui.Text(c, sub).FontSize(fz(11.5)).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…")
			})
		})
		if info.Hovered() {
			info.Background(t.SurfaceHover.Alpha(0.5))
		}
		if info.Clicked() && hasCur {
			a.npOpen = !a.npOpen
			// 正在播放页的浮层跟着页走：不然重新打开时旧菜单/浮窗还挂着。
			a.npMoreOpen = false
			a.npQInfoOpen = false
		}
		hits = append(hits, info)
		if hasCur {
			hits = append(hits, a.loveBtn(c, cur, hasCur, t))
		} else {
			ui.Box(c).Size(32, 32)
		}
	})
	return hits
}

// barTransport keeps the five transport controls together above the time.
// Radio only moves forward, so shuffle and repeat disappear there rather
// than sitting around as dead buttons.
func (a *App) barTransport(c *ui.Context, t *ui.Theme) []ui.Element {
	var hits []ui.Element
	ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(7).Children(func() {
		radio := a.PL.RadioActive()
		if !radio {
			hits = append(hits, a.shuffleBtn(c, t))
		}
		if radio {
			hits = append(hits, a.iconBtnDisabled(c, "prev", "无限电台只向前播放", 20))
		} else {
			hits = append(hits, a.iconBtn(c, "prev", "上一首"+a.hotkeyTip(hkPrev), 20, func() { a.PL.Prev() }))
		}
		hits = append(hits, a.playBtn(c, t))
		hits = append(hits, a.iconBtn(c, "next", "下一首"+a.hotkeyTip(hkNext), 20, func() { a.PL.Next(false) }))
		if !radio {
			hits = append(hits, a.modeBtn(c, t))
		}
	})
	return hits
}

func (a *App) barTimes(c *ui.Context, t *ui.Theme) {
	pos := a.PL.Position()
	if a.seekDragging {
		pos = float64(a.seekFrac) * a.PL.Duration()
	}
	ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(4).Children(func() {
		ui.Text(c, fmtTime(pos)).Font("monospace").FontSize(fz(10)).
			TextColor(t.TextMuted)
		ui.Text(c, "/").Font("monospace").FontSize(fz(10)).TextColor(t.TextMuted.Alpha(0.55))
		ui.Text(c, fmtTime(a.PL.Duration())).Font("monospace").FontSize(fz(10)).
			TextColor(t.TextMuted)
	})
}

// barSeekInput handles the whole bar as one slider. A press on a control
// is left for that control; a press on the bar itself takes the pointer,
// so the following moves keep arriving here even when they cross the
// cover, transport or utility buttons. That capture is the Issue 1 guard.
func (a *App) barSeekInput(ev ui.InputEvent, bar ui.Rect, hits barHits) bool {
	if ev.Button != 0 {
		return false
	}
	width := bar.W
	if width <= 0 {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		if a.PL.Duration() <= 0 {
			return false
		}
		if hits.over(bar.X+ev.X, bar.Y+ev.Y) {
			return false
		}
		a.seekDragging = true
		a.seekFrac = seekFracAt(ev.X, width)
		return true
	case ui.InputPointerMove:
		if !a.seekDragging {
			return false
		}
		a.seekFrac = seekFracAt(ev.X, width)
		return true
	case ui.InputPointerUp:
		if !a.seekDragging {
			return false
		}
		a.seekDragging = false
		a.seekFrac = seekFracAt(ev.X, width)
		if a.PL.Duration() > 0 {
			a.PL.SeekTo(float64(a.seekFrac))
		}
		return true
	}
	return false
}

func (a *App) barSeekKeyboard(c *ui.Context, bar ui.Element) {
	if !bar.Focused() {
		return
	}
	pos := a.PL.Position()
	dur := a.PL.Duration()
	switch {
	case c.Shortcut(0, ui.KeyHome):
		a.PL.SeekTo(0)
	case c.Shortcut(0, ui.KeyEnd):
		a.PL.SeekTo(1)
	case dur > 0 && c.Shortcut(0, ui.KeyLeft):
		a.PL.SeekTo(clampF64(pos/dur-0.01, 0, 1))
	case dur > 0 && c.Shortcut(0, ui.KeyRight):
		a.PL.SeekTo(clampF64(pos/dur+0.01, 0, 1))
	}
}

func (a *App) barUtilities(c *ui.Context, t *ui.Theme) []ui.Element {
	var hits []ui.Element
	ui.Row(c).Grow(1).Basis(0).MinWidth(140).Justify(ui.End).
		AlignItems(ui.Center).Gap(2).Children(func() {
		if a.PL.RadioActive() {
			ui.Row(c).Padding(3, 8).Radius(6).Border(1, t.Accent.Alpha(0.38)).
				Margin(0, 6, 0, 0).Children(func() {
				ui.Text(c, "∞ 无限电台").FontSize(fz(10.5)).FontWeight(700).TextColor(t.Accent)
			})
		}
		hits = append(hits, a.qualityPill(c, t))
		hits = append(hits, a.volumeCtl(c, t))
		if !a.PL.RadioActive() {
			hits = append(hits, a.queueBtn(c, t))
		}
	})
	return hits
}

func clampF32(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampF64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func seekFracAt(x, width float32) float32 {
	if width <= 0 {
		return 0
	}
	// 10 DIPs at each end snap to the exact endpoints. This is both easier
	// to hit and the regression guard for the old "cannot drag to 0" bug.
	if x <= 10 {
		return 0
	}
	if x >= width-10 {
		return 1
	}
	return clampF32(x / width)
}

func (a *App) playBtn(c *ui.Context, t *ui.Theme) ui.Element {
	btn := ui.ButtonBase(c).Size(40, 40).Radius(20).Center().Background(t.Text)
	// tooltip 里带上快捷键：播放条是纯图标按钮，用户得知道能不能用键盘控制。
	btn.Tooltip("播放/暂停" + a.hotkeyTip(hkPlayPause))
	btn.Label("播放/暂停")
	// 悬停压暗（主项目 .pb-play 是 transform: scale，MyGo 的元素没有缩放，
	// 用透明度做同一件事），走 Animate 让它是缓动的而不是硬切。
	o := float32(1)
	if btn.Hovered() {
		o = 0.85
	}
	btn.Opacity(btn.Animate("hover", o, 120*time.Millisecond))
	btn.Children(func() {
		if a.PL.Loading() {
			sp := ui.Spinner(c)
			sp.TextColor(t.Background).AlignSelf(ui.Center)
			return
		}
		name := "play"
		if a.PL.Playing() {
			name = "pause"
		}
		ui.Icon(c, Icons[name]).FontSize(fz(20)).TextColor(t.Background).AlignSelf(ui.Center)
	})
	if btn.Clicked() {
		a.PL.PlayOrPause()
	}
	return btn
}

// iconBtn 是播放条上的圆形图标按钮。label 用于无障碍 tooltip——
// 纯图标按钮没有文字说明，键盘/读屏用户无从得知其作用。
func (a *App) iconBtn(c *ui.Context, icon, label string, size float32, fn func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(size)).AlignSelf(ui.Center)
	})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if label != "" {
		b.Tooltip(label)
	}
	if b.Clicked() {
		fn()
	}
	return b
}

// iconBtnDisabled 是保持占位但不接受点击的图标按钮。电台的上一首用它，
// 避免布局在中段塌掉；禁用态压暗，tooltip 说明原因。
func (a *App) iconBtnDisabled(c *ui.Context, icon, label string, size float32) ui.Element {
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Opacity(0.34)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(size)).AlignSelf(ui.Center)
	})
	b.Disabled(true)
	if label != "" {
		b.Tooltip(label)
	}
	return b
}

// queueBtn 是播放条上的队列开关。面板开着时高亮：队列浮窗盖在正在播放页的
// 右侧，图标不带状态的话，「开着队列」这件事在整页浮层上就没有任何痕迹。
func (a *App) queueBtn(c *ui.Context, t *ui.Theme) ui.Element {
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	b.Children(func() {
		ic := ui.Icon(c, Icons["queue"]).FontSize(fz(18)).AlignSelf(ui.Center)
		if a.queueOpen {
			ic.TextColor(t.Accent)
		}
	})
	switch {
	case a.queueOpen:
		b.Background(t.Accent.Alpha(0.13))
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Tooltip("播放队列")
	b.Label("播放队列")
	if b.Clicked() {
		a.queueOpen = !a.queueOpen
		a.Conf.Set("Window.QueueOpen", a.queueOpen)
		if a.queueOpen {
			// 正在播放页的 ⋮ 菜单/音质浮窗和队列浮窗抢同一块右上角：
			// 开队列时先收掉，不然它们会被压在队列底下点不到。
			a.npMoreOpen = false
			a.npQInfoOpen = false
		}
	}
	return b
}

func (a *App) volumeCtl(c *ui.Context, t *ui.Theme) ui.Element {
	icon := "volHigh"
	v := a.PL.Volume()
	switch {
	case a.PL.Muted() || v <= 0:
		icon = "volMute"
	case v < 0.33:
		icon = "volLow"
	case v < 0.66:
		icon = "volMid"
	}
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	b.Children(func() { ui.Icon(c, Icons[icon]).FontSize(fz(17)).AlignSelf(ui.Center) })
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Tooltip("音量")
	b.Label("音量")
	if b.Clicked() {
		a.volOpen = !a.volOpen
	}
	ui.Popover(c, b, &a.volOpen, func() {
		panel := ui.Column(c).Width(220).Padding(12).Gap(10).
			Background(t.Surface).Radius(12).Border(1, t.Border).Shadow(0, 10, 32, 0, ui.RGBA(0, 0, 0, 0.18))
		panel.Children(func() {
			vol := a.PL.Volume()
			sl := ui.Slider(c, &vol, 0, 1).FillWidth()
			if sl.Changed() {
				a.PL.SetVolume(vol)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				linkButton(c, "静音", func() { a.PL.ToggleMute() })
				ui.Spacer(c)
				ui.Textf(c, "%d%%", int(vol*100)).FontSize(fz(12)).TextColor(t.TextMuted)
			})
		})
	})
	return b
}

func (a *App) modeBtn(c *ui.Context, t *ui.Theme) ui.Element {
	icon := "loopAll"
	switch a.PL.Mode() {
	case "off":
		icon = "loopOff"
	case "one":
		icon = "loopOne"
	}
	return a.iconBtn(c, icon, "循环模式", 17, func() { a.PL.CycleMode() })
}

func (a *App) shuffleBtn(c *ui.Context, t *ui.Theme) ui.Element {
	shuf := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	shuf.Children(func() {
		ic := ui.Icon(c, Icons["shuffle"]).FontSize(fz(16)).AlignSelf(ui.Center)
		if a.PL.Shuffle() {
			ic.TextColor(t.Accent)
		}
	})
	if shuf.Hovered() {
		shuf.Background(t.SurfaceHover)
	}
	shuf.Tooltip("随机播放")
	if shuf.Clicked() {
		a.PL.SetShuffle(!a.PL.Shuffle())
	}
	return shuf
}

// qualityPill 是播放条上的音质胶囊（浅色底，跟随主题）。
func (a *App) qualityPill(c *ui.Context, t *ui.Theme) ui.Element {
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, t.Border).Transition(hoverFade)
	pill.Children(func() {
		ui.Text(c, a.qualityText()).FontSize(fz(11)).FontWeight(600)
	})
	if pill.Hovered() {
		pill.Background(t.SurfaceHover)
	}
	pill.Tooltip("音质")
	pill.Label("音质")
	if pill.Clicked() {
		a.qualityOpen = !a.qualityOpen
	}
	a.qualityMenu(c, pill, t.Text, t.Accent.Alpha(0.12), t.SurfaceHover)
	return pill
}

// qualityText 当前生效音质的展示文本（会话覆盖优先于配置）。
func (a *App) qualityText() string {
	tier := a.PL.Quality()
	degraded := false
	if st := a.PL.Stream(); st != nil {
		tier = st.Tier
		degraded = st.Degraded
	}
	return qualityLabel(tier, degraded)
}

// qualityMenu 是音档选择菜单。播放条与正在播放页共用——两处只有配色不同，
// 逻辑（选中项高亮、切换后关闭、写回配置）完全一致。
func (a *App) qualityMenu(c *ui.Context, anchor ui.Element, fg ui.Color, activeBg, hoverBg ui.Color) {
	ui.Popover(c, anchor, &a.qualityOpen, func() {
		t := c.Theme()
		panel := ui.Column(c).Width(210).Padding(6).
			Background(t.Surface).Radius(12).Border(1, t.Border).
			Shadow(0, 10, 32, 0, ui.RGBA(0, 0, 0, 0.18))
		panel.Children(func() {
			ids, labels := tierLabels(a.PL.TierTable())
			cur := a.PL.Quality()
			for i, id := range ids {
				item := ui.ButtonBase(c).FillWidth().Padding(7, 10).Radius(8).Transition(hoverFade)
				sel := cur == id
				item.Children(func() {
					ui.Text(c, labels[i]).FontSize(fz(13)).Grow(1)
					if sel {
						ui.Icon(c, Icons["play"]).FontSize(fz(13)).TextColor(fg).AlignSelf(ui.Center)
					}
				})
				if sel {
					item.Background(activeBg)
				} else if item.Hovered() {
					item.Background(hoverBg)
				}
				if item.Clicked() {
					a.qualityOpen = false
					a.PL.SwitchQuality(id)
					if id == "auto" {
						a.Conf.Set("Quality.DefaultQuality", "auto")
					}
				}
			}
		})
	})
}

// loveBtn 是播放条上的红心。
//
// 未收藏态用正文色（t.Text）而不是 t.TextMuted：这是一枚只有轮廓的线框图标，
// 深色底上再降一档亮度就没什么可读性了（用户报的「无红心状态的红心在深色
// 模式无可读性」）。正文色在浅/深两套主题下都有 10:1 以上的对比度；悬停时
// 预演红心色（告诉用户点下去会变成什么），收藏后是实心红心。
func (a *App) loveBtn(c *ui.Context, s player.Song, hasCur bool, t *ui.Theme) ui.Element {
	if !hasCur {
		return ui.Element{}
	}
	loved := a.PL.IsLoved(s.Mid)
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	hover := b.Hovered()
	b.Children(func() {
		name, col := heartStyle(loved, hover, t)
		ui.Icon(c, Icons[name]).FontSize(fz(17)).TextColor(col).AlignSelf(ui.Center).
			Label(loveLabel(loved))
	})
	if !loved && hover {
		b.Background(t.SurfaceHover)
	}
	b.Tooltip(loveLabel(loved))
	if b.Clicked() {
		a.PL.ToggleLove(s)
	}
	return b
}

func (a *App) barBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#171c18")
	}
	return ui.Hex("#fbf8f1")
}
