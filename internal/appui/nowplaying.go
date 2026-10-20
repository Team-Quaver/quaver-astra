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
		qrcLine, hasQRC := a.PL.QrcLineAt(index)
		switch {
		case hasQRC && qrcLine.HasRuby():
			// 日文 QRC 的振假名挂在词上：即使不是当前句也画在词上方，
			// 这是歌词正文的一部分，不受翻译/音译档位影响。
			a.qrcRubyLine(c, qrcLine, isCur, size)
		case isCur && a.karaokeLineAt(c, index, size):
			// 已经由逐字绘制接管。
		default:
			txt := ui.Text(c, line.Text).Font(a.lyricFontFamily()).FontSize(fz(size)).TextColor(ui.Hex("#ffffff"))
			// 主项目行级歌词给非当前句 500；原生只给 400 会在模糊底上
			// 过早失去笔画，当前句再升到 800，层级才和 AMLL 的前后景一致。
			txt.FontWeight(500)
			if isCur {
				txt.FontWeight(800)
			}
		}
		if aux := player.AuxText(line, a.PL.LyricAuxMode()); aux != "" {
			col := ui.RGBA(255, 255, 255, 0.8)
			if !isCur {
				col = ui.RGBA(255, 255, 255, 0.55)
			}
			// 翻译/音译共用这一行；长文本允许换行，列表会按实际行高重新居中。
			ui.Text(c, aux).Font(a.lyricFontFamily()).FontSize(fz(size * 0.7)).TextColor(col)
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

// queuePalette 是队列面板的配色。
//
// 面板有两个落点：内容区右侧的停靠栏（正在播放页没开）和正在播放页上的浮窗。
// 列表本体、拖拽换序、当前行高亮、悬停删除在两处必须一模一样，不然两边迟早
// 长出不一样的队列；能变的只有颜色与容器装饰，所以把颜色单独抽出来。
type queuePalette struct {
	title, sub, hover, bg, border, tint, accent, icon ui.Color
	// line/divider 只在浮窗上用：停靠栏本来就贴着内容区边界，头行下再加一条
	// 横线显脏；浮窗悬在模糊封面上，需要一条线把头行和列表分开。
	line    ui.Color
	divider bool
}

// queuePalette 取本帧的队列配色。glass 为真时用正在播放页的深玻璃口径——
// 那一页恒为深底（见 npBase），跟随明暗主题的字色会在玻璃上糊掉。
func (a *App) queuePalette(t *ui.Theme, glass bool) queuePalette {
	if glass {
		return queuePalette{
			title: npPopText, sub: npPopText2, hover: npPopHover,
			bg: npPopBg, border: npPopBorder, tint: t.Accent.Alpha(0.16),
			accent: t.Accent, icon: npPopDim,
			line: npPopLine, divider: true,
		}
	}
	return queuePalette{
		title: t.Text, sub: t.TextMuted, hover: t.SurfaceHover,
		bg: a.sideBg(t), border: t.Border, tint: a.rowTint(t),
		accent: t.Accent, icon: t.Text,
	}
}

// queueHeadH/queueRowH 是面板的固定行高：头行与单首歌的行。浮窗要按内容算高，
// 这两处必须和 queueHead/queueRow 里写的高度是同一个数。
const (
	queueHeadH = 44.0
	queueRowH  = 46.0
)

// queueFloatH 是浮窗的内容高度：头行 + 各行 + 底边 8px 收口，装不下时封顶到
// 内容区（窗口高 − 播放条）减去上下 16px 的留白，列表自己滚动。
//
// 队列短的时候让面板跟着内容走，不拉着一条几百像素的空玻璃挂到播放条上。
func queueFloatH(c *ui.Context, n int) float32 {
	_, wh := c.Size()
	maxH := wh - barH - 32
	h := queueHeadH + float32(n)*queueRowH + 8
	if h > maxH {
		h = maxH
	}
	if minH := float32(queueHeadH + queueRowH + 8); h < minH {
		h = minH
	}
	return h
}

// queuePanel 是内容区右侧的停靠队列栏（正在播放页未打开时的形态）。
func (a *App) queuePanel(c *ui.Context) {
	s := a.queuePalette(c.Theme(), false)
	panel := ui.Column(c).Width(300).Background(s.bg).
		BorderWidth(1, 0, 0, 1).BorderColor(s.border)
	panel.Children(func() {
		a.queueHead(c, s)
		a.queueBody(c, s)
	})
}

// queueFloat 是正在播放页上的队列浮窗：右侧留 16px，悬在整页之上。
//
// 它必须建在 nowPlaying 之后：MyGo 里绝对子元素按建树顺序绘制，命中链也按同一
// 顺序取最上面的一个，晚建的才盖得住整页、也才收得到点击——不然点队列里的行，
// 命中的是底下的封面/歌词层，看着就是「按了没反应」。
func (a *App) queueFloat(c *ui.Context, t *ui.Theme) {
	s := a.queuePalette(t, true)
	panel := ui.Column(c).Key("queue-float").Absolute().
		Top(16).Right(16).Width(320).Height(queueFloatH(c, a.PL.QueueLen())).
		Background(s.bg).Radius(16).Border(1, s.border).
		Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.45)).Clip().
		Transition(ui.ElementTransition{
			Duration: 220 * time.Millisecond,
			Ease:     easePanel,
			Enter:    &ui.Motion{X: 24, Opacity: 0},
		})
	panel.Children(func() {
		a.queueHead(c, s)
		a.queueBody(c, s)
	})
}

