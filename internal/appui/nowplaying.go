package appui

import (
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// 正在播放页的布局常量：内容层与歌词列宽都从它们推导，不能各写一份
// （逐字高亮要先按歌词列宽量行高，宽度对不上换行的行就会错位）。
const (
	npPadY  = 40.0  // 内容层上下内边距
	npPadX  = 52.0  // 内容层左右内边距
	npGap   = 48.0  // 歌词列与封面列的间距
	npSideW = 340.0 // 右侧封面/信息列宽

	// 歌词列首尾留白：主项目 .np-lyrics 用 38% 的伪元素留白，MyGo 没有
	// mask-image，改用等价的上下 padding。留白随窗口高度缩放，但夹在
	// 72~160px：小窗口不把列表挤没，大窗口也不会让首行飘到画面外。
	npPadMin    = 72.0
	npPadMax    = 160.0
	npPadFactor = 0.22
)

// npLyricWidth 是歌词列的可排宽度：内容层宽 − 两侧内边距 − 列间距 − 封面列。
func npLyricWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	v := w - 2*npPadX - npGap - npSideW
	if v < 120 {
		v = 120
	}
	return v
}

// npListPadding 返回歌词列表首尾留白（上下对称）。用窗口高度而不是列表
// 的实时高度：列表此时还没布局，MyGo 的 Context.Size 只给窗口尺寸；
// 对称 padding 也保证 ScrollTo(Center) 的居中语义不受它影响。
func npListPadding(c *ui.Context) float32 {
	_, h := c.Size()
	pad := float32(npPadFactor) * h
	if pad < npPadMin {
		pad = npPadMin
	}
	if pad > npPadMax {
		pad = npPadMax
	}
	return pad
}

// nowPlaying 是正在播放页：绝对定位覆盖内容区（播放条仍在其下方可见）。
//
// 动效（对齐主项目 .np）：整体上滑 + 淡入，.34s cubic-bezier(.32,.72,.24,1)；
// 关闭时同样滑回下方（MyGo 的 Exit 会给退场元素留一份副本，兄弟不为它留位）。
func (a *App) nowPlaying(c *ui.Context, t *ui.Theme) {
	cur, hasCur := a.PL.Current()
	coverLarge := ""
	if hasCur {
		coverLarge = coverURL(cur.AlbumPmid, cur.AlbumMid, 500)
	}

	np := ui.Box(c).Key("nowplaying").Absolute().Fill().Background(npBase).
		Transition(ui.ElementTransition{
			Duration: 340 * time.Millisecond,
			Ease:     easePanel,
			Enter:    &ui.Motion{Y: 56, Opacity: 0},
			Exit:     &ui.Motion{Y: 56, Opacity: 0},
		})
	np.Children(func() {
		a.npBackdrop(c, coverLarge)

		// 内容层：必须也是绝对定位。
		//
		// 根因（用户报的「正在播放页封面渲染透明化」）：MyGo 里 Absolute 子元素
		// 绘制在流式子元素【之上】（ui/paint.go：先画 flow、再画 absolute，与
		// CSS 定位元素的层叠关系一致）。背景层只做绝对定位的话，模糊封面图与
		// 黑色遮罩就会压在歌词和封面上——封面看着像掺进背景里、半透明。
		// 让内容层同样绝对定位，绘制顺序就回到「底图 → 遮罩 → 内容」；
		// 关闭按钮建在最后，仍然最上。
		content := ui.Row(c).Absolute().Fill().
			Padding(npPadY, npPadX, npPadY, npPadX).Gap(npGap).AlignItems(ui.Center)
		content.Children(func() {
			a.lyricColumn(c, t)
			a.npSide(c, t, cur, hasCur)
		})

		// 右上关闭
		closeBtn := ui.Box(c).Absolute().Top(14).Right(14).Size(30, 30).Radius(15).
			Background(ui.RGBA(255, 255, 255, 0.12)).Center().Transition(hoverFade)
		closeBtn.Children(func() {
			ui.Icon(c, Icons["clear"]).FontSize(fz(15)).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
		})
		if closeBtn.Hovered() {
			closeBtn.Background(ui.RGBA(255, 255, 255, 0.22))
		}
		if closeBtn.Clicked() {
			a.npOpen = false
			a.npMoreOpen = false
			a.npQInfoOpen = false
		}
	})
}

