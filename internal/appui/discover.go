package appui

import (
	"strconv"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 歌单集合页 =====

// playlistsView 收拢「我创建的歌单」与「我收藏的歌单」。侧栏不再重复列出
// 所有歌单，只保留这个稳定入口；两段数据各自加载、各自降级。
func (a *App) playlistsView(c *ui.Context) {
	t := c.Theme()
	a.ensureFavLists(&a.favLists)

	created := a.PL.Playlists()
	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 28, 24).Gap(22)).Children(func() {
		ui.Column(c).FillWidth().Gap(3).Children(func() {
			ui.Text(c, "歌单").FontSize(fz(26)).FontWeight(800)
			ui.Text(c, "我创建的歌单与我收藏的歌单").FontSize(fz(12.5)).TextColor(t.TextMuted)
		})

		if !a.PL.LoggedIn() {
			ui.Column(c).FillWidth().Center().Gap(10).Padding(48).Children(func() {
				ui.Text(c, "登录后查看你的歌单").FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "去登录", func() { a.Router.Push("/login") })
			})
			return
		}

		ui.Column(c).FillWidth().Gap(12).Children(func() {
			sectionHeader(c, "我创建的歌单", func() {
				ui.Textf(c, "%d 个", len(created)).FontSize(fz(11.5)).TextColor(t.TextMuted)
			})
			if len(created) == 0 {
				ui.Text(c, "还没有创建歌单").FontSize(fz(12.5)).TextColor(t.TextMuted).Padding(12)
				return
			}
			lists := make([]backend.SonglistSummary, 0, len(created))
			for _, pl := range created {
				lists = append(lists, backend.SonglistSummary{ID: pl.ID, DirID: pl.DirID, Title: pl.Title, Picurl: pl.Cover})
			}
			a.playlistGrid(c, lists)
		})

		st := &a.favLists
		ui.Divider(c)
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			sectionHeader(c, "我收藏的歌单", func() {
				if st.total > 0 {
					ui.Textf(c, "%d 个", st.total).FontSize(fz(11.5)).TextColor(t.TextMuted)
				}
				linkButton(c, "刷新", func() {
					st.loaded, st.loading, st.page = false, false, 0
					st.lists = nil
				})
			})
			switch {
			case st.err != "":
				ui.Text(c, st.err).FontSize(fz(12.5)).TextColor(t.TextMuted)
			case st.loading && len(st.lists) == 0:
				ui.Row(c).FillWidth().Center().Padding(28).Children(func() { ui.Spinner(c) })
			case len(st.lists) == 0:
				ui.Text(c, "还没有收藏任何歌单").FontSize(fz(12.5)).TextColor(t.TextMuted).Padding(12)
			default:
				a.playlistGrid(c, st.lists, func(m *ui.Menu, pl backend.SonglistSummary) {
					if m.Item("取消收藏").Chosen() {
						a.unfavPlaylist(st, pl)
					}
				})
				if st.loadingMore {
					ui.Row(c).FillWidth().Center().Padding(12).Children(func() { ui.Spinner(c) })
				} else if st.hasMore {
					ui.Row(c).FillWidth().Center().Children(func() {
						plainBtn(c, "加载更多", func() { a.loadMoreFavLists(st) })
					})
				}
			}
		})
	})
}

// ===== 排行榜 =====

type toplistState struct {
	loaded      bool
	loading     bool
	loadingMore bool
	err         string
	info        backend.SonglistSummary
	songs       []player.Song
	total       int64
	hasMore     bool
	page        int
	list        songListState
	scroll      ui.ScrollState
}

func (a *App) toplistView(c *ui.Context, id int64) {
	if a.toplists == nil {
		a.toplists = map[int64]*toplistState{}
	}
	st := getOrCreatePage(a.toplists, &a.toplistRec, id, func() *toplistState { return &toplistState{} })
	a.ensureToplist(st, id)
	t := c.Theme()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 28, 24).Gap(16).TrackScroll(&st.scroll)).Children(func() {
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
			return
		}
		if st.info.Title == "" {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
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
				ui.Text(c, "排行榜").FontSize(fz(10.5)).FontWeight(700).TextColor(t.Accent)
				ui.Text(c, st.info.Title).FontSize(fz(26)).FontWeight(800).MaxLines(2).Ellipsis("…")
				meta := ""
				if st.info.Songnum > 0 {
					meta = strconv.FormatInt(st.info.Songnum, 10) + " 首"
				}
				if st.info.Listennum > 0 {
					if meta != "" {
						meta += " · "
					}
					meta += compactNumber(st.info.Listennum) + " 次播放"
				}
				if meta != "" {
					ui.Text(c, meta).FontSize(fz(12.5)).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					primaryBtn(c, "播放全部", func() { a.playListNow(st.songs, 0) })
				})
			})
		})

		ui.Column(c).FillWidth().Children(func() {
			st.list.songs = st.songs
			a.songList(c, &st.list, func(page int) { a.loadToplistPage(st, id, page) }, func(i int) {
				a.playListNow(st.songs, i)
			})
		})
	})
}

