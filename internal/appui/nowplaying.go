package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// nowPlaying 是正在播放页：绝对定位覆盖内容区（播放条仍在其下方可见）。
func (a *App) nowPlaying(c *ui.Context, t *ui.Theme) {
	cur, hasCur := a.PL.Current()
	coverURLLarge := ""
	if hasCur {
		coverURLLarge = coverURL(cur.AlbumPmid, cur.AlbumMid, 500)
	}

	np := ui.Box(c).Absolute().Fill().Background(a.npBg(t))
	np.Children(func() {
		// 模糊封面底 + 渐变压暗（均为绝对定位背景层，内容行是唯一流式子元素）
		if blur := a.Covers.GetBlur(coverURLLarge, a.invalidate); blur != nil {
			ui.Image(c, blur).Absolute().Fill().Fit(ui.Cover).Opacity(0.5)
		}
		ui.Box(c).Absolute().Fill().
			Gradient(ui.RGBA(0, 0, 0, 0.35), ui.RGBA(0, 0, 0, 0.78), 90)

		// 内容：左歌词 + 右封面/信息
		ui.Row(c).Fill().Padding(40, 52, 40, 52).Gap(48).AlignItems(ui.Center).Children(func() {
			a.lyricColumn(c, t)
			a.npSide(c, t, cur, hasCur)
		})

		// 右上关闭
		closeBtn := ui.Box(c).Absolute().Top(14).Right(14).Size(30, 30).Radius(15).
			Background(ui.RGBA(255, 255, 255, 0.12)).Center()
		closeBtn.Children(func() {
			ui.Icon(c, Icons["clear"]).FontSize(15).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
		})
		if closeBtn.Hovered() {
			closeBtn.Background(ui.RGBA(255, 255, 255, 0.22))
		}
		if closeBtn.Clicked() {
			a.npOpen = false
		}
	})
}

func (a *App) npBg(t *ui.Theme) ui.Color {
	return ui.Hex("#0b0e19")
}

// lyricColumn 左侧歌词列（行级歌词 + 翻译 + 跟随滚动）。
func (a *App) lyricColumn(c *ui.Context, t *ui.Theme) {
	lines, state := a.PL.LyricLines()
	cur := a.PL.LyricIndex(a.PL.Position())

	if state == "ok" && cur >= 0 && cur != a.lastLyricLine {
		a.lastLyricLine = cur
		a.npList.ScrollTo(cur, ui.Center)
	}

	ui.Column(c).Grow(1).Fill().Gap(8).Children(func() {
		switch state {
		case "loading":
			ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c) })
		case "none":
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "暂无歌词").FontSize(14).TextColor(ui.RGBA(255, 255, 255, 0.6))
			})
		default:
			a.npList.Key = nil
			a.npList.Label = func(i int) string { return lines[i].Text }
			ui.List(c, &a.npList, len(lines), func(i int) {
				a.lyricLine(c, lines[i], i == cur)
			}).Grow(1)
			if len(lines) == 0 {
				ui.Column(c).Fill().Center().Children(func() {
					ui.Text(c, "Quaver Astra").FontSize(15).FontWeight(700).
						TextColor(ui.RGBA(255, 255, 255, 0.7))
				})
			}
		}
	})
}

func (a *App) lyricLine(c *ui.Context, line player.LyricLine, isCur bool) {
	t := c.Theme()
	_ = t
	scale := a.Conf.Float("Style.LyricScale", 100) / 100
	size := 17 * float32(scale)
	if isCur {
		size = 20 * float32(scale)
	}
	row := ui.Column(c).FillWidth().Padding(6, 0).Gap(2)
	row.Children(func() {
		txt := ui.Text(c, line.Text).FontSize(size)
		if isCur {
			txt.FontWeight(800).TextColor(ui.Hex("#ffffff"))
		} else {
			txt.TextColor(ui.RGBA(255, 255, 255, 0.45))
		}
		if line.Trans != "" && a.PL.ShowTranslation() {
			tr := ui.Text(c, line.Trans).FontSize(size * 0.7).SingleLine()
			if isCur {
				tr.TextColor(ui.RGBA(255, 255, 255, 0.85))
			} else {
				tr.TextColor(ui.RGBA(255, 255, 255, 0.4))
			}
		}
	})
	// 点击行跳转
	if row.Clicked() {
		if d := a.PL.Duration(); d > 0 {
			a.PL.SeekTo(line.Time / d)
		}
	}
}

