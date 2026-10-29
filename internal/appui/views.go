package appui

import (
	"strconv"
	"sync"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// update 把状态回填切回主线程（Win 未就绪时直接调用——测试环境用）。
func (a *App) update(fn func()) {
	if a.Win != nil {
		a.Win.Update(fn)
	} else {
		fn()
	}
}

// ===== 首页 =====

type homeState struct {
	loaded  bool
	loading bool
	err     string

	recs    []backend.SonglistSummary
	newsong []player.Song
	newsErr string

	ai       []backend.SonglistSummary
	aiPage   int
	aiMore   bool
	aiLoaded bool
	aiBusy   bool
	aiErr    string

	topGroups []backend.ToplistGroup
	topGroup  int
	topLoaded bool
	topBusy   bool
	topErr    string
}

func (a *App) homeView(c *ui.Context) {
	st := &a.home
	a.ensureHome()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(24, 28, 30, 28).Gap(28)).Children(func() {
		a.homeHeader(c)

		// 快速开始 + 新歌速递。宽屏双栏，窄屏自动换行。
		lead := ui.Row(c).FillWidth().Wrap().GapX(18).GapY(18).AlignItems(ui.Stretch)
		lead.Children(func() {
			a.homeRadioCard(c, st)
			a.homeNewSongs(c, st)
		})

		a.homeRecommendations(c, st)
		a.homeAI(c, st)
		a.homeToplists(c, st)
	})
}

// homeHeader 把首页从“后台仪表盘”拉回一张有日期的听歌桌面：
// 早上、下午与深夜各有自己的问候，右侧只留日期和星期，不放无意义的
// 统计数字。它是页面里唯一的大字号标题，下面各区围绕它降一级。
func (a *App) homeHeader(c *ui.Context) {
	t := c.Theme()
	now := time.Now()
	name := ""
	if a.PL.LoggedIn() {
		if me := a.PL.Me(); me.Name != "" {
			name = "，" + me.Name
		}
	}
	greeting := homeGreeting(now.Hour()) + name
	weekday := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[int(now.Weekday())]

	ui.Row(c).FillWidth().AlignItems(ui.End).Gap(18).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, "ASTRA / SIGNAL DESK").Font("monospace").FontSize(fz(9.5)).
					FontWeight(700).TextColor(t.Accent)
				ui.Box(c).Width(24).Height(1).Background(t.Accent.Alpha(0.55))
				ui.Text(c, "从今天的推荐开始").FontSize(fz(10.5)).TextColor(t.TextMuted)
			})
			ui.Text(c, greeting).FontSize(fz(30)).FontWeight(800).SingleLine().Ellipsis("…")
		})
		ui.Column(c).AlignItems(ui.End).Gap(1).Children(func() {
			ui.Text(c, now.Format("01 / 02")).Font("monospace").FontSize(fz(17)).FontWeight(800)
			ui.Text(c, weekday).FontSize(fz(10.5)).TextColor(t.TextMuted)
		})
	})
}

func homeGreeting(h int) string {
	switch {
	case h < 5:
		return "夜深了"
	case h < 11:
		return "早上好"
	case h < 13:
		return "中午好"
	case h < 18:
		return "下午好"
	case h < 23:
		return "晚上好"
	default:
		return "夜深了"
	}
}