// npBackdrop 画正在播放页的背景两层：模糊封面底 + 轻度压暗遮罩。
//
// 换曲时按封面地址重置淡入时钟：旧图的模糊底会消失、新图要等下载，中间空出
// 的那一拍就是主项目注释里说的「切歌闪一下」——淡入把它盖过去。这里只淡入
// 装饰层、不动内容层，所以哪怕动画没跑完，封面与歌词也始终是实心的。
func (a *App) npBackdrop(c *ui.Context, cover string) {
	if blur := a.Covers.GetBlur(cover, a.invalidate); blur != nil {
		if a.npBlurKey != cover {
			a.npBlurKey, a.npBlurAt = cover, c.Now()
		}
		img := ui.Image(c, blur).Absolute().Fill().Fit(ui.Cover)
		styleFade(c, motion{
			start: a.npBlurAt,
			dur:   420 * time.Millisecond,
			ease:  ui.EaseOut,
		}, img, 0, npBlurOpacity)
	}

	// 压暗遮罩：上浅下深（主项目 .np-scrim 的 180° 渐变）。注意角度是 180
	// 而不是 90——MyGo 与 CSS 同口径，90° 是「向右」，那样渐变会横过来。
	ui.Box(c).Absolute().Fill().Gradient(npScrimTop, npScrimBottom, 180)

	// 左缘再压一层：歌词列是纯白文字，背景却是任意色相的模糊封面，亮暖封面
	// 下会撞色。主项目同样为逐字模式加深左缘（.np.kara .np-scrim），这里
	// 行级/逐字都用，向右 60% 收干净。
	ui.Box(c).Absolute().Fill().
		Gradient(ui.RGBA(11, 14, 25, 0.7), ui.RGBA(11, 14, 25, 0), 90)
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
				ui.Text(c, "暂无歌词").FontSize(fz(14)).TextColor(ui.RGBA(255, 255, 255, 0.6))
			})
		default:
			if len(lines) == 0 {
				ui.Column(c).Fill().Center().Children(func() {
					ui.Text(c, "Quaver Astra").FontSize(fz(15)).FontWeight(700).
						TextColor(ui.RGBA(255, 255, 255, 0.7))
				})
				break
			}
			a.npList.Key = nil
			a.npList.Label = func(i int) string { return lines[i].Text }
			// 首尾 padding 负责留白；不再叠加 Absolute 渐变——渐变只要横跨
			// 内容层就会在右侧模糊底上切出硬边（用户反馈过这条异常分界）。
			// 列表自身负责裁切滚动内容，背景保持原本连续的模糊渐变。
			pad := npListPadding(c)
			ui.List(c, &a.npList, len(lines), func(i int) {
				a.lyricColumnLine(c, lines[i], i)
			}).Grow(1).Padding(pad, 0, pad, 0)
		}
	})
}