func (a *App) ensureToplist(st *toplistState, id int64) {
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	st.err = ""
	go func() {
		var d backend.ToplistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.ToplistDetail(id, 1, 100) })
		a.update(func() {
			st.loading = false
			st.loaded = true
			if err != nil {
				st.err = err.Error()
				return
			}
			st.info = d.Info
			st.songs = toPlayerSongs(d.Songs)
			st.total = d.Total
			st.hasMore = d.Hasmore
			st.page = 1
			st.list.songs = st.songs
		})
	}()
}

func (a *App) loadToplistPage(st *toplistState, id int64, page int) {
	if st.loadingMore || page <= st.page {
		return
	}
	st.loadingMore = true
	go func() {
		var d backend.ToplistDetail
		var err error
		a.withAPILimit(func() { d, err = a.API.ToplistDetail(id, page, 100) })
		a.update(func() {
			st.loadingMore = false
			st.list.fetching = false
			if err != nil {
				return
			}
			st.page = page
			st.songs = append(st.songs, toPlayerSongs(d.Songs)...)
			st.hasMore = d.Hasmore
			st.list.songs = st.songs
		})
	}()
}

// toplistGrid 与歌单网格同宽同节奏，只是落地页换成榜单详情。
func (a *App) toplistGrid(c *ui.Context, lists []backend.ToplistSummary) {
	ui.Row(c).FillWidth().Wrap().GapX(18).GapY(24).AlignItems(ui.Start).Children(func() {
		for i := range lists {
			a.toplistCard(c, lists[i])
		}
	})
}

func (a *App) toplistCard(c *ui.Context, pl backend.ToplistSummary) {
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
			badge := ui.Box(c).Absolute().Top(8).Left(8).Padding(2, 7).Radius(999).
				Background(ui.RGBA(0, 0, 0, 0.58)).Center()
			badge.Children(func() { ui.Text(c, "榜").FontSize(fz(10)).TextColor(ui.Hex("#fff")).FontWeight(700) })
			play := ui.Box(c).Absolute().Bottom(8).Right(8).Size(34, 34).Radius(17).
				Background(ui.RGBA(0, 0, 0, 0.55)).Center()
			vis := float32(0)
			if box.Hovered() {
				vis = 1
			}
			play.Opacity(play.Animate("hover", vis, 160*time.Millisecond))
			play.Children(func() {
				ui.Icon(c, Icons["play"]).FontSize(fz(16)).TextColor(ui.Hex("#fff")).AlignSelf(ui.Center)
			})
			if play.Clicked() {
				a.Router.Push("/toplist/" + strconv.FormatInt(pl.ID, 10))
			}
		})
		ui.Box(c).FillWidth().Height(fz(35)).Children(func() {
			ui.Text(c, pl.Title).FontSize(fz(12.5)).MaxLines(2).Ellipsis("…").LineHeight(1.35)
		})
		meta := pl.UpdateTips
		if meta == "" {
			if pl.UpdateType == 0 {
				meta = "每周更新"
			} else {
				meta = "每日更新"
			}
		}
		ui.Box(c).FillWidth().Height(fz(16)).Children(func() {
			ui.Text(c, meta).FontSize(fz(11)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
	})
	if card.Clicked() {
		a.Router.Push("/toplist/" + strconv.FormatInt(pl.ID, 10))
	}
}

func compactNumber(n int64) string {
	switch {
	case n >= 100000000:
		return strconv.FormatFloat(float64(n)/100000000, 'f', 1, 64) + "亿"
	case n >= 10000:
		return strconv.FormatFloat(float64(n)/10000, 'f', 1, 64) + "万"
	default:
		return strconv.FormatInt(n, 10)
	}
}