// homeRadioCard 是首页的无限电台入口：未开始时负责开台，播放中则直接
// 展示当前曲并给出播放/暂停、下一首、收藏。进入详情页只恢复主舞台，
// 不会打断队列——这正是电台的「退出页面保留」语义。
func (a *App) homeRadioCard(c *ui.Context, st *homeState) {
	cur, hasCur := a.PL.Current()
	active := a.PL.RadioActive()
	busy := a.PL.RadioLoading()
	artURL := ""
	if active && hasCur {
		artURL = coverURL(cur.AlbumPmid, cur.AlbumMid, 300)
	} else if len(st.recs) > 0 {
		artURL = st.recs[0].Picurl
	}
	art := a.Covers.Get(artURL, a.invalidate)

	heroBg := ui.Hex("#151b18")
	heroInk := ui.Hex("#f7f1e4")
	heroMuted := ui.RGBA(247, 241, 228, 0.64)
	heroLine := ui.RGBA(247, 241, 228, 0.16)

	card := ui.Row(c).Grow(2).Basis(500).MinWidth(440).Height(236).Padding(22, 24).
		Gap(22).Radius(16).Background(heroBg).Clip().AlignItems(ui.Center)
	card.Draw(func(p *ui.Painter, r ui.Rect) {
		// Orbital geometry gives the hero a Quaver/Astra signature without
		// borrowing the traffic-light, glass and floating-pill language of
		// the reference desktop shell.
		cx, cy := r.X+r.W*0.8, r.Y+r.H*0.5
		for _, rad := range []float32{58, 88, 118} {
			var orbit ui.Path
			orbit.Circle(cx, cy, rad)
			p.StrokePath(&orbit, 1, ui.RGBA(25, 194, 216, 0.13))
		}
		p.Line(cx-96, cy, cx+96, cy, 1, ui.RGBA(247, 241, 228, 0.07))
		p.Line(cx, cy-96, cx, cy+96, 1, ui.RGBA(247, 241, 228, 0.07))
	})
	card.Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(220).Gap(7).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, "∞").FontSize(fz(16)).FontWeight(800).TextColor(ui.Hex("#19c2d8"))
				ui.Text(c, "无限电台").FontSize(fz(11.5)).FontWeight(700).TextColor(ui.Hex("#19c2d8"))
				ui.Text(c, "PICKED FOR YOU").Font("monospace").FontSize(fz(9)).
					TextColor(heroMuted)
				ui.Spacer(c)
				if active && busy {
					ui.Text(c, "正在续歌…").FontSize(fz(10.5)).TextColor(heroMuted)
				}
			})
			title := "猜你喜欢"
			sub := "按你的口味持续播放。没有列表尽头，只有下一首。"
			if active && hasCur {
				title = cur.DisplayName()
				sub = cur.Artists
				if cur.Album != "" {
					sub += " · " + cur.Album
				}
			} else if busy {
				title = "正在为你取歌…"
				sub = "首轮约 5 首，播到轮末会自动续歌"
			} else if e := a.PL.RadioError(); e != "" {
				sub = e
			}
			ui.Text(c, title).FontSize(fz(29)).FontWeight(800).TextColor(heroInk).
				SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(fz(12.5)).TextColor(heroMuted).SingleLine().Ellipsis("…")
			ui.Spacer(c)
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				if !active {
					heroBtn(c, "开始电台", true, heroInk, heroBg, heroLine, func() { a.startRadioFromUI(true) })
				} else {
					playLabel := "播放"
					if a.PL.Playing() {
						playLabel = "暂停"
					}
					heroBtn(c, playLabel, true, heroInk, heroBg, heroLine, func() { a.PL.PlayOrPause() })
					heroBtn(c, "下一首", false, heroInk, heroBg, heroLine, func() { a.PL.Next(false) })
					if hasCur {
						loveLabel := "收藏"
						if a.PL.IsLoved(cur.Mid) {
							loveLabel = "已收藏"
						}
						heroBtn(c, loveLabel, false, heroInk, heroBg, heroLine, func() { a.PL.ToggleLove(cur) })
					}
				}
				heroBtn(c, "打开电台", false, heroInk, heroBg, heroLine, func() { a.Router.Push("/guess") })
			})
		})

		// The cover is a physical sleeve rather than a dashboard thumbnail.
		// Two offset cards behind it imply a stack without making the page
		// look like an Apple Music shelf.
		visual := ui.Box(c).Width(212).Height(188).Shrink(0)
		visual.Children(func() {
			ui.Text(c, time.Now().Format("02")).Absolute().Right(2).Bottom(-42).
				Font("monospace").FontSize(fz(126)).FontWeight(800).
				TextColor(ui.RGBA(247, 241, 228, 0.045))
			ui.Box(c).Absolute().Top(28).Right(32).Size(142, 142).Radius(12).
				Background(ui.RGBA(25, 194, 216, 0.13)).Rotate(5)
			ui.Box(c).Absolute().Top(24).Right(48).Size(142, 142).Radius(12).
				Background(ui.RGBA(247, 241, 228, 0.08)).Rotate(-3)
			box := ui.Box(c).Absolute().Top(20).Right(38).Size(146, 146).Radius(12).
				Clip().Background(ui.RGBA(247, 241, 228, 0.1)).
				Shadow(0, 14, 38, 0, ui.RGBA(0, 0, 0, 0.38))
			box.Children(func() {
				if art != nil {
					ui.Image(c, art).Fill().Fit(ui.Cover)
				} else {
					ui.Box(c).Fill().Center().Children(func() {
						ui.Text(c, "∞").FontSize(fz(64)).FontWeight(800).TextColor(ui.Hex("#19c2d8"))
					})
				}
			})
			status := "READY / 01"
			if active && hasCur {
				status = "ON AIR / 01"
			}
			ui.Text(c, status).Absolute().Left(2).Bottom(4).Font("monospace").
				FontSize(fz(9)).FontWeight(700).TextColor(heroMuted)
		})
	})
}

