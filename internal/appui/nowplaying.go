package appui

import (
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// 正在播放页的布局常量：内容层与歌词列宽都从它们推导，不能各写一份
// （逐字高亮要先按歌词列宽量行高，宽度对不上换行的行就会错位）。
const (
	npPadY  = 56.0  // 内容层上下内边距
	npPadX  = 56.0  // 内容层左右内边距
	npGap   = 56.0  // 歌词列与封面列的间距
	npSideW = 340.0 // 左侧封面/信息列宽

	// 歌词列首尾留白：主项目 .np-lyrics 用 38% 的伪元素留白，MyGo 没有
	// mask-image，改用等价的上下 padding。留白随窗口高度缩放，但夹在
	// 72~160px：小窗口不把列表挤没，大窗口也不会让首行飘到画面外。
	npPadMin    = 72.0
	npPadMax    = 160.0
	npPadFactor = 0.22

	// 非当前句的模糊（对齐主项目 .np-ly-line 的 blur(1.6px)）。模糊值按帧
	// 插值，换句与进出静默期时才是淡入淡出，而不是硬切。
	npLyricBlur    = 1.6
	npLyricBlurDur = 260 * time.Millisecond
)

var debugBlurHook func(index int, isCur bool, v float32)

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

// npCoverSize is the record sleeve's edge, in DIPs. It fills the side
// column unless the window is too short, in which case it shrinks so the
// metadata and the quality row below still fit on screen.
func npCoverSize(c *ui.Context) float32 {
	_, h := c.Size()
	// npPadY at the top, 24 at the bottom, and the metadata block below
	// the sleeve (title, artist, quality row, gaps) which must stay visible.
	avail := h - npPadY - 24 - 150
	size := float32(npSideW)
	if avail > 0 && avail < size {
		size = avail
	}
	if size < 180 {
		size = 180
	}
	return size
}

// nowPlaying 是正在播放页：绝对定位覆盖内容区（播放条仍在其下方可见）。
//
// 左侧是唱片封套与元信息（歌名、歌手、音质、收藏、更多），右侧默认是歌词；
// 点顶上的「队列」标签，队列会从右缘滑入并取代歌词，点「歌词」再淡回来。
// 播放控制不在这里重复一份，仍由页面下方的播放条负责。
// 背景由封面取色驱动，而不是纯粹压黑一张模糊图。
//
// 动效（对齐主项目 .np）：整体上滑 + 淡入，.34s cubic-bezier(.32,.72,.24,1)；
// 关闭时同样滑回下方（MyGo 的 Exit 会给退场元素留一份副本，兄弟不为它留位）。
func (a *App) nowPlaying(c *ui.Context, t *ui.Theme) {
	cur, hasCur := a.PL.Current()
	a.resetLyricFollowOnSongChange(cur.Mid)
	a.advanceLyricFollow(c)
	var coverURLs []string
	var blurURLs []string
	if hasCur {
		coverURLs = a.songCoverURLs(cur, npCoverSizes)
		blurURLs = a.songCoverURLs(cur, npBlurSizes)
	}

	np := ui.Box(c).Key("nowplaying").Absolute().Fill().Background(npBase).
		Transition(ui.ElementTransition{
			Duration: 340 * time.Millisecond,
			Ease:     easePanel,
			Enter:    &ui.Motion{Y: 56, Opacity: 0},
			Exit:     &ui.Motion{Y: 56, Opacity: 0},
		})
	np.Children(func() {
		a.npBackdrop(c, coverURLs, blurURLs)

		// 内容层：必须也是绝对定位。
		//
		// 根因（用户报的「正在播放页封面渲染透明化」）：MyGo 里 Absolute 子元素
		// 绘制在流式子元素【之上】（ui/paint.go：先画 flow、再画 absolute，与
		// CSS 定位元素的层叠关系一致）。背景层只做绝对定位的话，模糊封面图与
		// 黑色遮罩就会压在队列和封面上——封面看着像掺进背景里、半透明。
		// 让内容层同样绝对定位，绘制顺序就回到「底图 → 遮罩 → 内容」；
		// 关闭按钮建在最后，仍然最上。
		a.npSplit(c, t, cur, hasCur)

		// 整页可拖窗口：正在播放页盖住了顶栏，没有这一层用户就无法拖动
		// 无边框窗口。标签与关闭键晚建，盖在拖拽层之上。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(48).DragWindow()
		a.npTopControls(c)
	})
}

// npSplit is the quiet layout: the sleeve and its metadata on the left,
// the lyrics (or the queue, once shown) on the right. The cover column
// mirrors a record player's deck while the right pane is the working area.
func (a *App) npSplit(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) {
	ui.Row(c).Absolute().Fill().Padding(npPadY, npPadX, 24, npPadX).
		Gap(npGap).AlignItems(ui.Stretch).Children(func() {
		a.npLeft(c, t, cur, hasCur)
		a.npRightPane(c, t)
	})
}

// npRightPane holds the single right-hand surface. Lyrics are the default;
// the queue takes their place when the user opens it, and the two crossfade
// instead of swapping abruptly. Radio has no editable queue, so it always
// falls back to lyrics.
func (a *App) npRightPane(c *ui.Context, t *ui.Theme) {
	queue := a.npQueue && !a.PL.RadioActive()
	ui.Box(c).Grow(1).Basis(0).MinWidth(0).Clip().Children(func() {
		if queue {
			a.npQueuePane(c, t)
		} else {
			a.npLyricPane(c, t)
		}
	})
}

// npLyricPane is the right-hand reading column: a small header naming the
// source, then the lyrics themselves. It fades back in when the queue closes.
func (a *App) npLyricPane(c *ui.Context, t *ui.Theme) {
	_ = t
	pane := ui.Column(c).Key("np-lyrics").Grow(1).Basis(0).MinWidth(0).
		Transition(ui.ElementTransition{
			Duration: 260 * time.Millisecond,
			Ease:     easePage,
			Enter:    &ui.Motion{Opacity: 0},
			Exit:     &ui.Motion{Opacity: 0},
		})
	pane.Children(func() {
		ui.Row(c).FillWidth().Height(26).AlignItems(ui.End).Children(func() {
			ui.Text(c, "歌词").FontSize(fz(10.5)).FontWeight(800).
				LetterSpacing(1.4).TextColor(ui.RGBA(255, 255, 255, 0.42))
			ui.Spacer(c)
			if len(a.PL.QrcLines()) > 0 {
				source := "歌词源 · QQ 音乐 QRC"
				if a.PL.LyricFormat() == "ttml" {
					source = "歌词源 · TTML"
				}
				ui.Text(c, source).FontSize(fz(10.5)).
					TextColor(ui.RGBA(255, 255, 255, 0.42)).SingleLine()
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(ui.RGBA(255, 255, 255, 0.08))
		a.lyricColumn(c, t)
	})
}

// npQueuePane is the queue in the lyric area: it slides in from the right
// edge when the user switches to it. Its header, list and rows are shared
// with the docked panel outside now-playing.
func (a *App) npQueuePane(c *ui.Context, t *ui.Theme) {
	s := a.queuePalette(t, true)
	pane := ui.Column(c).Key("np-queue").Grow(1).Basis(0).FillWidth().MinWidth(0).
		Transition(ui.ElementTransition{
			Duration: 340 * time.Millisecond,
			Ease:     easePanel,
			Enter:    &ui.Motion{X: 56, Opacity: 0},
			Exit:     &ui.Motion{X: 56, Opacity: 0},
		})
	pane.Children(func() {
		// 播放页上用顶部标签切回歌词，头行不再重复一个「收起」。
		a.queueHead(c, s, nil)
		a.queueBody(c, s)
	})
}

// npTopControls keeps the pane tabs and the close button above the layout.
// Lyrics are the default surface; the 队列 tab slides the queue in from the
// right, and 歌词 fades the lyrics back in.
func (a *App) npTopControls(c *ui.Context) {
	queue := a.npQueue && !a.PL.RadioActive()
	tabs := ui.Row(c).Absolute().Top(16).Left(npPadX).
		Gap(6).AlignItems(ui.Center).Padding(3, 3).Radius(999).
		Background(ui.RGBA(10, 12, 18, 0.32))
	tabs.Children(func() {
		a.npTab(c, "歌词", "显示歌词", !queue, func() {
			a.npQueue = false
		})
		if !a.PL.RadioActive() {
			a.npTab(c, "队列", "显示播放队列", queue, func() {
				a.npQueue = true
				a.npMoreOpen = false
				a.npQInfoOpen = false
			})
		}
	})

	closeBtn := ui.Box(c).Absolute().Top(14).Right(14).Size(32, 32).Radius(999).
		Background(ui.RGBA(255, 255, 255, 0.10)).Center().Transition(hoverFade)
	closeBtn.Children(func() {
		ui.Icon(c, Icons["clear"]).FontSize(fz(15)).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
	})
	if closeBtn.Hovered() {
		closeBtn.Background(ui.RGBA(255, 255, 255, 0.20))
	}
	closeBtn.Tooltip("关闭正在播放页")
	closeBtn.Label("关闭正在播放页")
	if closeBtn.Clicked() {
		a.npOpen = false
		a.npMoreOpen = false
		a.npQInfoOpen = false
	}
}

// npTab is one pill of the top tab strip. Accessibility labels describe the
// action ("显示或隐藏歌词") while the visible text stays short.
func (a *App) npTab(c *ui.Context, text, label string, active bool, run func()) {
	b := ui.ButtonBase(c).Padding(6, 13).Radius(999).Label(label).Transition(hoverFade)
	color := ui.RGBA(255, 255, 255, 0.58)
	switch {
	case active:
		b.Background(ui.RGBA(255, 255, 255, 0.16))
		color = ui.Hex("#ffffff")
	case b.Hovered():
		b.Background(ui.RGBA(255, 255, 255, 0.07))
		color = ui.RGBA(255, 255, 255, 0.82)
	}
	b.Tooltip(label)
	b.Children(func() {
		ui.Text(c, text).FontSize(fz(12.5)).FontWeight(650).TextColor(color)
	})
	if b.Clicked() {
		run()
	}
}

// npBackdrop 画正在播放页的背景：封面取色的深色渐变 + 轻度模糊的封面
// 本身，再加一层压暗遮罩。
//
// 参考实现（mygo-cloudmusic）的底不是一张模糊封面，而是封面主色压深后的
// 渐变：颜色一致、边缘干净，白字永远压得住。这里没有取到色时退回原来的
// 模糊封面方案，所以冷启动也不会空成一块纯黑。
//
// 换曲时按封面地址重置淡入时钟：旧图的模糊底会消失、新图要等下载，中间空出
// 的那一拍就是主项目注释里说的「切歌闪一下」——淡入把它盖过去。这里只淡入
// 装饰层、不动内容层，所以哪怕动画没跑完，封面与歌词也始终是实心的。
func (a *App) npBackdrop(c *ui.Context, covers, blurCovers []string) {
	top, foot, tinted := npBackdropColors(a)
	if tinted {
		ui.Box(c).Absolute().Fill().Gradient(top, foot, 180)
	} else {
		ui.Box(c).Absolute().Fill().Gradient(npBase, ui.Hex("#05060b"), 180)
	}

	coverKey := ""
	if len(blurCovers) > 0 {
		coverKey = blurCovers[0]
	}
	if blur := a.Covers.GetBestBlur(blurCovers, a.invalidate); blur != nil {
		if a.npBlurKey != coverKey {
			a.npBlurKey, a.npBlurAt = coverKey, c.Now()
		}
		img := ui.Image(c, blur).Absolute().Fill().Fit(ui.Cover)
		styleFade(c, motion{
			start: a.npBlurAt,
			dur:   420 * time.Millisecond,
			ease:  ui.EaseOut,
		}, img, 0, npBlurOpacity*0.62)
	}

	// 压暗遮罩：上浅下深（主项目 .np-scrim 的 180° 渐变）。注意角度是 180
	// 而不是 90——MyGo 与 CSS 同口径，90° 是「向右」，那样渐变会横过来。
	ui.Box(c).Absolute().Fill().Gradient(npScrimTop, npScrimBottom, 180)

	// 左缘再压一层：封面与歌词都是纯白文字，背景却是任意色相的封面。
	// 向右 60% 收干净，保证左侧文字的对比度。
	ui.Box(c).Absolute().Fill().
		Gradient(ui.RGBA(6, 8, 13, 0.62), ui.RGBA(6, 8, 13, 0), 90)

	// 封面色的薄雾：让整页不只是黑，也不会盖掉模糊底。
	if tinted {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(360).
			Gradient(top.Alpha(0.34), ui.RGBA(0, 0, 0, 0), 180)
	}
}

// npBackdropColors derives the backdrop's two stops from the cover tint.
// Like NetEase's player, the hue is kept while the value is pushed down so
// that white text reads over any sleeve.
func npBackdropColors(a *App) (top, foot ui.Color, ok bool) {
	accent, _, tinted := a.tint.get()
	if !tinted {
		return ui.Hex("#171a22"), ui.Hex("#080a10"), false
	}
	deep := ui.Hex("#07080d")
	top = accent.Mix(deep, 0.74)
	foot = accent.Mix(deep, 0.90)
	return top, foot, true
}

// npLeft is the left-hand deck: sleeve, metadata, then the quality pill,
// favourite and more button in one row. Everything is left-aligned with
// the sleeve. Playback is not repeated here: the player bar under the page
// already owns progress, transport and volume, and two sets of the same
// controls only compete for the eye (and for tests' labels).
func (a *App) npLeft(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) {
	_ = t
	size := npCoverSize(c)
	ui.Column(c).Width(npSideW).Gap(18).AlignItems(ui.Start).Justify(ui.Center).
		Children(func() {
			a.npCover(c, cur, hasCur, size)

			title := "未在播放"
			artist := "选一首歌，让封面先亮起来"
			album := ""
			if hasCur {
				title = cur.DisplayName()
				artist = cur.Artists
				album = cur.Album
			}
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Text(c, title).FontSize(fz(26)).FontWeight(800).
					LetterSpacing(-0.4).TextColor(ui.Hex("#ffffff")).
					SingleLine().Ellipsis("…")
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					sub := artist
					if album != "" && hasCur {
						sub += " · " + album
					}
					ui.Text(c, sub).FontSize(fz(13)).TextColor(ui.RGBA(255, 255, 255, 0.62)).
						SingleLine().Ellipsis("…").Grow(1)
				})
			})

			// 音质 + 收藏 + 更多。浮层锚点跟按钮走，所以这一行必须常驻
			//（无在播曲时两个浮层显示各自的空态文案）。
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				a.qualityPillNP(c)
				ui.Spacer(c)
				if hasCur {
					if cur.CloudFileID == "" {
						a.npLove(c, cur)
					}
				}
				more := a.npIconButton(c, "more", "更多操作", 17)
				if more.Clicked() {
					a.npMoreOpen = !a.npMoreOpen
					a.npQInfoOpen = false // 同一锚区只开一个（主项目 closeQMenu 的互斥）
				}
				a.npMoreMenu(c, more)
			})
		})
}

