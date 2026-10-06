package appui

import (
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

	bar := ui.Row(c).FillWidth().Height(64).PaddingX(16).Gap(12).
		AlignItems(ui.Center).Background(a.barBg(t)).Margin(8, 10, 10, 10).Radius(14)
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		// 进度填充：整个条高的圆角矩形
		if frac > 0.001 {
			w := r.W * frac
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, a.glowNow.Alpha(0.28), 14)
		}
	})

	bar.Children(func() {
		a.barSeek(c, bar, t)

		// 左：封面 + 标题
		cover := ui.ButtonBase(c).Size(46, 46).Radius(10).Clip()
		if art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 300), a.invalidate); art != nil {
			cover.Children(func() { ui.Image(c, art).Fill().Fit(ui.Cover) })
		} else {
			cover.Background(t.SurfaceHover)
			cover.Children(func() {
				ui.Box(c).Fill().Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(20).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			})
		}
		if cover.Clicked() && hasCur {
			a.npOpen = !a.npOpen
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
			ui.Text(c, title).FontSize(13.5).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})

		// 中：上一首 / 播放 / 下一首
		ui.Spacer(c)
		a.iconBtn(c, "prev", "上一首", 20, func() { a.PL.Prev() })
		a.playBtn(c, t)
		a.iconBtn(c, "next", "下一首", 20, func() { a.PL.Next(false) })
		ui.Spacer(c)

		// 右：时间 / 音量 / 模式 / 随机 / 音质 / 红心 / 队列
		ui.Textf(c, "%s / %s", fmtTime(a.PL.Position()), fmtTime(a.PL.Duration())).
			FontSize(11.5).TextColor(t.TextMuted).Font("monospace")
		a.volumeCtl(c, t)
		a.modeBtn(c, t)
		a.qualityPill(c, t)
		a.loveBtn(c, cur, hasCur, t)
		a.iconBtn(c, "queue", "播放队列", 18, func() {
			a.queueOpen = !a.queueOpen
			a.Conf.Set("Window.QueueOpen", a.queueOpen)
		})
	})
}

// barSeek 是覆盖整条播放条的 seek 面（在子控件之下拿指针）。
func (a *App) barSeek(c *ui.Context, bar *ui.Element, t *ui.Theme) {
	_ = c
	_ = t
	// 注意：barSeek 不渲染东西，只挂事件；子控件优先拿指针。
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
		ui.Icon(c, Icons[name]).FontSize(20).TextColor(t.Background).AlignSelf(ui.Center)
	})
	if btn.Hovered() {
		btn.Opacity(0.85)
	} else {
		btn.Opacity(1)
	}
	if btn.Clicked() {
		a.PL.PlayOrPause()
	}
}

func (a *App) iconBtn(c *ui.Context, icon, label string, size float32, fn func()) {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center()
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(size).AlignSelf(ui.Center)
	})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		fn()
	}
	_ = label
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
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center()
	b.Children(func() { ui.Icon(c, Icons[icon]).FontSize(17).AlignSelf(ui.Center) })
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
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
				ui.Textf(c, "%d%%", int(vol*100)).FontSize(12).TextColor(t.TextMuted)
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

	shuf := ui.ButtonBase(c).Size(32, 32).Radius(16).Center()
	shuf.Children(func() {
		ic := ui.Icon(c, Icons["shuffle"]).FontSize(16).AlignSelf(ui.Center)
		if a.PL.Shuffle() {
			ic.TextColor(t.Accent)
		}
	})
	if shuf.Hovered() {
		shuf.Background(t.SurfaceHover)
	}
	if shuf.Clicked() {
		a.PL.SetShuffle(!a.PL.Shuffle())
	}
}

func (a *App) qualityPill(c *ui.Context, t *ui.Theme) {
	st := a.PL.Stream()
	tier := a.PL.Quality()
	degraded := false
	if st != nil {
		tier = st.Tier
		degraded = st.Degraded
	}
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, t.Border)
	pill.Children(func() {
		ui.Text(c, qualityLabel(tier, degraded)).FontSize(11).FontWeight(600)
	})
	if pill.Hovered() {
		pill.Background(t.SurfaceHover)
	}
	if pill.Clicked() {
		a.qualityOpen = !a.qualityOpen
	}
	ui.Popover(c, pill, &a.qualityOpen, func() {
		panel := ui.Column(c).Width(210).Padding(6).
			Background(t.Surface).Radius(12).Border(1, t.Border).Shadow(0, 10, 32, 0, ui.RGBA(0, 0, 0, 0.18))
		panel.Children(func() {
			ids, labels := tierLabels(a.PL.TierTable())
			cur := a.PL.Quality()
			for i, id := range ids {
				item := ui.ButtonBase(c).FillWidth().Padding(7, 10).Radius(8)
				item.Children(func() {
					ui.Text(c, labels[i]).FontSize(13).Grow(1)
					if cur == id {
						ui.Icon(c, Icons["play"]).FontSize(13).TextColor(t.Accent).AlignSelf(ui.Center)
					}
				})
				if cur == id {
					item.Background(t.Accent.Alpha(0.12))
				} else if item.Hovered() {
					item.Background(t.SurfaceHover)
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

func (a *App) loveBtn(c *ui.Context, s player.Song, hasCur bool, t *ui.Theme) {
	if !hasCur {
		ui.Box(c).Size(32, 32)
		return
	}
	loved := a.PL.IsLoved(s.Mid)
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center()
	b.Children(func() {
		name := "heart"
		col := t.TextMuted
		if loved {
			name = "heartFill"
			col = t.Danger
		}
		ui.Icon(c, Icons[name]).FontSize(17).TextColor(col).AlignSelf(ui.Center)
	})
	if !loved && b.Hovered() {
		b.Background(t.SurfaceHover)
	}
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