func (a *App) homeNewSongs(c *ui.Context, st *homeState) {
	t := c.Theme()
	panel := ui.Column(c).Grow(1).Basis(370).MinWidth(340).Height(236).
		Padding(14).Radius(16).Background(t.Surface).Border(1, t.Border)
	panel.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(0).Children(func() {
				ui.Text(c, "新歌速递").FontSize(fz(15)).FontWeight(800)
				ui.Text(c, "NEW RELEASES / DAILY").Font("monospace").FontSize(fz(8.5)).
					TextColor(t.TextMuted)
			})
			if len(st.newsong) > 0 {
				linkButton(c, "播放全部", func() { a.playListNow(st.newsong, 0) })
			}
		})
		ui.Box(c).FillWidth().Height(1).Margin(8, 0, 4, 0).Background(t.Border.Alpha(0.7))
		if st.loading && len(st.newsong) == 0 {
			ui.Column(c).FillWidth().Grow(1).Center().Children(func() { ui.Spinner(c) })
			return
		}
		if len(st.newsong) == 0 {
			msg := st.newsErr
			if msg == "" {
				msg = "暂时没有新歌"
			}
			ui.Column(c).FillWidth().Grow(1).Center().Children(func() {
				ui.Text(c, msg).FontSize(fz(12)).TextColor(t.TextMuted)
			})
			return
		}
		for i := 0; i < len(st.newsong) && i < 5; i++ {
			a.compactSongRow(c, st.newsong, i)
		}
	})
}

// heroBtn is the in-hero action style: one warm paper button, the rest
// quiet outlines. It deliberately avoids the bright accent fill used by
// primary buttons elsewhere, so the radio's cover remains the loudest thing
// in the card.
func heroBtn(c *ui.Context, label string, primary bool, ink, ground, line ui.Color, onClick func()) {
	b := ui.ButtonBase(c).Height(32).Padding(0, 13).Radius(8).Center().
		Border(1, line).Transition(hoverFade)
	text := ink
	if primary {
		b.Background(ink).Border(0, line)
		text = ground
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(fz(12)).FontWeight(700).TextColor(text)
	})
	if !primary && b.Hovered() {
		b.Background(ui.RGBA(247, 241, 228, 0.1))
	}
	if b.Clicked() {
		onClick()
	}
}

func (a *App) homeRecommendations(c *ui.Context, st *homeState) {
	if st.loading && len(st.recs) == 0 {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			sectionHeader(c, "推荐歌单", nil)
			ui.Row(c).FillWidth().Center().Padding(28).Children(func() { ui.Spinner(c) })
		})
		return
	}
	if len(st.recs) == 0 {
		if st.err != "" {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				sectionHeader(c, "推荐歌单", nil)
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Text(c, st.err).FontSize(fz(12.5)).TextColor(c.Theme().TextMuted).Grow(1)
					plainBtn(c, "重试", func() { st.loaded, st.loading = false, false })
				})
			})
		}
		return
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		sectionHeader(c, "推荐歌单", func() {
			linkButton(c, "刷新", func() {
				st.loaded, st.loading = false, false
			})
		})
		a.playlistGrid(c, st.recs)
	})
}

func (a *App) homeAI(c *ui.Context, st *homeState) {
	if st.aiErr != "" {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			sectionHeader(c, "AI 歌单", func() {
				linkButton(c, "重试", func() { a.loadHomeAI(1) })
			})
			ui.Text(c, st.aiErr).FontSize(fz(12.5)).TextColor(c.Theme().TextMuted)
		})
		return
	}
	if !st.aiLoaded && !st.aiBusy {
		return
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		sectionHeader(c, "AI 歌单", func() {
			label := "换一批"
			if st.aiBusy {
				label = "生成中…"
			}
			if !st.aiBusy {
				linkButton(c, label, func() {
					page := st.aiPage + 1
					if !st.aiMore {
						page = 1
					}
					a.loadHomeAI(page)
				})
			}
		})
		if st.aiBusy && len(st.ai) == 0 {
			ui.Row(c).FillWidth().Center().Padding(28).Children(func() { ui.Spinner(c) })
			return
		}
		if len(st.ai) == 0 {
			ui.Text(c, "AI 暂时没有生成内容").FontSize(fz(12.5)).TextColor(c.Theme().TextMuted).Padding(16)
			return
		}
		a.playlistGrid(c, st.ai)
	})
}

func (a *App) homeToplists(c *ui.Context, st *homeState) {
	if st.topErr != "" {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			sectionHeader(c, "排行榜", func() {
				linkButton(c, "重试", func() {
					st.topErr = ""
					st.loaded, st.loading = false, false
				})
			})
			ui.Text(c, st.topErr).FontSize(fz(12.5)).TextColor(c.Theme().TextMuted)
		})
		return
	}
	if st.topBusy && len(st.topGroups) == 0 {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			sectionHeader(c, "排行榜", nil)
			ui.Row(c).FillWidth().Center().Padding(28).Children(func() { ui.Spinner(c) })
		})
		return
	}
	if len(st.topGroups) == 0 {
		return
	}
	if st.topGroup < 0 || st.topGroup >= len(st.topGroups) {
		st.topGroup = 0
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		sectionHeader(c, "排行榜", func() {
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for i, g := range st.topGroups {
					idx := i
					label := g.Name
					if label == "" {
						label = "分类"
					}
					b := ui.ButtonBase(c).Padding(4, 10).Radius(999)
					b.Children(func() {
						ui.Text(c, label).FontSize(fz(11.5))
					})
					if i == st.topGroup {
						b.Background(c.Theme().Accent.Alpha(0.14))
					} else if b.Hovered() {
						b.Background(c.Theme().SurfaceHover)
					}
					if b.Clicked() {
						st.topGroup = idx
					}
				}
			})
		})
		a.toplistGrid(c, st.topGroups[st.topGroup].Toplists)
	})
}

