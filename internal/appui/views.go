package appui

import (
	"strconv"
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
}

func (a *App) homeView(c *ui.Context) {
	st := &a.home
	a.ensureHome()
	t := c.Theme()

	ui.Scroll(c).Fill().Padding(20, 24, 24, 24).Gap(18).Children(func() {
		if st.err != "" {
			blk := ui.Column(c).FillWidth().Center().Gap(8).Padding(40)
			a.enterBlock(c, 0, blk)
			blk.Children(func() {
				ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "重试", func() { st.loaded = false; st.err = "" })
			})
			return
		}
		if len(st.recs) == 0 {
			blk := ui.Column(c).FillWidth().Center().Padding(40)
			a.enterBlock(c, 0, blk)
			blk.Children(func() { ui.Spinner(c) })
			return
		}

		// 两栏头部：今日精选 hero + 新歌速递。
		// hero 带 MinWidth、容器 Wrap：内容区不够同时放下「hero 最小宽 +
		// 新歌速递 430」时，新歌速递折到下一行整行铺开，hero 不再被挤瘪。
		head := ui.Row(c).FillWidth().Wrap().Gap(16).AlignItems(ui.Stretch)
		a.enterBlock(c, 0, head)
		head.Children(func() {
			a.homeHero(c, st.recs[0])
			ui.Column(c).Width(430).Gap(2).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "新歌速递").FontSize(fz(15)).FontWeight(800).Grow(1)
					linkButton(c, "播放全部", func() {
						a.playListNow(st.newsong, 0)
					})
				})
				for i := 0; i < len(st.newsong) && i < 6; i++ {
					idx := i
					a.compactSongRow(c, st.newsong, idx)
				}
			})
		})

		// 推荐歌单网格（flex wrap 自适应列数）
		recs := ui.Column(c).FillWidth().Gap(10)
		a.enterBlock(c, 1, recs)
		recs.Children(func() {
			sectionHeader(c, "推荐歌单", nil)
			ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
				for i := range st.recs {
					a.playlistCard(c, st.recs[i])
				}
			})
		})
	})
}

func (a *App) homeHero(c *ui.Context, pl backend.SonglistSummary) {
	t := c.Theme()
	art := a.Covers.Get(pl.Picurl, a.invalidate)
	card := ui.Row(c).Grow(1).MinWidth(460).Gap(18).Padding(18).Radius(14).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).
		Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	card.Children(func() {
		if art != nil {
			ui.Image(c, art).Size(160, 160).Fit(ui.Cover).Radius(10)
		} else {
			ui.Box(c).Size(160, 160).Radius(10).Background(t.SurfaceHover).Center().Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(fz(44)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			})
		}
		ui.Column(c).Grow(1).Gap(8).Children(func() {
			ui.Text(c, "PLAYLIST · 今日精选").FontSize(fz(11)).FontWeight(600).TextColor(t.Accent)
			ui.Text(c, pl.Title).FontSize(fz(20)).FontWeight(800).MaxLines(2).Ellipsis("…")
			meta := ""
			if pl.Nickname != "" {
				meta = pl.Nickname
			}
			if pl.Songnum > 0 {
				if meta != "" {
					meta += " · "
				}
				meta += strconv.FormatInt(pl.Songnum, 10) + " 首"
			}
			ui.Text(c, meta).FontSize(fz(12.5)).TextColor(t.TextMuted)
			if pl.Desc != "" {
				ui.Text(c, pl.Desc).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(2).Ellipsis("…")
			}
			ui.Row(c).Gap(8).Margin(6, 0, 0, 0).Children(func() {
				primaryBtn(c, "播放歌单", func() { a.openPlaylistAndPlay(pl.ID) })
				plainBtn(c, "查看详情", func() { a.Router.Push("/playlist/" + strconv.FormatInt(pl.ID, 10)) })
			})
		})
	})
	if card.Clicked() {
		a.Router.Push("/playlist/" + strconv.FormatInt(pl.ID, 10))
	}
}

// compactSongRow 是新歌速递的紧凑行（无封面）。
func (a *App) compactSongRow(c *ui.Context, songs []player.Song, i int) {
	t := c.Theme()
	s := songs[i]
	row := ui.Row(c).FillWidth().Height(40).PaddingX(10).Gap(10).AlignItems(ui.Center).Radius(8).
		Transition(hoverFade)
	row.Children(func() {
		ui.Textf(c, "%02d", i+1).FontSize(fz(11)).TextColor(t.TextMuted).Width(22)
		ui.Text(c, s.DisplayName()).FontSize(fz(13)).SingleLine().Ellipsis("…").Grow(1)
		ui.Text(c, s.Artists).FontSize(fz(11.5)).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Width(120).TextAlign(ui.End)
	})
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.DoubleClicked() {
		a.playListNow(songs, i)
	}
}