// npSide 右侧：封面、标题、音质、操作。
func (a *App) npSide(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) {
	_ = t
	ui.Column(c).Width(340).Gap(12).AlignItems(ui.Center).Children(func() {
		if !hasCur {
			return
		}
		art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 500), a.invalidate)
		if art != nil {
			ui.Image(c, art).Size(300, 300).Fit(ui.Cover).Radius(18).
				Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.4))
		} else {
			ui.Box(c).Size(300, 300).Radius(18).Background(ui.RGBA(255, 255, 255, 0.08)).Center().Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(72).TextColor(ui.RGBA(255, 255, 255, 0.4)).AlignSelf(ui.Center)
			})
		}
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, cur.DisplayName()).FontSize(21).FontWeight(800).
				TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
			sub := cur.Artists
			if cur.Album != "" {
				sub += " - " + cur.Album
			}
			ui.Text(c, sub).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.72)).SingleLine().Ellipsis("…")
		})
		// 音质 + 更多
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			a.qualityPillNP(c)
			ui.Spacer(c)
			more := ui.ButtonBase(c).Size(30, 30).Radius(15).Center()
			more.Children(func() {
				ui.Icon(c, Icons["more"]).FontSize(17).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
			})
			if more.Hovered() {
				more.Background(ui.RGBA(255, 255, 255, 0.14))
			}
			more.ContextMenu(func(m *ui.Menu) {
				if m.Item("同名搜索").Chosen() {
					a.npOpen = false
					a.Router.Push("/search?kw=" + cur.DisplayName())
				}
				if m.Item("显示翻译").Checked(a.PL.ShowTranslation()).Chosen() {
					a.PL.SetShowTranslation(!a.PL.ShowTranslation())
				}
			})
		})
	})
}

// qualityPillNP 在深色背景上的音质胶囊。
func (a *App) qualityPillNP(c *ui.Context) {
	st := a.PL.Stream()
	tier := a.PL.Quality()
	degraded := false
	if st != nil {
		tier = st.Tier
		degraded = st.Degraded
	}
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, ui.RGBA(255, 255, 255, 0.35))
	pill.Children(func() {
		ui.Text(c, qualityLabel(tier, degraded)).FontSize(11).FontWeight(600).TextColor(ui.Hex("#ffffff"))
	})
	if pill.Hovered() {
		pill.Background(ui.RGBA(255, 255, 255, 0.12))
	}
	if pill.Clicked() {
		a.qualityOpen = !a.qualityOpen
	}
	ui.Popover(c, pill, &a.qualityOpen, func() {
		t := c.Theme()
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
				}
			}
		})
	})
}

// ===== 播放队列面板 =====

func (a *App) queuePanel(c *ui.Context) {
	t := c.Theme()
	queue := a.PL.Queue()
	a.queueList.Key = nil
	a.queueList.Reorder = func(rows []int, to int) {
		a.PL.MoveInQueue(rows, to)
	}

	panel := ui.Column(c).Width(300).Background(a.sideBg(t)).BorderWidth(1, 0, 0, 1).BorderColor(t.Border)
	panel.Children(func() {
		ui.Row(c).FillWidth().Height(44).PaddingX(12).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Textf(c, "播放列表 · %d 首", len(queue)).FontSize(13).FontWeight(700).Grow(1)
			clearBtn := ui.ButtonBase(c).Size(26, 26).Radius(13).Center()
			clearBtn.Children(func() { ui.Icon(c, Icons["trash"]).FontSize(14).AlignSelf(ui.Center) })
			if clearBtn.Hovered() {
				clearBtn.Background(t.SurfaceHover)
			}
			if clearBtn.Clicked() {
				a.PL.ClearQueue()
			}
			colBtn := ui.ButtonBase(c).Size(26, 26).Radius(13).Center()
			colBtn.Children(func() { ui.Icon(c, Icons["clear"]).FontSize(14).AlignSelf(ui.Center) })
			if colBtn.Hovered() {
				colBtn.Background(t.SurfaceHover)
			}
			if colBtn.Clicked() {
				a.queueOpen = false
				a.Conf.Set("Window.QueueOpen", false)
			}
		})
		cur, _ := a.PL.Current()
		a.queueList.Label = func(i int) string { return queue[i].DisplayName() }
		ui.List(c, &a.queueList, len(queue), func(i int) {
			a.queueRow(c, queue, i, cur.Mid)
		}).Grow(1)
	})
}

func (a *App) queueRow(c *ui.Context, queue []player.Song, i int, curMid string) {
	t := c.Theme()
	s := queue[i]
	isCur := s.Mid == curMid
	row := ui.Row(c).FillWidth().Height(46).PaddingX(10).Gap(10).AlignItems(ui.Center).Radius(8)
	row.Children(func() {
		ui.Box(c).Width(22).Center().Children(func() {
			if isCur && a.PL.Playing() {
				ui.Icon(c, Icons["note"]).FontSize(13).TextColor(t.Accent).AlignSelf(ui.Center)
			} else {
				ui.Textf(c, "%d", i+1).FontSize(11).TextColor(t.TextMuted).AlignSelf(ui.Center)
			}
		})
		if art := a.Covers.Get(coverURL(s.AlbumPmid, s.AlbumMid, 300), a.invalidate); art != nil {
			ui.Image(c, art).Size(34, 34).Fit(ui.Cover).Radius(5)
		} else {
			ui.Box(c).Size(34, 34).Radius(5).Background(t.SurfaceHover)
		}
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Text(c, s.DisplayName()).FontSize(12.5).SingleLine().Ellipsis("…")
			ui.Text(c, s.Artists).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		if row.Hovered() {
			del := ui.ButtonBase(c).Size(24, 24).Radius(12).Center()
			del.Children(func() { ui.Icon(c, Icons["clear"]).FontSize(12).AlignSelf(ui.Center) })
			if del.Clicked() {
				a.PL.RemoveAt(i)
			}
		}
	})
	if isCur {
		row.Background(a.rowTint(t))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.PL.Jump(i)
	}
}