func (a *App) ensureHome() {
	st := &a.home
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	st.err = ""
	st.newsErr = ""
	st.aiBusy = true
	st.topBusy = true

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		var recs backend.RecommendSonglists
		var err error
		a.withAPILimit(func() { recs, err = a.API.RecommendSonglists(1, 13) })
		a.update(func() {
			if err != nil {
				st.err = err.Error()
				return
			}
			st.err = ""
			st.recs = recs.Songlists
		})
	}()
	go func() {
		defer wg.Done()
		var newest struct {
			Songs []backend.Song `json:"songs"`
		}
		var err error
		a.withAPILimit(func() { newest, err = a.API.RecommendNewsong() })
		a.update(func() {
			if err != nil {
				st.newsErr = err.Error()
				return
			}
			st.newsErr = ""
			st.newsong = toPlayerSongs(newest.Songs)
		})
	}()
	go func() {
		defer wg.Done()
		var ai backend.AISonglists
		var err error
		a.withAPILimit(func() { ai, err = a.API.RecommendAISonglists(1, 8) })
		a.update(func() {
			st.aiLoaded = true
			st.aiBusy = false
			st.aiPage = 1
			if err != nil {
				st.aiErr = err.Error()
				return
			}
			st.aiErr = ""
			st.ai = ai.Songlists
			st.aiMore = ai.HasMore
		})
	}()
	go func() {
		defer wg.Done()
		var tops backend.ToplistCategories
		var err error
		a.withAPILimit(func() { tops, err = a.API.ToplistCategories() })
		a.update(func() {
			st.topLoaded = true
			st.topBusy = false
			if err != nil {
				st.topErr = err.Error()
				return
			}
			st.topErr = ""
			st.topGroups = tops.Groups
		})
	}()
	go func() {
		wg.Wait()
		a.update(func() {
			st.loading = false
			st.loaded = true
		})
	}()
}

func (a *App) loadHomeAI(page int) {
	st := &a.home
	if st.aiBusy {
		return
	}
	st.aiBusy = true
	st.aiErr = ""
	go func() {
		var res backend.AISonglists
		var err error
		a.withAPILimit(func() { res, err = a.API.RecommendAISonglists(page, 8) })
		a.update(func() {
			st.aiBusy = false
			if err != nil {
				st.aiErr = err.Error()
				return
			}
			st.ai = res.Songlists
			st.aiPage = page
			st.aiMore = res.HasMore
		})
	}()
}

// startRadioFromUI 统一处理首页与电台页的开台入口：收起可编辑队列，
// 开台本身由 player 维护，离开页面不会停。
func (a *App) startRadioFromUI(autoplay bool) {
	a.queueOpen = false
	a.Conf.Set("Window.QueueOpen", false)
	a.PL.StartRadio(autoplay)
}

// compactSongRow 是新歌速递的紧凑行（无封面）。
func (a *App) compactSongRow(c *ui.Context, songs []player.Song, i int) {
	t := c.Theme()
	s := songs[i]
	row := ui.Row(c).FillWidth().Height(34).PaddingX(4).Gap(10).AlignItems(ui.Center).Radius(7).
		Transition(hoverFade)
	row.Children(func() {
		col := t.TextMuted
		if i == 0 {
			col = t.Accent
		}
		ui.Textf(c, "%02d", i+1).Font("monospace").FontSize(fz(10)).
			FontWeight(700).TextColor(col).Width(22)
		ui.Text(c, s.DisplayName()).FontSize(fz(12.5)).FontWeight(600).
			SingleLine().Ellipsis("…").Grow(1)
		ui.Text(c, s.Artists).FontSize(fz(10.5)).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Width(108).TextAlign(ui.End)
	})
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.DoubleClicked() {
		a.playListNow(songs, i)
	}
}

func (a *App) openPlaylistAndPlay(id int64) {
	go func() {
		var d backend.SonglistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.SonglistDetail(id, 1, 100) })
		a.update(func() {
			if err != nil {
				return
			}
			a.playListNow(toPlayerSongs(d.Songs), 0)
		})
	}()
}

