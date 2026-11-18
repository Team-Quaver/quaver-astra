package appui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
)

func TestMoveRows(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	cases := []struct {
		name    string
		rows    []int
		to      int
		want    []int
		changed bool
	}{
		{"single forward", []int{1}, 4, []int{1, 3, 4, 2, 5}, true},
		{"single to end", []int{1}, 5, []int{1, 3, 4, 5, 2}, true},
		{"single backward", []int{3}, 1, []int{1, 4, 2, 3, 5}, true},
		{"multiple forward", []int{1, 2}, 5, []int{1, 4, 5, 2, 3}, true},
		{"multiple backward", []int{3, 4}, 1, []int{1, 4, 5, 2, 3}, true},
		{"no-op", []int{1}, 1, in, false},
		{"no-op range", []int{2, 3}, 4, in, false},
		{"invalid", []int{9}, 0, in, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := moveRows(in, c.rows, c.to)
			if changed != c.changed || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("moveRows(%v, %d) = %v, %v; want %v, %v", c.rows, c.to, got, changed, c.want, c.changed)
			}
		})
	}
}

// TestReorderPayload 读侧 song_type 原样上行（映射交给后端）。
func TestReorderPayload(t *testing.T) {
	got := reorderPayload([]player.Song{{SongID: 1, SongType: 1}, {SongID: 2, SongType: 113}})
	if len(got) != 2 || got[0].SongID != 1 || got[0].SongType != 1 || got[1].SongType != 113 {
		t.Fatalf("payload = %+v", got)
	}
}

// TestReorderCreatedPlaylistPostsFullOrder 自建歌单重排把新顺序全量提交到
// /songlist/{dirid}/order，tid 用歌单 ID。
func TestReorderCreatedPlaylistPostsFullOrder(t *testing.T) {
	songs := []player.Song{
		{Mid: "m1", SongID: 1, SongType: 1},
		{Mid: "m2", SongID: 2, SongType: 5},
		{Mid: "m3", SongID: 3, SongType: 0},
	}
	type request struct {
		path string
		body struct {
			Tid   int64 `json:"tid"`
			Songs []struct {
				SongID   int64 `json:"song_id"`
				SongType int64 `json:"song_type"`
			} `json:"songs"`
		}
	}
	requests := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got request
		got.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		select {
		case requests <- got:
		default:
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"ok":true}}`))
	}))
	defer srv.Close()

	app := &App{
		API:  backend.NewClient(srv.URL),
		Conf: conf.Open(t.TempDir()),
	}
	p := player.New(fixtureWithPlaylist{}, loadEngine{}, &nullPrefs{})
	p.RefreshUser()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(p.Playlists()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	app.PL = p

	st := &playlistState{songs: append([]player.Song(nil), songs...)}
	app.reorderCreatedPlaylist(st, 77, []int{0}, 3)
	got := <-requests
	if got.path != "/songlist/9/order" || got.body.Tid != 77 {
		t.Fatalf("path=%q tid=%d; want /songlist/9/order, 77", got.path, got.body.Tid)
	}
	ids := make([]int64, 0, len(got.body.Songs))
	types := make([]int64, 0, len(got.body.Songs))
	for _, s := range got.body.Songs {
		ids = append(ids, s.SongID)
		types = append(types, s.SongType)
	}
	if !reflect.DeepEqual(ids, []int64{2, 3, 1}) || !reflect.DeepEqual(types, []int64{5, 0, 1}) {
		t.Fatalf("ids=%v types=%v; want [2 3 1] [5 0 1]", ids, types)
	}
	localIDs := make([]int64, 0, len(st.songs))
	for _, s := range st.songs {
		localIDs = append(localIDs, s.SongID)
	}
	if !reflect.DeepEqual(localIDs, []int64{2, 3, 1}) {
		t.Fatalf("local order = %v", localIDs)
	}
}

type fixtureWithPlaylist struct{ npBackend }

func (fixtureWithPlaylist) LoginStatus() (bool, error) { return true, nil }

func (fixtureWithPlaylist) Playlists() ([]player.PlaylistRef, error) {
	return []player.PlaylistRef{{ID: 77, DirID: 9, Title: "自建"}}, nil
}

// TestEnsurePlaylistPrefetchesAllPagesForCreated 自建歌单在后台拉齐所有页，
// 使全量重排可用的同时不留下半载列表。
func TestEnsurePlaylistPrefetchesAllPagesForCreated(t *testing.T) {
	requests := make(chan int, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			_, _ = fmt.Sscanf(p, "%d", &page)
		}
		requests <- page
		hasmore := 0
		songs := []map[string]any{}
		if page < 3 {
			hasmore = 1
			songs = append(songs, map[string]any{"mid": "m" + strconv.Itoa(page), "song_id": page, "song_type": 1})
		}
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"code":0,"msg":"ok","data":{"info":{"id":77,"title":"自建","songnum":2},"songs":[{"mid":"m1","song_id":1,"song_type":1},{"mid":"m2","song_id":2,"song_type":1}],"hasmore":%d,"total":%d}}`,
			hasmore, len(songs),
		)))
	}))
	defer srv.Close()

	app := &App{
		API:  backend.NewClient(srv.URL),
		PL:   player.New(fixtureWithPlaylist{}, loadEngine{}, &nullPrefs{}),
		Conf: conf.Open(t.TempDir()),
	}
	app.PL.RefreshUser()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(app.PL.Playlists()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	st := &playlistState{}
	app.ensurePlaylist(st, 77)
	for time.Now().Before(deadline) && (st.loading || st.prefetching || len(st.songs) < 4) {
		time.Sleep(10 * time.Millisecond)
	}
	if st.hasMore || st.prefetching || st.loadingMore {
		t.Fatalf("自建歌单应拉齐全部页：hasMore=%v prefetching=%v loadingMore=%v songs=%d", st.hasMore, st.prefetching, st.loadingMore, len(st.songs))
	}
	seen := map[int]bool{}
	for len(requests) > 0 {
		seen[<-requests] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Fatalf("应请求第 1/2/3 页，实际 %v", seen)
	}
}