// npCover is the sleeve: a rounded square over the tinted backdrop, with a
// floating shadow. A missing cover keeps the layout stable with a note.
func (a *App) npCover(c *ui.Context, cur player.Song, hasCur bool, size float32) {
	var art *ui.Bitmap
	if hasCur {
		art = a.Covers.GetBest(a.songCoverURLs(cur, npCoverSizes), a.invalidate)
	}
	if art != nil {
		ui.Image(c, art).Size(size, size).Fit(ui.Cover).Radius(18).
			Shadow(0, 24, 70, 0, ui.RGBA(0, 0, 0, 0.52))
		return
	}
	ui.Box(c).Size(size, size).Radius(18).
		Background(ui.RGBA(255, 255, 255, 0.07)).
		Border(1, ui.RGBA(255, 255, 255, 0.10)).Center().Children(func() {
		ui.Icon(c, Icons["note"]).FontSize(fz(size * 0.22)).
			TextColor(ui.RGBA(255, 255, 255, 0.35)).AlignSelf(ui.Center)
	})
}

// npLove is the sleeve's favourite control, drawn for the dark page rather
// than the light player bar.
func (a *App) npLove(c *ui.Context, cur player.Song) {
	loved := a.PL.IsLoved(cur.Mid)
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	hover := b.Hovered()
	b.Children(func() {
		name := "heart"
		col := ui.RGBA(255, 255, 255, 0.72)
		if loved {
			name, col = "heartFill", ui.Hex("#ef666f")
		} else if hover {
			col = ui.Hex("#ef666f")
		}
		ui.Icon(c, Icons[name]).FontSize(fz(17)).TextColor(col).AlignSelf(ui.Center).
			Label(loveLabel(loved))
	})
	if hover {
		b.Background(ui.RGBA(255, 255, 255, 0.10))
	}
	b.Tooltip(loveLabel(loved))
	if b.Clicked() {
		a.PL.ToggleLove(cur)
	}
}