// playlistGrid 是歌单卡片的自适应换行网格。
//
// 卡片宽度统一由 playlistCardWidth 决定，末行数量不足时也不会被 flex 拉宽；
// 标题区与元信息区固定高度，一/两行标题都不会把同排卡片的后续内容顶歪。
func (a *App) playlistGrid(c *ui.Context, lists []backend.SonglistSummary, cardMenu ...func(m *ui.Menu, pl backend.SonglistSummary)) {
	ui.Row(c).FillWidth().Wrap().GapX(18).GapY(24).AlignItems(ui.Start).Children(func() {
		for i := range lists {
			a.playlistCard(c, lists[i], cardMenu...)
		}
	})
}

// playlistCardWidth 是歌单卡片的统一宽度。
//
// 网格卡片必须跨行保持同宽；如果让最后一行继续 Grow，末行只有两三张时
// 会把卡片拉宽，看起来就像“后面几张突然变大”。
const playlistCardWidth = 172

// playlistCard 歌单卡片：封面 1:1，文字区占高固定，所有行使用同一宽度。
// cardMenu 让调用方给卡片追加右键菜单（如收藏页的「取消收藏」）。
func (a *App) playlistCard(c *ui.Context, pl backend.SonglistSummary, cardMenu ...func(m *ui.Menu, pl backend.SonglistSummary)) {
	t := c.Theme()
	art := a.Covers.Get(pl.Picurl, a.invalidate)
	card := ui.Column(c).Width(playlistCardWidth).Shrink(0).Gap(7).Label(pl.Title)
	card.Children(func() {
		box := ui.Box(c).FillWidth().AspectRatio(1).Clip().Radius(10).Background(t.SurfaceHover).
			Transition(hoverFade)
		box.Children(func() {
			if art != nil {
				ui.Image(c, art).Fill().Fit(ui.Cover)
			} else {
				ui.Box(c).Fill().Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(34)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			}
			// 悬停播放按钮
			play := ui.Box(c).Absolute().Bottom(8).Right(8).Size(34, 34).Radius(17).
				Background(ui.RGBA(0, 0, 0, 0.55)).Center()
			// 悬停淡入而不是硬切（主项目 .card:hover .art .play 同一件事）。
			vis := float32(0)
			if box.Hovered() {
				vis = 1
			}
			play.Opacity(play.Animate("hover", vis, 160*time.Millisecond))
			play.Children(func() {
				ui.Icon(c, Icons["play"]).FontSize(fz(16)).TextColor(ui.Hex("#ffffff")).AlignSelf(ui.Center)
			})
			if play.Clicked() {
				a.openPlaylistAndPlay(pl.ID)
			}
		})
		ui.Box(c).FillWidth().Height(fz(35)).Children(func() {
			ui.Text(c, pl.Title).FontSize(fz(12.5)).MaxLines(2).Ellipsis("…").LineHeight(1.35)
		})
		sub := compactNumber(pl.Listennum) + " 次播放"
		ui.Box(c).FillWidth().Height(fz(16)).Children(func() {
			ui.Text(c, sub).FontSize(fz(11)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
	})
	if len(cardMenu) > 0 && cardMenu[0] != nil {
		card.ContextMenu(func(m *ui.Menu) {
			cardMenu[0](m, pl)
		})
	}
	if card.Clicked() {
		a.Router.Push("/playlist/" + strconv.FormatInt(pl.ID, 10))
	}
}

// ===== 我喜欢 / 每日 30 首 =====

func (a *App) likedView(c *ui.Context) {
	t := c.Theme()
	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "我喜欢").FontSize(fz(24)).FontWeight(800)
			if n := a.PL.LikedTotal(); n > 0 {
				ui.Textf(c, "%d 首", n).FontSize(fz(13)).TextColor(t.TextMuted)
			}
		})
		if !a.PL.LoggedIn() {
			ui.Column(c).FillWidth().Center().Gap(10).Padding(48).Children(func() {
				ui.Text(c, "登录后同步你收藏的音乐").FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "去登录", func() { a.Router.Push("/login") })
			})
			return
		}
		liked := a.PL.LikedCache()
		if len(liked) == 0 {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() {
				ui.Spinner(c)
			})
			return
		}
		st := &a.liked
		st.songs = liked
		a.songList(c, st, nil, func(i int) { a.playListNow(liked, i) })
	})
}

func (a *App) dailyView(c *ui.Context) {
	st := &a.daily
	a.ensureDaily()
	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "每日 30 首").FontSize(fz(24)).FontWeight(800)
			ui.Spacer(c)
			if len(st.songs) > 0 {
				primaryBtn(c, "播放全部", func() { a.playListNow(st.songs, 0) })
			}
		})
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(c.Theme().TextMuted).Padding(24)
			return
		}
		if len(st.songs) == 0 {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
		a.songList(c, st, nil, func(i int) { a.playListNow(st.songs, i) })
	})
}

