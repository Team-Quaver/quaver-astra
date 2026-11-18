package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"
)

// moveRows 按 ui.ListState.Reorder 的语义移动切片元素：rows 是要移动的下标
// （升序），to 是它们要插到的位置（以移动前的下标计）。返回新切片和是否
// 真的发生了移动。
func moveRows[T any](songs []T, rows []int, to int) ([]T, bool) {
	n := len(songs)
	if len(rows) == 0 || to < 0 || to > n {
		return songs, false
	}
	take := make(map[int]bool, len(rows))
	for _, r := range rows {
		if r < 0 || r >= n || take[r] {
			return songs, false
		}
		take[r] = true
	}
	// 目标是“移动前”的下标：去掉被移动的行后，剩余序列里 to 之前的
	// 非移动行数就是插入点。整体不动时不必重排。
	at := 0
	for i := 0; i < to; i++ {
		if !take[i] {
			at++
		}
	}
	changed := false
	for j, r := range rows {
		if r != at+j {
			changed = true
			break
		}
	}
	if !changed {
		return songs, false
	}
	out := make([]T, 0, n)
	for i, s := range songs {
		if !take[i] {
			out = append(out, s)
		}
	}
	moved := make([]T, 0, len(rows))
	for _, r := range rows {
		moved = append(moved, songs[r])
	}
	out = append(out[:at], append(moved, out[at:]...)...)
	return out, true
}

// reorderPayload 读侧歌曲转成后端重排请求；song_type 原样上行，由后端做写侧映射。
func reorderPayload(songs []player.Song) []backend.SonglistReorderSong {
	refs := make([]backend.SonglistReorderSong, 0, len(songs))
	for _, s := range songs {
		refs = append(refs, backend.SonglistReorderSong{SongID: s.SongID, SongType: s.SongType})
	}
	return refs
}

// reorderLiked 重排「我喜欢」：乐观更新本地列表，失败时回滚。
func (a *App) reorderLiked(st *songListState, rows []int, to int) {
	before := a.PL.LikedCache()
	after, ok := moveRows(before, rows, to)
	if !ok {
		return
	}
	a.PL.SetLikedCache(after)
	st.songs = after
	go func() {
		var err error
		a.withAPILimit(func() {
			err = a.API.SonglistReorder(201, 0, reorderPayload(after))
		})
		a.update(func() {
			if err != nil {
				a.PL.SetLikedCache(before)
				st.songs = before
				a.pendingToast = "重排失败：" + err.Error()
			}
		})
	}()
}

// canReorderPlaylist 报告自建歌单此刻能不能拖拽换序。
//
// 后端 SeqSonglist 是「全量提交」：只交手里这一部分时上游照样返回成功，但没
// 交的那部分顺序会被打乱。所以除了要是自建歌单，还得确认整份列表已经拉齐
// ——还有下一页、正在后台补齐、或条数对不上详情给的总数时都不放开拖拽。
func (a *App) canReorderPlaylist(st *playlistState, id int64) bool {
	if !a.isCreatedPlaylist(id) || st.hasMore || st.prefetching || st.loadingMore || st.loading {
		return false
	}
	if len(st.songs) == 0 {
		return false
	}
	if st.total > 0 && int64(len(st.songs)) < st.total {
		return false
	}
	return true
}

// reorderCreatedPlaylist 重排自建歌单：乐观更新本页列表，失败时重载。
func (a *App) reorderCreatedPlaylist(st *playlistState, id int64, rows []int, to int) {
	var dirid int64
	for _, pl := range a.PL.Playlists() {
		if pl.ID == id {
			dirid = pl.DirID
			break
		}
	}
	if dirid <= 0 {
		return
	}
	before := append([]player.Song(nil), st.songs...)
	after, ok := moveRows(before, rows, to)
	if !ok {
		return
	}
	st.songs = after
	st.list.songs = after
	go func() {
		var err error
		a.withAPILimit(func() {
			err = a.API.SonglistReorder(dirid, id, reorderPayload(after))
		})
		a.update(func() {
			if err != nil {
				st.loaded = false
				st.songs = nil
				st.list.songs = nil
				st.prefetchTried = false
				a.pendingToast = "重排失败：" + err.Error()
			}
		})
	}()
}

// enableSongReorder 给歌单列表接上拖拽换序。liked/playlist 之外的调用方不传
// reorder，列表保持原样（不可拖）。
func (a *App) enableSongReorder(st *songListState, reorder func(rows []int, to int)) {
	st.list.Reorder = reorder
	st.list.Key = func(i int) any {
		s := st.songs[i]
		return struct {
			SongID int64
			Mid    string
		}{s.SongID, s.Mid}
	}
}
