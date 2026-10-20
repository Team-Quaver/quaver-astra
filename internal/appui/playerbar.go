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
const barH = 64.0

// playerBar 底部 64px 播放条：进度填充铺底 + 拖拽 seek + 控制区。
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
	bar := ui.Row(c).Height(barH).PaddingX(16).Gap(12).
		AlignItems(ui.Center).Background(a.barBg(t)).Margin(0).Radius(0)
	// 进度填充要缓动：位置采样只有 5Hz（player tick 200ms），直接用采样值画
	// 会一格一格跳。拖拽时不做缓动，要跟手。
	fill := frac
	if !a.seekDragging {
		fill = bar.Animate("seek-fill", frac, 260*time.Millisecond)
	}
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		// 进度填充跟随播放条外形，保持整条直角。
		if fill > 0.001 {
			w := r.W * fill
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, a.glowNow.Alpha(0.28), 0)
		}
	})

	bar.Children(func() {
		a.barSeek(c, bar)

		// 左：封面 + 标题
		cover := ui.ButtonBase(c).Size(46, 46).Radius(10).Clip().Transition(hoverFade)
		if art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 300), a.invalidate); art != nil {
			cover.Children(func() { ui.Image(c, art).Fill().Fit(ui.Cover) })
		} else {
			cover.Background(t.SurfaceHover)
			cover.Children(func() {
				ui.Box(c).Fill().Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(20)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			})
		}
		if cover.Clicked() && hasCur {
			a.npOpen = !a.npOpen
			// 正在播放页的浮层跟着页走：不然重新打开时旧菜单/浮窗还挂着。
			a.npMoreOpen = false
			a.npQInfoOpen = false
		}
		ui.Column(c).Width(200).Gap(1).Children(func() {
			title := ""
			sub := ""
			if a.PL.Loading() {
				title = "加载中…"
			} else if e := a.PL.Error(); e != "" {
				title = "播放失败"
				sub = e
			} else if hasCur {
				title = cur.DisplayName()
				sub = cur.Artists
			} else {
				title = "Quaver Astra"
				sub = "双击歌曲开始播放"
			}
			ui.Text(c, title).FontSize(fz(13.5)).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(fz(11.5)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})

		// 中：上一首 / 播放 / 下一首。无限电台只向前，上一首在底层与
		// 界面两层都关掉，不能只藏一个按钮。
		ui.Spacer(c)
		if a.PL.RadioActive() {
			a.iconBtnDisabled(c, "prev", "无限电台只向前播放", 20)
		} else {
			a.iconBtn(c, "prev", "上一首"+a.hotkeyTip(hkPrev), 20, func() { a.PL.Prev() })
		}
		a.playBtn(c, t)
		a.iconBtn(c, "next", "下一首"+a.hotkeyTip(hkNext), 20, func() { a.PL.Next(false) })
		ui.Spacer(c)

		// 右：时间 / 音量 / 模式 / 随机 / 音质 / 红心 / 队列。
		// 电台没有可编辑队列、循环与随机也不参与推进，所以这些入口收掉。
		ui.Textf(c, "%s / %s", fmtTime(a.PL.Position()), fmtTime(a.PL.Duration())).
			FontSize(fz(11.5)).TextColor(t.TextMuted).Font("monospace")
		a.volumeCtl(c, t)
		if a.PL.RadioActive() {
			ui.Row(c).Padding(4, 8).Radius(999).Border(1, t.Accent.Alpha(0.35)).Children(func() {
				ui.Text(c, "∞ 无限电台").FontSize(fz(10.5)).FontWeight(700).TextColor(t.Accent)
			})
		} else {
			a.modeBtn(c, t)
		}
		a.qualityPill(c, t)
		a.loveBtn(c, cur, hasCur, t)
		if !a.PL.RadioActive() {
			a.queueBtn(c, t)
		}
	})
}

// barSeek 是覆盖整条播放条的 seek 面（在子控件之下拿指针）。
// 它只挂事件、不渲染内容；子控件优先拿到指针。
func (a *App) barSeek(c *ui.Context, bar ui.Element) {
	_ = c
	if bar.Pressed() {
		if !a.seekDragging {
			a.seekDragging = true
		}
		if x, _, over := bar.PointerPosition(); over {
			if w := bar.Bounds().W; w > 0 {
				a.seekFrac = clampF32(x / w)
			}
		}
	} else if a.seekDragging {
		a.seekDragging = false
		if a.PL.Duration() > 0 {
			a.PL.SeekTo(float64(a.seekFrac))
		}
	}
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

func (a *App) playBtn(c *ui.Context, t *ui.Theme) {
	btn := ui.ButtonBase(c).Size(40, 40).Radius(20).Center().Background(t.Text)
	// tooltip 里带上快捷键：播放条是纯图标按钮，用户得知道能不能用键盘控制。
	btn.Tooltip("播放/暂停" + a.hotkeyTip(hkPlayPause))
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
}

// iconBtn 是播放条上的圆形图标按钮。label 用于无障碍 tooltip——
// 纯图标按钮没有文字说明，键盘/读屏用户无从得知其作用。
func (a *App) iconBtn(c *ui.Context, icon, label string, size float32, fn func()) {
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
}

// iconBtnDisabled 是保持占位但不接受点击的图标按钮。电台的上一首用它，
// 避免布局在中段塌掉；禁用态压暗，tooltip 说明原因。
func (a *App) iconBtnDisabled(c *ui.Context, icon, label string, size float32) {
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Opacity(0.34)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(size)).AlignSelf(ui.Center)
	})
	b.Disabled(true)
	if label != "" {
		b.Tooltip(label)
	}
}

// queueBtn 是播放条上的队列开关。面板开着时高亮：队列浮窗盖在正在播放页的
// 右侧，图标不带状态的话，「开着队列」这件事在整页浮层上就没有任何痕迹。
func (a *App) queueBtn(c *ui.Context, t *ui.Theme) {
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
}

func (a *App) volumeCtl(c *ui.Context, t *ui.Theme) {
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
}

func (a *App) modeBtn(c *ui.Context, t *ui.Theme) {
	icon := "loopAll"
	switch a.PL.Mode() {
	case "off":
		icon = "loopOff"
	case "one":
		icon = "loopOne"
	}
	a.iconBtn(c, icon, "循环模式", 17, func() { a.PL.CycleMode() })

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
}

// qualityPill 是播放条上的音质胶囊（浅色底，跟随主题）。
func (a *App) qualityPill(c *ui.Context, t *ui.Theme) {
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, t.Border).Transition(hoverFade)
	pill.Children(func() {
		ui.Text(c, a.qualityText()).FontSize(fz(11)).FontWeight(600)
	})
	if pill.Hovered() {
		pill.Background(t.SurfaceHover)
	}
	pill.Tooltip("音质")
	if pill.Clicked() {
		a.qualityOpen = !a.qualityOpen
	}
	a.qualityMenu(c, pill, t.Text, t.Accent.Alpha(0.12), t.SurfaceHover)
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
func (a *App) loveBtn(c *ui.Context, s player.Song, hasCur bool, t *ui.Theme) {
	if !hasCur {
		ui.Box(c).Size(32, 32)
		return
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
}

func (a *App) barBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#1e2026")
	}
	return ui.Hex("#ffffff")
}
