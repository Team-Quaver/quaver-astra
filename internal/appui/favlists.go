package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

// ===== 收藏的歌单 =====

// favListsState 是「我收藏的歌单」页的状态。
//
// 与「我创建的歌单」分开：前者是别人建的、只读；后者能编辑。这里
// 跟进 Quaver Astra 本体的分栏逻辑：网格展示 + 悬停播放 + 点开进详情。
type favListsState struct {
	loaded      bool
	loading     bool
	loadingMore bool
	err         string
	lists       []backend.SonglistSummary
	total       int64
	hasMore     bool
	page        int
}

func (a *App) favListsView(c *ui.Context) {
	st := &a.favLists
	a.ensureFavLists(st)
	t := c.Theme()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 28, 24).Gap(18)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "收藏的歌单").FontSize(fz(24)).FontWeight(800)
			if st.total > 0 {
				ui.Textf(c, "%d 个", st.total).FontSize(fz(13)).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			plainBtn(c, "刷新", func() {
				st.loaded, st.loading, st.page = false, false, 0
				st.lists = nil
			})
		})

		if !a.PL.LoggedIn() {
			ui.Column(c).FillWidth().Center().Gap(10).Padding(48).Children(func() {
				ui.Text(c, "登录后查看你收藏的歌单").FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "去登录", func() { a.Router.Push("/login") })
			})
			return
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
			return
		}
		if len(st.lists) == 0 {
			if st.loading {
				ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			} else {
				ui.Column(c).FillWidth().Center().Gap(8).Padding(48).Children(func() {
					ui.Icon(c, Icons["star"]).FontSize(fz(40)).TextColor(t.TextMuted).AlignSelf(ui.Center)
					ui.Text(c, "还没有收藏任何歌单").FontSize(fz(13)).TextColor(t.TextMuted)
				})
			}
			return
		}

		a.playlistGrid(c, st.lists, func(m *ui.Menu, pl backend.SonglistSummary) {
			if m.Item("取消收藏").Chosen() {
				a.unfavPlaylist(st, pl)
			}
		})

		if st.loadingMore {
			ui.Row(c).FillWidth().Center().Padding(14).Children(func() { ui.Spinner(c) })
			return
		}
		if st.hasMore {
			ui.Row(c).FillWidth().Center().Padding(10, 0, 2, 0).Children(func() {
				plainBtn(c, "加载更多", func() { a.loadMoreFavLists(st) })
			})
		}
	})
}

func (a *App) ensureFavLists(st *favListsState) {
	if st.loaded || st.loading || !a.PL.LoggedIn() {
		return
	}
	st.loading = true
	go func() {
		var res backend.FavSonglists
		var err error
		a.withAPILimit(func() { res, err = a.API.FavSonglists(1, 30) })
		a.update(func() {
			st.loading = false
			st.loaded = true
			st.page = 1
			if err != nil {
				st.err = err.Error()
				return
			}
			st.lists = res.Playlists
			st.total = res.Total
			st.hasMore = res.Hasmore
		})
	}()
}

func (a *App) loadMoreFavLists(st *favListsState) {
	if st.loadingMore || !st.hasMore {
		return
	}
	st.loadingMore = true
	page := st.page + 1
	go func() {
		var res backend.FavSonglists
		var err error
		a.withAPILimit(func() { res, err = a.API.FavSonglists(page, 30) })
		a.update(func() {
			st.loadingMore = false
			if err != nil {
				return
			}
			st.page = page
			st.lists = append(st.lists, res.Playlists...)
			if res.Total > 0 {
				st.total = res.Total
			}
			st.hasMore = res.Hasmore
		})
	}()
}

// unfavPlaylist 取消收藏歌单：乐观移除，后端失败时回退。
func (a *App) unfavPlaylist(st *favListsState, pl backend.SonglistSummary) {
	// 乐观移除
	for i, l := range st.lists {
		if l.ID == pl.ID {
			st.lists = append(st.lists[:i], st.lists[i+1:]...)
			break
		}
	}
	if st.total > 0 {
		st.total--
	}
	go func() {
		var err error
		a.withAPILimit(func() { err = a.API.SonglistFav(pl.ID, false) })
		a.update(func() {
			if err != nil {
				// 失败：整页重载拿回正确状态
				st.loaded = false
				st.lists = nil
				a.pendingToast = "取消收藏失败：" + err.Error()
			} else {
				// 同步已缓存的歌单详情页收藏态
				if ps, ok := a.playlist[pl.ID]; ok {
					ps.faved = false
				}
				a.pendingToast = "已取消收藏"
			}
		})
	}()
}