func (a *App) ensureDaily() {
	st := &a.daily
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	go func() {
		var d backend.SonglistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.RecommendDaily() })
		a.update(func() {
			st.loading = false
			st.loaded = true
			if err != nil {
				st.err = err.Error()
				return
			}
			st.songs = toPlayerSongs(d.Songs)
		})
	}()
}

// ===== 猜你喜欢 =====

type guessState struct {
	autoStarted bool
}

// guessView 是猜你喜欢无限电台的主舞台。电台状态在 player 里，页面只读：
// 离开再回来不会重开一轮；「换一批」是唯一主动换歌的入口。
func (a *App) guessView(c *ui.Context) {
	st := &a.guess
	if !st.autoStarted {
		st.autoStarted = true
		if !a.PL.RadioActive() && !a.PL.RadioLoading() {
			a.startRadioFromUI(true)
		}
	}
	t := c.Theme()
	cur, hasCur := a.PL.Current()
	active := a.PL.RadioActive()
	busy := a.PL.RadioLoading()
	artURL := ""
	if active && hasCur {
		artURL = coverURL(cur.AlbumPmid, cur.AlbumMid, 500)
	}
	art := a.Covers.Get(artURL, a.invalidate)

	a.pageEnter(c, ui.Column(c).Fill().Padding(28, 32, 32, 32).Gap(18).AlignItems(ui.Center)).Children(func() {
		ui.Row(c).FillWidth().MaxWidth(680).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "∞").FontSize(fz(17)).FontWeight(800).TextColor(t.Accent)
			ui.Text(c, "无限电台").FontSize(fz(15)).FontWeight(800).TextColor(t.Accent)
			ui.Spacer(c)
			if busy {
				ui.Text(c, "正在续上下一轮…").FontSize(fz(11.5)).TextColor(t.TextMuted)
			} else if active {
				ui.Text(c, "只向前播放").FontSize(fz(11.5)).TextColor(t.TextMuted)
			}
		})

		ui.Spacer(c)
		stage := ui.Column(c).Width(520).MaxWidth(640).AlignItems(ui.Center).Gap(14)
		stage.Children(func() {
			cover := ui.Box(c).Size(252, 252).Radius(20).Clip().Background(t.SurfaceHover).
				Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.22))
			cover.Children(func() {
				if art != nil {
					ui.Image(c, art).Fill().Fit(ui.Cover)
				} else {
					ui.Box(c).Fill().Center().Children(func() {
						ui.Text(c, "∞").FontSize(fz(92)).FontWeight(800).TextColor(t.Accent)
					})
				}
			})

			title := "猜你喜欢"
			sub := "按你的口味持续播放"
			if active && hasCur {
				title = cur.DisplayName()
				sub = cur.Artists
				if cur.Album != "" {
					sub += " · " + cur.Album
				}
			} else if busy {
				title = "正在为你取歌…"
				sub = "首轮约 5 首，播到轮末会自动续歌"
			} else if e := a.PL.RadioError(); e != "" {
				sub = e
			}
			ui.Text(c, title).FontSize(fz(27)).FontWeight(800).SingleLine().Ellipsis("…")
			ui.Text(c, sub).FontSize(fz(13)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")

			ui.Row(c).AlignItems(ui.Center).Gap(10).Margin(4, 0, 0, 0).Children(func() {
				if !active {
					primaryBtn(c, "开始电台", func() { a.startRadioFromUI(true) })
				} else {
					label := "播放"
					if a.PL.Playing() {
						label = "暂停"
					}
					primaryBtn(c, label, func() { a.PL.PlayOrPause() })
					plainBtn(c, "下一首", func() { a.PL.Next(false) })
					if hasCur {
						love := "收藏"
						if a.PL.IsLoved(cur.Mid) {
							love = "已收藏"
						}
						plainBtn(c, love, func() { a.PL.ToggleLove(cur) })
					}
				}
				plainBtn(c, "换一批", func() { a.startRadioFromUI(true) })
			})
		})
		ui.Spacer(c)
	})
}

// ===== 歌单详情 =====

type playlistState struct {
	loaded      bool
	loading     bool
	err         string
	info        backend.SonglistSummary
	creator     backend.Creator
	songs       []player.Song
	total       int64
	hasMore     bool
	page        int
	faved       bool
	favKnown    bool
	expanded    bool
	loadingMore bool
	list        songListState
	scroll      ui.ScrollState
}