// lyricColumnLine 是歌词列表的一行（与列表容器分开命名，便于单独维护行样式）。
func (a *App) lyricColumnLine(c *ui.Context, line player.LyricLine, index int) {
	cur := a.PL.LyricIndex(a.PL.Position())
	isCur := index == cur
	scale := a.Conf.Float("Style.LyricScale", 100) / 100
	size := 17 * float32(scale)
	if isCur {
		size = 20 * float32(scale)
	}
	row := ui.Column(c).FillWidth().Padding(6, 0).Gap(2).Radius(8).
		Cursor(ui.CursorPointer).Transition(hoverFade)
	// 行间过渡：非当前句压暗（主项目 .np-ly-line 是 opacity .34 + blur(1.6px)；
	// MyGo 的元素没有模糊滤镜，用透明度把同一件事做出来）。走 Animate 而不是
	// 硬切色值，「成为当前句 / 不再是当前句」这一跳就是缓动的。
	target := float32(0.42)
	if isCur {
		target = 1
	}
	row.Opacity(row.Animate("lyric-line", target, 260*time.Millisecond))
	row.Children(func() {
		if !(isCur && a.karaokeLineAt(c, index, size)) {
			txt := ui.Text(c, line.Text).Font(a.lyricFontFamily()).FontSize(fz(size)).TextColor(ui.Hex("#ffffff"))
			// 主项目行级歌词给非当前句 500；原生只给 400 会在模糊底上
			// 过早失去笔画，当前句再升到 800，层级才和 AMLL 的前后景一致。
			txt.FontWeight(500)
			if isCur {
				txt.FontWeight(800)
			}
		}
		if line.Trans != "" && a.PL.ShowTranslation() {
			col := ui.RGBA(255, 255, 255, 0.8)
			if !isCur {
				col = ui.RGBA(255, 255, 255, 0.55)
			}
			// 翻译允许换行：长译文被 SingleLine 截断时，用户看不到完整信息；
			// 列表会按行高重新居中，滚动跟随不受影响。
			ui.Text(c, line.Trans).Font(a.lyricFontFamily()).FontSize(fz(size * 0.7)).TextColor(col)
		}
	})
	if row.Hovered() && !isCur {
		row.Background(ui.RGBA(255, 255, 255, 0.055))
	}
	// 点击行跳转
	if row.Clicked() {
		if d := a.PL.Duration(); d > 0 {
			a.PL.SeekTo(line.Time / d)
		}
	}
}

// karaokeLineAt 在「这一行有词级时间轴」时画逐字高亮，返回是否接管渲染。
// index 是行号，不能用「当前行」反查——列表在建别的行时也会调到这里。
func (a *App) karaokeLineAt(c *ui.Context, index int, size float32) bool {
	ql, ok := a.PL.QrcLineAt(index)
	if !ok || len(ql.Words) == 0 {
		return false
	}
	a.karaokeLine(c, ql, size, npLyricWidth(c))
	return true
}

// npSide 右侧：封面、标题、音质、操作。
func (a *App) npSide(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) {
	_ = t
	ui.Column(c).Width(npSideW).Gap(12).AlignItems(ui.Center).Children(func() {
		var art *ui.Bitmap
		if hasCur {
			art = a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 500), a.invalidate)
		}
		if art != nil {
			ui.Image(c, art).Size(300, 300).Fit(ui.Cover).Radius(18).
				Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.4))
		} else {
			ui.Box(c).Size(300, 300).Radius(18).Background(ui.RGBA(255, 255, 255, 0.08)).Center().Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(fz(72)).TextColor(ui.RGBA(255, 255, 255, 0.4)).AlignSelf(ui.Center)
			})
		}
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			title := "未在播放"
			sub := ""
			if hasCur {
				title = cur.DisplayName()
				sub = cur.Artists
				if cur.Album != "" {
					sub += " - " + cur.Album
				}
			}
			ui.Text(c, title).FontSize(fz(21)).FontWeight(800).
				TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(fz(13)).TextColor(ui.RGBA(255, 255, 255, 0.72)).SingleLine().Ellipsis("…")
		})
		// 音质 + 更多。未在播放时也在：两个浮层各自的空态文案（未在播放 /
		// 等待播放流…）才有着落，主项目的 np-morewrap 同样常驻。
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			a.qualityPillNP(c)
			ui.Spacer(c)
			more := ui.ButtonBase(c).Size(30, 30).Radius(15).Center().Transition(hoverFade)
			more.Children(func() {
				ui.Icon(c, Icons["more"]).FontSize(fz(17)).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
			})
			if more.Hovered() {
				more.Background(ui.RGBA(255, 255, 255, 0.14))
			}
			more.Tooltip("更多操作").Label("更多操作")
			if more.Clicked() {
				a.npMoreOpen = !a.npMoreOpen
				a.npQInfoOpen = false // 同一锚区只开一个（主项目 closeQMenu 的互斥）
			}
			a.npMoreMenu(c, more)
		})
	})
}

