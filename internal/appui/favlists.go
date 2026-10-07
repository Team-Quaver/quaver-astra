package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

// ===== 收藏的歌单 =====

// favListsState 是「我收藏的歌单」页的状态。
//
// 与「我创建的歌单」分开：前者是别人建的、只读；后者能编辑。这里
// 跟进 Quaver Music 本体的分栏逻辑：网格展示 + 悬停播放 + 点开进详情。
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

	ui.Scroll(c).Fill().Padding(20, 24, 24, 24).Gap(16).Children(func() {
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

		ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
			for i := range st.lists {
				a.playlistCard(c, st.lists[i])
			}
		})

		if st.loadingMore {
			ui.Row(c).FillWidth().Center().Padding(14).Children(func() { ui.Spinner(c) })
			return
		}
		if st.hasMore {
			plainBtn(c, "加载更多", func() { a.loadMoreFavLists(st) })
		}
	})
}

func (a *App) ensureFavLists(st *favListsState) {
	if st.loaded || st.loading || !a.PL.LoggedIn() {
		return
	}
	st.loading = true
	go func() {
		res, err := a.API.FavSonglists(1, 30)
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
		res, err := a.API.FavSonglists(page, 30)
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