func (a *App) playlistView(c *ui.Context, id int64) {
	if a.playlist == nil {
		a.playlist = map[int64]*playlistState{}
	}
	st := getOrCreatePage(a.playlist, &a.playlistRec, id, func() *playlistState { return &playlistState{} })
	a.ensurePlaylist(st, id)
	t := c.Theme()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 24, 24).Gap(16).TrackScroll(&st.scroll)).Children(func() {
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
			return
		}
		if st.info.Title == "" {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
		// 头部
		art := a.Covers.Get(st.info.Picurl, a.invalidate)
		ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Center).Children(func() {
			if art != nil {
				ui.Image(c, art).Size(168, 168).Fit(ui.Cover).Radius(14)
			} else {
				ui.Box(c).Size(168, 168).Radius(14).Background(t.SurfaceHover).Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(48)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			}
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				ui.Text(c, st.info.Title).FontSize(fz(26)).FontWeight(800).MaxLines(2).Ellipsis("…")
				meta := st.creator.Nick
				if meta == "" {
					meta = st.info.Nickname
				}
				if n := st.info.Songnum; n > 0 {
					meta += " · " + strconv.FormatInt(n, 10) + " 首"
				}
				ui.Text(c, meta).FontSize(fz(13)).TextColor(t.TextMuted)
				if st.info.Desc != "" {
					lines := 2
					toggle := "展开"
					if st.expanded {
						lines = 0
						toggle = "收起"
					}
					body := ui.Text(c, st.info.Desc).FontSize(fz(12.5)).TextColor(t.TextMuted)
					if lines > 0 {
						body = body.MaxLines(lines).Ellipsis("…")
					}
					linkButton(c, toggle, func() { st.expanded = !st.expanded })
				}
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					primaryBtn(c, "播放全部", func() { a.playListNow(st.songs, 0) })
					// 自建歌单不显示收藏按钮：收藏是收藏别人的歌单。
					if !a.isCreatedPlaylist(id) {
						favLabel := "收藏歌单"
						if st.faved {
							favLabel = "已收藏"
						}
						plainBtn(c, favLabel, func() { a.togglePlaylistFav(st, id) })
					}
				})
			})
		})

		// 歌曲行：整页滚动（原版行为），触底翻页
		ui.Column(c).FillWidth().Children(func() {
			for i := range st.songs {
				idx := i
				a.songRow(c, &st.list, i, func(i int) { a.playListNow(st.songs, i) }, func(m *ui.Menu, s player.Song, i int) {
					// 自建歌单的歌曲可以删除。
					if a.isCreatedPlaylist(id) {
						if m.Item("从歌单删除").Chosen() {
							a.deleteFromPlaylist(st, idx, s)
						}
					}
				})
			}
			if st.loadingMore {
				ui.Row(c).FillWidth().Center().Padding(14).Children(func() { ui.Spinner(c) })
			}
		})

		// 接近底部：翻页
		if st.hasMore && !st.loadingMore && st.scroll.MaxY > 0 && st.scroll.Y >= st.scroll.MaxY-300 {
			st.loadingMore = true
			st.page++
			go a.loadPlaylistPage(st, id, st.page)
		}
	})
}

func (a *App) ensurePlaylist(st *playlistState, id int64) {
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	st.page = 1
	go func() {
		var d backend.SonglistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.SonglistDetail(id, 1, 100) })
		if err != nil {
			a.update(func() {
				st.loading = false
				st.loaded = true
				st.err = err.Error()
			})
			return
		}
		// 非自建歌单要查收藏态：调 /songlist/fav/check 拿已收藏 ID 集合。
		var favIDs []int64
		if !a.isCreatedPlaylist(id) {
			a.withAPILimit(func() { favIDs, _ = a.API.SonglistFavCheck() })
		}
		a.update(func() {
			st.loading = false
			st.loaded = true
			st.info = d.Info
			st.creator = d.Creator
			st.songs = toPlayerSongs(d.Songs)
			st.total = d.Total
			st.hasMore = d.Hasmore != 0
			st.list.songs = st.songs
			st.favKnown = true
			for _, fid := range favIDs {
				if fid == id {
					st.faved = true
					break
				}
			}
		})
	}()
}

func (a *App) loadPlaylistPage(st *playlistState, id int64, page int) {
	go func() {
		var d backend.SonglistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.SonglistDetail(id, page, 100) })
		a.update(func() {
			st.loadingMore = false
			if err != nil {
				return
			}
			st.songs = append(st.songs, toPlayerSongs(d.Songs)...)
			st.hasMore = d.Hasmore != 0
			st.list.songs = st.songs
		})
	}()
}

func (a *App) togglePlaylistFav(st *playlistState, id int64) {
	target := !st.faved
	st.faved = target
	go func() {
		var err error
		a.withAPILimit(func() { err = a.API.SonglistFav(id, target) })
		a.update(func() {
			if err != nil {
				st.faved = !target
			} else {
				// 收藏态变化后作废「收藏的歌单」缓存
				a.favLists.loaded = false
			}
		})
	}()
}