// qualityPillNP 在深色背景上的音质胶囊。它是【只读】的音频流参数入口：
// 点开的是流信息浮窗（编码/采样率/采样精度/码率/声道 + 档位徽标），不是
// 音质切换菜单——切档在播放条胶囊上（qualityMenu），两者状态互相独立。
func (a *App) qualityPillNP(c *ui.Context) {
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, ui.RGBA(255, 255, 255, 0.35)).
		Transition(hoverFade)
	pill.Children(func() {
		ui.Text(c, a.qualityText()).FontSize(fz(11)).FontWeight(600).TextColor(ui.Hex("#ffffff"))
	})
	if pill.Hovered() {
		pill.Background(ui.RGBA(255, 255, 255, 0.12))
	}
	pill.Tooltip("音质").Label("音质")
	if pill.Clicked() {
		a.npQInfoOpen = !a.npQInfoOpen
		a.npMoreOpen = false // 同一锚区只开一个
	}
	a.npQualityInfo(c, pill)
}

// ===== 播放队列面板 =====

func (a *App) queuePanel(c *ui.Context) {
	t := c.Theme()
	queueLen := a.PL.QueueLen()
	a.queueList.Key = nil
	a.queueList.Reorder = func(rows []int, to int) {
		a.PL.MoveInQueue(rows, to)
	}

	panel := ui.Column(c).Width(300).Background(a.sideBg(t)).BorderWidth(1, 0, 0, 1).BorderColor(t.Border)
	panel.Children(func() {
		ui.Row(c).FillWidth().Height(44).PaddingX(12).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Textf(c, "播放列表 · %d 首", queueLen).FontSize(fz(13)).FontWeight(700).Grow(1)
			clearBtn := ui.ButtonBase(c).Size(26, 26).Radius(13).Center().Transition(hoverFade)
			clearBtn.Children(func() { ui.Icon(c, Icons["trash"]).FontSize(fz(14)).AlignSelf(ui.Center) })
			if clearBtn.Hovered() {
				clearBtn.Background(t.SurfaceHover)
			}
			if clearBtn.Clicked() {
				a.PL.ClearQueue()
			}
			colBtn := ui.ButtonBase(c).Size(26, 26).Radius(13).Center().Transition(hoverFade)
			colBtn.Children(func() { ui.Icon(c, Icons["clear"]).FontSize(fz(14)).AlignSelf(ui.Center) })
			if colBtn.Hovered() {
				colBtn.Background(t.SurfaceHover)
			}
			if colBtn.Clicked() {
				a.queueOpen = false
				a.Conf.Set("Window.QueueOpen", false)
			}
		})
		cur, _ := a.PL.Current()
		a.queueList.Label = func(i int) string {
			row, ok := a.PL.QueueRow(i)
			if !ok {
				return ""
			}
			return row.DisplayName()
		}
		ui.List(c, &a.queueList, queueLen, func(i int) {
			if row, ok := a.PL.QueueRow(i); ok {
				a.queueRow(c, row, i, cur.Mid)
			}
		}).Grow(1)
	})
}

func (a *App) queueRow(c *ui.Context, s player.QueueRowView, i int, curMid string) {
	t := c.Theme()
	isCur := s.Mid == curMid
	row := ui.Row(c).FillWidth().Height(46).PaddingX(10).Gap(10).AlignItems(ui.Center).Radius(8).
		Transition(hoverFade)
	row.Children(func() {
		ui.Box(c).Width(22).Center().Children(func() {
			if isCur && a.PL.Playing() {
				ui.Icon(c, Icons["note"]).FontSize(fz(13)).TextColor(t.Accent).AlignSelf(ui.Center)
			} else {
				ui.Textf(c, "%d", i+1).FontSize(fz(11)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			}
		})
		if art := a.Covers.Get(coverURL(s.AlbumPmid, s.AlbumMid, 300), a.invalidate); art != nil {
			ui.Image(c, art).Size(34, 34).Fit(ui.Cover).Radius(5)
		} else {
			ui.Box(c).Size(34, 34).Radius(5).Background(t.SurfaceHover)
		}
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Text(c, s.DisplayName()).FontSize(fz(12.5)).SingleLine().Ellipsis("…")
			ui.Text(c, s.Artists).FontSize(fz(11)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		if row.Hovered() {
			del := ui.ButtonBase(c).Size(24, 24).Radius(12).Center().Transition(hoverFade)
			del.Children(func() { ui.Icon(c, Icons["clear"]).FontSize(fz(12)).AlignSelf(ui.Center) })
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