// qualityPillNP 在深色背景上的音质胶囊。它是【只读】的音频流参数入口：
// 点开的是流信息浮窗（编码/采样率/采样精度/码率/声道 + 档位徽标），不是
// 音质切换菜单——切档在播放条胶囊上（qualityMenu），两者状态互相独立。
func (a *App) qualityPillNP(c *ui.Context) {
	pill := ui.ButtonBase(c).Padding(3, 10).Radius(999).Border(1, ui.RGBA(255, 255, 255, 0.30)).
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

// npIconButton is the dark-page counterpart of iconBtn, with a label and a
// tooltip so pure icons stay addressable by screen readers and tests.
func (a *App) npIconButton(c *ui.Context, icon, label string, size float32) ui.Element {
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Transition(hoverFade)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(size)).
			TextColor(ui.RGBA(255, 255, 255, 0.78)).AlignSelf(ui.Center)
	})
	if b.Hovered() {
		b.Background(ui.RGBA(255, 255, 255, 0.10))
	}
	b.Tooltip(label)
	b.Label(label)
	return b
}

// lyricColumn 左侧歌词列（行级歌词 + 翻译 + 跟随滚动）。
func (a *App) lyricColumn(c *ui.Context, t *ui.Theme) {
	lines, state := a.PL.LyricLines()
	cur := a.PL.LyricIndex(a.PL.Position())

	if state == "ok" && cur >= 0 && cur != a.lastLyricLine {
		a.lastLyricLine = cur
		// 用户滚动后的静默期里只记当前句，不把列表拽回去。
		if !a.lyricFollowPaused(c.Now()) {
			a.followLyricLine(cur)
		}
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
			// 首尾 padding 负责留白；不再叠加 Absolute 渐变——渐变只要横跨
			// 内容层就会在右侧模糊底上切出硬边（用户反馈过这条异常分界）。
			// 列表自身负责裁切滚动内容，背景保持原本连续的模糊渐变。
			a.lyricList(c, len(lines), cur, func(i int) {
				a.lyricColumnLine(c, lines[i], i)
			})
		}
	})
}