// deleteFromPlaylist 乐观删除歌单内歌曲：先从本地切片移除，后端失败时整页重载。
func (a *App) deleteFromPlaylist(st *playlistState, idx int, s player.Song) {
	if idx < 0 || idx >= len(st.songs) {
		return
	}
	// 乐观删除：立刻从 UI 移除
	st.songs = append(st.songs[:idx], st.songs[idx+1:]...)
	st.list.songs = st.songs
	if st.total > 0 {
		st.total--
	}
	if st.info.Songnum > 0 {
		st.info.Songnum--
	}
	go func() {
		var err error
		a.withAPILimit(func() {
			err = a.API.SonglistDelSong(st.info.DirID, st.info.ID, s.SongID, s.SongType-1)
		})
		a.update(func() {
			if err != nil {
				// 后端失败：标记重载，下次渲染时拉最新状态
				st.loaded = false
				st.songs = nil
				st.list.songs = nil
				a.pendingToast = "删除失败：" + err.Error()
			} else {
				a.pendingToast = "已从歌单删除"
			}
		})
	}()
}

// ===== 搜索 =====

type searchState struct {
	kw         string
	requestSeq uint64
	tab        int // 0 歌曲 1 歌单
	songs      []player.Song
	songlists  []backend.SonglistSummary
	total      int64
	page       int
	nextpage   int64
	loading    bool
	err        string
	hot        []string
	hotLoaded  bool
	list       songListState
}

func (a *App) searchView(c *ui.Context, kw string) {
	st := &a.search
	if kw != "" && kw != st.kw {
		st.kw = kw
		st.songs = nil
		st.songlists = nil
		st.page = 0
		st.err = ""
		st.list.songs = nil
		st.requestSeq++
		a.ensureSearch(st)
	}
	if kw == "" && !st.hotLoaded {
		st.hotLoaded = true
		go func() {
			var hot []string
			var err error
			a.withAPILimit(func() { hot, err = a.API.SearchHotkey() })
			a.update(func() {
				if err == nil {
					st.hot = hot
				}
			})
		}()
	}

	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "搜索").FontSize(fz(24)).FontWeight(800)
			ui.Spacer(c)
			ui.Tabs(c, &st.tab, "歌曲", "歌单")
		})
		if st.kw == "" {
			// 热搜词
			if len(st.hot) > 0 {
				ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
					for _, h := range st.hot {
						hk := h
						chip := ui.ButtonBase(c).Padding(4, 12).Radius(999).Border(1, c.Theme().Border)
						chip.Children(func() { ui.Text(c, hk).FontSize(fz(12.5)) })
						if chip.Hovered() {
							chip.Background(c.Theme().SurfaceHover)
						}
						if chip.Clicked() {
							a.Router.Push("/search?kw=" + hk)
						}
					}
				})
			} else {
				ui.Text(c, "输入关键词搜索").FontSize(fz(13)).TextColor(c.Theme().TextMuted).Padding(24)
			}
			return
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(c.Theme().TextMuted)
			return
		}
		if st.loading && len(st.songs) == 0 && len(st.songlists) == 0 {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
		if st.tab == 0 {
			st.list.songs = st.songs
			a.songList(c, &st.list, func(page int) { a.loadSearchPage(st, page) },
				func(i int) { a.playListNow(st.songs, i) })
		} else {
			a.playlistGrid(c, st.songlists)
			if st.nextpage > 0 {
				linkButton(c, "加载更多", func() { a.loadSearchPage(st, st.page+1) })
			}
		}
	})
}

func (a *App) ensureSearch(st *searchState) {
	if st.loading {
		return
	}
	st.loading = true
	st.tab = 0
	kw := st.kw
	st.requestSeq++
	seq := st.requestSeq
	go func() {
		var res backend.SearchResults
		var err error
		a.withAPILimit(func() { res, err = a.API.Search(kw, 0, 1, 30) })
		a.update(func() {
			if st.requestSeq != seq || st.kw != kw {
				return
			}
			st.loading = false
			if err != nil {
				st.err = err.Error()
				return
			}
			st.songs = toPlayerSongs(res.Song)
			st.page = 1
			st.total = res.TotalNum
			st.nextpage = res.Nextpage
			st.list.songs = st.songs
			st.list.fetching = false
		})
	}()
}

func (a *App) loadSearchPage(st *searchState, page int) {
	if page <= st.page {
		return
	}
	st.page = page
	typ := 0
	if st.tab == 1 {
		typ = 3
	}
	kw := st.kw
	st.requestSeq++
	seq := st.requestSeq
	go func() {
		var res backend.SearchResults
		var err error
		a.withAPILimit(func() { res, err = a.API.Search(kw, typ, page, 30) })
		a.update(func() {
			if st.requestSeq != seq || st.kw != kw {
				return
			}
			st.list.fetching = false
			if err != nil {
				return
			}
			st.songs = append(st.songs, toPlayerSongs(res.Song)...)
			st.nextpage = res.Nextpage
			st.list.songs = st.songs
		})
	}()
}