func (a *App) ensureHome() {
	st := &a.home
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	go func() {
		recs, err1 := a.API.RecommendSonglists(1, 13)
		newsong, err2 := a.API.RecommendNewsong()
		a.update(func() {
			st.loading = false
			st.loaded = true
			if err1 != nil {
				st.err = err1.Error()
			} else {
				st.recs = recs.Songlists
			}
			if err2 == nil {
				st.newsong = toPlayerSongs(newsong.Songs)
			}
		})
	}()
}

func (a *App) openPlaylistAndPlay(id int64) {
	go func() {
		d, err := a.API.SonglistDetail(id, 1, 100)
		a.update(func() {
			if err != nil {
				return
			}
			a.playListNow(toPlayerSongs(d.Songs), 0)
		})
	}()
}

// playlistCard 歌单卡片（150 宽）。
func (a *App) playlistCard(c *ui.Context, pl backend.SonglistSummary) {
	t := c.Theme()
	art := a.Covers.Get(pl.Picurl, a.invalidate)
	card := ui.Column(c).Width(150).Gap(6)
	card.Children(func() {
		box := ui.Box(c).Size(150, 150).Clip().Radius(10).Background(t.SurfaceHover).
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
		ui.Text(c, pl.Title).FontSize(fz(12.5)).MaxLines(2).Ellipsis("…").LineHeight(1.35)
		sub := strconv.FormatInt(pl.Listennum, 10) + " 次播放"
		ui.Text(c, sub).FontSize(fz(11)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
	})
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
		d, err := a.API.RecommendDaily()
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
	list   songListState
	rounds int
}

func (a *App) guessView(c *ui.Context) {
	st := &a.guess
	a.ensureGuess()
	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "猜你喜欢").FontSize(fz(24)).FontWeight(800)
			ui.Spacer(c)
			plainBtn(c, "换一批", func() {
				st.list.songs = nil
				st.list.loaded = false
			})
			if len(st.list.songs) > 0 {
				primaryBtn(c, "播放全部", func() { a.playListNow(st.list.songs, 0) })
			}
		})
		if st.list.err != "" {
			ui.Text(c, st.list.err).FontSize(fz(13)).TextColor(c.Theme().TextMuted).Padding(24)
			return
		}
		if len(st.list.songs) == 0 {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
		a.songList(c, &st.list, nil, func(i int) { a.playListNow(st.list.songs, i) })
	})
}

func (a *App) ensureGuess() {
	st := &a.guess
	if st.list.loaded || st.list.loading {
		return
	}
	st.list.loading = true
	rounds := st.rounds
	if rounds != 2 {
		rounds = 2
	} else {
		rounds = 4
	}
	st.rounds = rounds
	go func() {
		res, err := a.API.RecommendGuess(rounds)
		a.update(func() {
			st.list.loading = false
			st.list.loaded = true
			if err != nil {
				st.list.err = err.Error()
				return
			}
			st.list.songs = toPlayerSongs(res.Songs)
		})
	}()
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
	st, ok := a.playlist[id]
	if !ok {
		st = &playlistState{}
		a.playlist[id] = st
	}
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
					favLabel := "收藏歌单"
					if st.faved {
						favLabel = "已收藏"
					}
					plainBtn(c, favLabel, func() { a.togglePlaylistFav(st, id) })
				})
			})
		})

		// 歌曲行：整页滚动（原版行为），触底翻页
		ui.Column(c).FillWidth().Children(func() {
			for i := range st.songs {
				a.songRow(c, &st.list, i, func(i int) { a.playListNow(st.songs, i) })
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
		d, err := a.API.SonglistDetail(id, 1, 100)
		a.update(func() {
			st.loading = false
			st.loaded = true
			if err != nil {
				st.err = err.Error()
				return
			}
			st.info = d.Info
			st.creator = d.Creator
			st.songs = toPlayerSongs(d.Songs)
			st.total = d.Total
			st.hasMore = d.Hasmore != 0
			st.list.songs = st.songs
		})
	}()
}

func (a *App) loadPlaylistPage(st *playlistState, id int64, page int) {
	go func() {
		d, err := a.API.SonglistDetail(id, page, 100)
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
		if err := a.API.SonglistFav(id, target); err != nil {
			a.update(func() { st.faved = !target })
		}
	}()
}

// ===== 搜索 =====

type searchState struct {
	kw        string
	tab       int // 0 歌曲 1 歌单
	songs     []player.Song
	songlists []backend.SonglistSummary
	total     int64
	page      int
	nextpage  int64
	loading   bool
	err       string
	hot       []string
	hotLoaded bool
	list      songListState
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
		a.ensureSearch(st)
	}
	if kw == "" && !st.hotLoaded {
		st.hotLoaded = true
		go func() {
			hot, err := a.API.SearchHotkey()
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
			ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
				for i := range st.songlists {
					a.playlistCard(c, st.songlists[i])
				}
			})
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
	go func() {
		res, err := a.API.Search(kw, 0, 1, 30)
		a.update(func() {
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
	go func() {
		res, err := a.API.Search(kw, typ, page, 30)
		a.update(func() {
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