// lyricList 是正在播放页三种歌词布局共用的列表：接管滚动状态（跟随换行由
// lyricfollow.go 按帧推进偏移），并记下上一帧的视口，供居中目标计算。
// cur 是本帧的当前句；若它没被建出来（跳转太远或首次布局），退回瞬间居中。
func (a *App) lyricList(c *ui.Context, n, cur int, row func(int)) ui.Element {
	a.npList.Key = nil
	pad := npListPadding(c)
	el := ui.List(c, &a.npList, n, row).Grow(1).Padding(pad, 0, pad, 0).
		TrackScroll(&a.npScroll)
	// 滚轮（含触控板）在这里先记一笔：事件返回 false，列表照常滚。拖动
	// 滚动条不经过元素事件，靠 advanceLyricFollow 的偏移差兜住。
	el.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputScroll {
			a.pauseLyricFollow(ev.Time)
		}
		return false
	})
	a.npListRect = el.LayoutBounds()
	if f := &a.npFollow; f.pending && f.line == cur {
		f.pending = false
		a.npList.ScrollTo(cur, ui.Center)
		// 这次居中是我们自己滚的，下一帧的偏移差不是用户滚动。
		a.npScrollAbsorb, a.npScrollAbsorbAt = true, c.Now()
		// 用户滚远后静默期结束：当前句可能根本不在已建的行里，只能瞬移
		// 过去。瞬移用一次淡入兜住，别让歌词硬切一下。
		if a.npResume {
			a.npResume, a.npSnapAt = false, c.Now()
		}
	}
	if !a.npSnapAt.IsZero() {
		k, moving := motion{start: a.npSnapAt, dur: 220 * time.Millisecond, ease: ui.EaseOut}.at(c.Now())
		el.Opacity(k)
		if moving {
			c.AnimationFrame()
		} else {
			a.npSnapAt = time.Time{}
		}
	}
	return el
}