// queueHead 是面板头行：标题 + 清空 + 收起。两个落点共用。
func (a *App) queueHead(c *ui.Context, s queuePalette) {
	ui.Row(c).FillWidth().Height(queueHeadH).PaddingX(12).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Textf(c, "播放列表 · %d 首", a.PL.QueueLen()).FontSize(fz(13)).FontWeight(700).
			TextColor(s.title).SingleLine().Grow(1)
		a.queueIconBtn(c, s, "trash", "清空播放列表", func() { a.PL.ClearQueue() })
		a.queueIconBtn(c, s, "clear", "收起播放列表", func() {
			a.queueOpen = false
			a.Conf.Set("Window.QueueOpen", false)
		})
	})
	if s.divider {
		ui.Box(c).FillWidth().Height(1).Background(s.line)
	}
}

// queueIconBtn 是头行上的小图标按钮。纯图标要有 Label：读屏与测试都得知道
// 这个按钮干什么。
func (a *App) queueIconBtn(c *ui.Context, s queuePalette, icon, label string, run func()) {
	b := ui.ButtonBase(c).Size(26, 26).Radius(13).Center().Transition(hoverFade)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(14)).TextColor(s.icon).AlignSelf(ui.Center)
	})
	if b.Hovered() {
		b.Background(s.hover)
	}
	b.Tooltip(label)
	b.Label(label)
	if b.Clicked() {
		run()
	}
}

// queueBody 是队列列表本体：两个落点共用（拖拽换序、当前行高亮、悬停删除）。
func (a *App) queueBody(c *ui.Context, s queuePalette) {
	n := a.PL.QueueLen()
	a.queueList.Key = nil
	a.queueList.Reorder = func(rows []int, to int) {
		a.PL.MoveInQueue(rows, to)
	}
	cur, _ := a.PL.Current()
	a.queueList.Label = func(i int) string {
		row, ok := a.PL.QueueRow(i)
		if !ok {
			return ""
		}
		return row.DisplayName()
	}
	ui.List(c, &a.queueList, n, func(i int) {
		if row, ok := a.PL.QueueRow(i); ok {
			a.queueRow(c, s, row, i, cur.Mid)
		}
	}).Grow(1)
}

func (a *App) queueRow(c *ui.Context, s queuePalette, r player.QueueRowView, i int, curMid string) {
	isCur := r.Mid == curMid
	row := ui.Row(c).FillWidth().Height(queueRowH).PaddingX(10).Gap(10).AlignItems(ui.Center).Radius(8).
		Transition(hoverFade)
	row.Children(func() {
		ui.Box(c).Width(22).Center().Children(func() {
			if isCur && a.PL.Playing() {
				ui.Icon(c, Icons["note"]).FontSize(fz(13)).TextColor(s.accent).AlignSelf(ui.Center)
			} else {
				ui.Textf(c, "%d", i+1).FontSize(fz(11)).TextColor(s.sub).AlignSelf(ui.Center)
			}
		})
		if art := a.Covers.Get(coverURL(r.AlbumPmid, r.AlbumMid, 300), a.invalidate); art != nil {
			ui.Image(c, art).Size(34, 34).Fit(ui.Cover).Radius(5)
		} else {
			ui.Box(c).Size(34, 34).Radius(5).Background(s.hover)
		}
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Text(c, r.DisplayName()).FontSize(fz(12.5)).TextColor(s.title).SingleLine().Ellipsis("…")
			ui.Text(c, r.Artists).FontSize(fz(11)).TextColor(s.sub).SingleLine().Ellipsis("…")
		})
		if row.Hovered() {
			del := ui.ButtonBase(c).Size(24, 24).Radius(12).Center().Transition(hoverFade)
			del.Children(func() {
				ui.Icon(c, Icons["clear"]).FontSize(fz(12)).TextColor(s.icon).AlignSelf(ui.Center)
			})
			del.Tooltip("从播放列表移除")
			if del.Clicked() {
				a.PL.RemoveAt(i)
			}
		}
	})
	if isCur {
		row.Background(s.tint)
	} else if row.Hovered() {
		row.Background(s.hover)
	}
	if row.Clicked() {
		a.PL.Jump(i)
	}
}