// lyricLineBlur 是一行歌词的目标模糊量：当前句 0，其余句 npLyricBlur；
// 用户滚动后的静默期（lyricFollowPause）里一律 0，让正在找的那句保持可读。
// 真正的淡入淡出由调用方的 AnimateWith 按帧插值。
func (a *App) lyricLineBlur(isCur bool, now time.Time) float32 {
	if isCur || a.lyricFollowPaused(now) {
		return 0
	}
	return npLyricBlur
}

// lyricColumnLine 是歌词列表的一行（与列表容器分开命名，便于单独维护行样式）。
func (a *App) lyricColumnLine(c *ui.Context, line player.LyricLine, index int) {
	cur := a.PL.LyricIndex(a.PL.Position())
	isCur := index == cur
	scale := a.Conf.Float("Style.LyricScale", 100) / 100
	size := 18 * float32(scale)
	if isCur {
		size = 29 * float32(scale)
	}
	row := ui.Column(c).FillWidth().Padding(8, 4).Gap(5).Radius(10).Label(line.Text).
		Cursor(ui.CursorPointer).Transition(hoverFade)
	if isCur {
		a.followLyricRow(c, row, index)
	}
	// 行间过渡：非当前句压暗并模糊（对齐主项目 .np-ly-line 的 opacity .34 +
	// blur(1.6px)）。两个值都走 Animate 而不是硬切，「成为当前句 / 不再是
	// 当前句」这一跳因此是缓动的。
	target := float32(0.34)
	if isCur {
		target = 1
	}
	row.Opacity(row.Animate("lyric-line", target, 260*time.Millisecond)).
		TransformOrigin(0, 0.5)
	// 当前句清晰，其余句子模糊（对齐主项目 .np-ly-line 的 blur(1.6px)）。
	// 用户滚动歌词后的 3s 静默期里整片都不模糊——那会儿用户正在找、在
	// 读，糊掉只会帮倒忙。模糊值本身走缓动，换句与进出静默期都是淡入淡出。
	bv := row.AnimateWith("lyric-blur", a.lyricLineBlur(isCur, c.Now()), npLyricBlurDur, ui.EaseOut)
	if debugBlurHook != nil {
		debugBlurHook(index, isCur, bv)
	}
	row.Blur(bv)
	if !isCur {
		// The line shrinks a hair as it gives up the focus. The current line
		// springs back to full size, matching the reference's lyric motion.
		row.AnimatePose(ui.Pose{Scale: 0.985}, ui.Animation{Duration: 260 * time.Millisecond})
	} else {
		row.AnimatePose(ui.Pose{Scale: 1}, ui.Animation{Duration: 260 * time.Millisecond})
	}
	row.Children(func() {
		qrcLine, hasQRC := a.PL.QrcLineAt(index)
		switch {
		case hasQRC && qrcLine.HasRuby():
			// 日文 QRC 的振假名挂在词上：即使不是当前句也画在词上方，
			// 这是歌词正文的一部分，不受翻译/音译档位影响。
			a.karaokeRubyLine(c, qrcLine, isCur, size, npLyricWidth(c), ui.Start, func(fill float32) ui.Color {
				if !isCur {
					return ui.Hex("#ffffff")
				}
				return karaokeColor(fill)
			}, false)
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
			col := ui.RGBA(255, 255, 255, 0.72)
			if !isCur {
				col = ui.RGBA(255, 255, 255, 0.42)
			}
			// 翻译/音译共用这一行；长文本允许换行，列表会按实际行高重新居中。
			ui.Text(c, aux).Font(a.lyricFontFamily()).FontSize(fz(size * 0.6)).TextColor(col)
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
	a.karaokeLine(c, ql, size, ui.Start)
	return true
}

// ===== 播放队列面板 =====

// queuePalette 是队列面板的配色。
//
// 面板有两个落点：内容区右侧的停靠栏（正在播放页没开）和正在播放页上替代
// 歌词区的右栏。列表本体、拖拽换序、当前行高亮、悬停删除在两处必须一模一样，
// 不然两边迟早长出不一样的队列；能变的只有颜色与容器装饰，所以把颜色单独抽出来。
type queuePalette struct {
	title, sub, hover, bg, border, tint, accent, icon ui.Color
	// line/divider 只在正在播放页上用：停靠栏本来就贴着内容区边界，头行下再加
	// 一条横线显脏；播放页的右栏压在模糊封面上，需要一条线把头行和列表分开。
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
		bg: panelBg(t), border: t.Border, tint: a.rowTint(t),
		accent: t.Accent, icon: t.Text,
	}
}

// queueHeadH/queueRowH 是面板的固定行高：头行与单首歌的行。两处必须和
// queueHead/queueRow 里写的高度是同一个数。
const (
	queueHeadH = 44.0
	queueRowH  = 46.0
)

// queuePanel 是内容区右侧的停靠队列栏（正在播放页未打开时的形态）。
// 开关时从右缘外侧滑入/滑出：它是从内容区边上「抽出来」的一栏，位移要
// 和正在播放页右栏的队列一致，不能生硬地闪现。
func (a *App) queuePanel(c *ui.Context) {
	s := a.queuePalette(c.Theme(), false)
	panel := ui.Column(c).Key("queue-dock").Width(300).Background(s.bg).
		BorderWidth(1, 0, 0, 1).BorderColor(s.border).
		Transition(ui.ElementTransition{
			Duration: 340 * time.Millisecond,
			Ease:     easePanel,
			Enter:    &ui.Motion{X: 300, Opacity: 0},
			Exit:     &ui.Motion{X: 300, Opacity: 0},
		})
	panel.Children(func() {
		a.queueHead(c, s, func() {
			a.queueOpen = false
			a.Conf.Set("Window.QueueOpen", false)
		})
		a.queueBody(c, s)
	})
}

// queueHead 是面板头行：标题 + 清空 +（停靠栏才有的）收起。onClose 为 nil
// 时不画收起——正在播放页用顶部标签切回歌词。
func (a *App) queueHead(c *ui.Context, s queuePalette, onClose func()) {
	ui.Row(c).FillWidth().Height(queueHeadH).PaddingX(12).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Textf(c, "播放列表 · %d 首", a.PL.QueueLen()).FontSize(fz(13)).FontWeight(700).
			TextColor(s.title).SingleLine().Grow(1)
		a.queueIconBtn(c, s, "trash", "清空播放列表", func() { a.PL.ClearQueue() })
		if onClose != nil {
			a.queueIconBtn(c, s, "clear", "收起播放列表", onClose)
		}
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
	ui.List(c, &a.queueList, n, func(i int) {
		if row, ok := a.PL.QueueRow(i); ok {
			a.queueRow(c, s, row, i, cur.Mid)
		}
	}).Grow(1)
}

func (a *App) queueRow(c *ui.Context, s queuePalette, r player.QueueRowView, i int, curMid string) {
	isCur := r.Mid == curMid
	row := ui.Row(c).FillWidth().Height(queueRowH).PaddingX(10).Gap(10).AlignItems(ui.Center).Radius(8).
		Label(r.DisplayName()).
		Transition(hoverFade)
	row.Children(func() {
		ui.Box(c).Width(22).Center().Children(func() {
			if isCur && a.PL.Playing() {
				ui.Icon(c, Icons["note"]).FontSize(fz(13)).TextColor(s.accent).AlignSelf(ui.Center)
			} else {
				ui.Textf(c, "%d", i+1).FontSize(fz(11)).TextColor(s.sub).AlignSelf(ui.Center)
			}
		})
		if art := a.Covers.Get(a.queueRowCoverURL(r, 300), a.invalidate); art != nil {
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
