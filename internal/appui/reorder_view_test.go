package appui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/egoist/mygo/ui"
)

// reorderBackend 提供已登录状态与三首「我喜欢」歌曲，并记录重排请求。
type reorderBackend struct {
	npBackend
	songs []player.Song
}

func (b *reorderBackend) LoginStatus() (bool, error) { return true, nil }
func (b *reorderBackend) LikedPage(page, num int) ([]player.Song, int64, bool, error) {
	if page > 1 {
		return nil, 0, false, nil
	}
	return b.songs, int64(len(b.songs)), false, nil
}

func dragRows(tt *ui.Tester, fromX, fromY, toX, toY float32) {
	tt.Press(fromX, fromY)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
	}
	tt.Release(toX, toY)
}

// TestLikedViewReorderDragsAndPosts 真实拖一行后：本地列表顺序变化，且 POST
// /songlist/201/order 带上全量新顺序（song_type 原样上行）。
func TestLikedViewReorderDragsAndPosts(t *testing.T) {
	songs := []player.Song{
		{Mid: "m1", SongID: 1, SongType: 1, Title: "song 1"},
		{Mid: "m2", SongID: 2, SongType: 113, Title: "song 2"},
		{Mid: "m3", SongID: 3, SongType: 1, Title: "song 3"},
	}
	p := player.New(&reorderBackend{songs: songs}, loadEngine{}, &nullPrefs{})
	p.RefreshUser()
	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(p.LikedCache()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.LikedCache()) != 3 {
		t.Fatal("「我喜欢」没加载进来，测试无从谈起")
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
		API:           backend.NewClient(srv.URL),
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/liked"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()

	first, ok := tester.Find("song 1")
	if !ok {
		t.Fatalf("找不到第一行；当前文本：%v", tester.Texts())
	}
	third, ok := tester.Find("song 3")
	if !ok {
		t.Fatalf("找不到第三行；当前文本：%v", tester.Texts())
	}
	// 把第一行拖到第三行的下边缘：移动到列表末尾。
	dragRows(tester, first.X+first.W/2, first.Y+first.H/2, third.X+third.W/2, third.Y+third.H)
	tester.Frame()

	got := <-requests
	if got.path != "/songlist/201/order" {
		t.Fatalf("path = %q", got.path)
	}
	if got.body.Tid != 0 || len(got.body.Songs) != 3 {
		t.Fatalf("body = %+v", got.body)
	}
	ids := []int64{got.body.Songs[0].SongID, got.body.Songs[1].SongID, got.body.Songs[2].SongID}
	if ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Fatalf("song order = %v; want [2 3 1]", ids)
	}
	if got.body.Songs[2].SongType != 1 || got.body.Songs[0].SongType != 113 {
		t.Fatalf("song_type must be sent as read-side value: %+v", got.body.Songs)
	}
}

// TestPlaylistMetaHidesCreatorForCreated 自建歌单头部不显示创建者名字，也不会
// 留下“ · N 首”的前导分隔点；他人歌单正常显示创建者。
func TestPlaylistMetaHidesCreatorForCreated(t *testing.T) {
	songs := []player.Song{{Mid: "m1", SongID: 1, SongType: 1}}
	p := player.New(fixtureWithPlaylist{}, loadEngine{}, &nullPrefs{})
	p.RefreshUser()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(p.Playlists()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	app := &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/playlist/77"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	app.playlist[77] = &playlistState{
		loaded:  true,
		info:    backend.SonglistSummary{ID: 77, Title: "自建", Songnum: 3, Nickname: "作者A"},
		creator: backend.Creator{Nick: "作者A"},
		songs:   songs,
		list:    songListState{songs: songs, loaded: true},
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()
	texts := tester.Texts()
	joined := ""
	for _, s := range texts {
		joined += s + "\n"
	}
	if contains(joined, "作者A") {
		t.Fatalf("自建歌单不应显示创建者名字；文本：%v", texts)
	}
	if !contains(joined, "3 首") {
		t.Fatalf("自建歌单应显示歌曲数；文本：%v", texts)
	}

	app.playlist[88] = &playlistState{
		loaded:  true,
		info:    backend.SonglistSummary{ID: 88, Title: "他人", Songnum: 3},
		creator: backend.Creator{Nick: "作者B"},
		songs:   songs,
		list:    songListState{songs: songs, loaded: true},
	}
	app.Router = ui.NewRouter("/playlist/88")
	tester = ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()
	texts = tester.Texts()
	if !contains(strings.Join(texts, "\n"), "作者B · 3 首") {
		t.Fatalf("他人歌单应显示 作者 · N 首；文本：%v", texts)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// playlistOrderFixture 起一个假后端：/songlist/77/detail 按页返回 total 首歌，
// /songlist/9/order 记下重排请求。返回 App、请求通道与就绪的 Tester。
func playlistOrderFixture(t *testing.T, total, perPage int) (*App, chan []int64, *ui.Tester) {
	t.Helper()
	orders := make(chan []int64, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/detail") {
			page := 1
			_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			from := (page-1)*perPage + 1
			count := min(perPage, max(0, total-from+1))
			items := make([]string, 0, count)
			for k := range count {
				n := from + k
				items = append(items, fmt.Sprintf(`{"mid":"m%d","id":%d,"type":1,"name":"song %d"}`, n, n, n))
			}
			hasmore := 0
			if from+count-1 < total {
				hasmore = 1
			}
			_, _ = fmt.Fprintf(w, `{"code":0,"msg":"ok","data":{"info":{"id":77,"dirid":9,"title":"自建","songnum":%d},`+
				`"songs":[%s],"hasmore":%d,"total":%d}}`, total, strings.Join(items, ","), hasmore, total)
			return
		}
		var body struct {
			Songs []struct {
				SongID int64 `json:"song_id"`
			} `json:"songs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ids := make([]int64, 0, len(body.Songs))
		for _, s := range body.Songs {
			ids = append(ids, s.SongID)
		}
		select {
		case orders <- ids:
		default:
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"ok":true}}`))
	}))
	t.Cleanup(srv.Close)

	app := &App{
		API:           backend.NewClient(srv.URL),
		PL:            player.New(fixtureWithPlaylist{}, loadEngine{}, &nullPrefs{}),
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/playlist/77"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	app.PL.RefreshUser()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(app.PL.Playlists()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	waitText(t, tester, "song 1")
	for time.Now().Before(deadline) {
		tester.Frame()
		st := app.playlist[77]
		if st != nil && !st.prefetching && !st.loading && !st.hasMore {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return app, orders, tester
}

// TestPlaylistViewReorderDragsAndPosts 自建歌单页真拖一行：行必须挂在 ui.List
// 上，否则 ListState.Reorder 永远不被消费——「拖不动」的老毛病。拖完本地顺序
// 变化，且 POST /songlist/9/order 带全量新顺序。
func TestPlaylistViewReorderDragsAndPosts(t *testing.T) {
	app, orders, tester := playlistOrderFixture(t, 3, 100)
	st := app.playlist[77]
	if st.list.list.Reorder == nil {
		t.Fatal("自建歌单拉齐后应开启拖拽换序")
	}
	first := mustFind(t, tester, "song 1")
	third := mustFind(t, tester, "song 3")
	// 拖到最后一首的下边缘：末尾那段留白也是列表的落点，否则「排到最后」
	// 会被判成丢在列表外。
	dragRows(tester, first.X+first.W/2, first.Y+first.H/2, third.X+third.W/2, third.Y+third.H)
	tester.Frame()

	select {
	case ids := <-orders:
		if !reflect.DeepEqual(ids, []int64{2, 3, 1}) {
			t.Fatalf("提交的顺序 = %v；want [2 3 1]", ids)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("拖拽没有发出重排请求：行没挂到 ui.List 上")
	}
	local := make([]int64, 0, len(st.songs))
	for _, s := range st.songs {
		local = append(local, s.SongID)
	}
	if !reflect.DeepEqual(local, []int64{2, 3, 1}) {
		t.Fatalf("本地顺序 = %v；want [2 3 1]", local)
	}
	if !reflect.DeepEqual(songIDs(st.list.songs), local) {
		t.Fatalf("列表数据源没跟着重排：%v vs %v", songIDs(st.list.songs), local)
	}
}

// TestPlaylistViewReorderAfterScroll 多页歌单滚到中段再拖：虚拟列表只铺视口
// 内的行，换序仍要按全局下标提交全量顺序。
func TestPlaylistViewReorderAfterScroll(t *testing.T) {
	const total = 60
	app, orders, tester := playlistOrderFixture(t, total, 20)
	st := app.playlist[77]
	if len(st.songs) != total {
		t.Fatalf("应后台拉齐 %d 首，实际 %d", total, len(st.songs))
	}
	tester.Scroll(testWinW/2, testWinH/2, 0, 1200)
	tester.Frame()
	tester.Frame()

	// 视口内取两行：上面那行拖到下面那行的下边缘。
	var src, dst ui.Rect
	for _, text := range tester.Texts() {
		r, ok := tester.Find(text)
		if !ok || r.H <= 0 || r.Y < 200 || r.Y > 500 {
			continue
		}
		if src.H == 0 {
			src = r
		} else if r.Y > src.Y+50 {
			dst = r
			break
		}
	}
	if src.H == 0 || dst.H == 0 {
		t.Fatalf("滚动后视口内取不到两行；文本：%v", tester.Texts())
	}
	before := songIDs(st.songs)
	dragRows(tester, src.X+20, src.Y+src.H/2, dst.X+20, dst.Y+dst.H)
	tester.Frame()

	select {
	case ids := <-orders:
		if len(ids) != total {
			t.Fatalf("重排必须全量提交 %d 首，实际 %d", total, len(ids))
		}
		if reflect.DeepEqual(ids, before) {
			t.Fatal("滚动后拖拽没有改变顺序")
		}
		if !reflect.DeepEqual(ids, songIDs(st.songs)) {
			t.Fatalf("提交顺序与本地不一致：%v vs %v", ids, songIDs(st.songs))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("滚动后拖拽没有发出重排请求")
	}
}

// TestPlaylistViewReorderToLastRow 把中间的歌拖到最后一首之下：列表末尾要留出
// 落点，否则指针压在最后一行底边（无尺寸列表的边界）上算列表外，拖拽被丢弃。
func TestPlaylistViewReorderToLastRow(t *testing.T) {
	app, orders, tester := playlistOrderFixture(t, 5, 100)
	st := app.playlist[77]
	second := mustFind(t, tester, "song 2")
	last := mustFind(t, tester, "song 5")
	dragRows(tester, second.X+second.W/2, second.Y+second.H/2, last.X+last.W/2, last.Y+last.H)
	tester.Frame()

	select {
	case ids := <-orders:
		if !reflect.DeepEqual(ids, []int64{1, 3, 4, 5, 2}) {
			t.Fatalf("提交的顺序 = %v；want [1 3 4 5 2]", ids)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("拖到末尾没有发出重排请求：列表末尾缺少落点")
	}
	if !reflect.DeepEqual(songIDs(st.songs), []int64{1, 3, 4, 5, 2}) {
		t.Fatalf("本地顺序 = %v；want [1 3 4 5 2]", songIDs(st.songs))
	}
}

// TestPlaylistReorderDisabledUntilFullyLoaded 列表没拉齐时不许拖：全量重排
// 只交一部分会打乱没提交的那部分。
func TestPlaylistReorderDisabledUntilFullyLoaded(t *testing.T) {
	songs := []player.Song{{Mid: "m1", SongID: 1}, {Mid: "m2", SongID: 2}}
	base := playlistState{songs: songs, total: 2, loaded: true}
	cases := []struct {
		name string
		mut  func(*playlistState)
		want bool
	}{
		{"拉齐", func(*playlistState) {}, true},
		{"还有下一页", func(st *playlistState) { st.hasMore = true }, false},
		{"后台补齐中", func(st *playlistState) { st.prefetching = true }, false},
		{"翻页中", func(st *playlistState) { st.loadingMore = true }, false},
		{"条数对不上总数", func(st *playlistState) { st.total = 5 }, false},
		{"空列表", func(st *playlistState) { st.songs = nil }, false},
	}
	app := &App{PL: player.New(fixtureWithPlaylist{}, loadEngine{}, &nullPrefs{})}
	app.PL.RefreshUser()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(app.PL.Playlists()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := base
			c.mut(&st)
			if got := app.canReorderPlaylist(&st, 77); got != c.want {
				t.Fatalf("canReorderPlaylist = %v; want %v", got, c.want)
			}
		})
	}
	if app.canReorderPlaylist(&base, 88) {
		t.Fatal("他人歌单不可重排")
	}
}

func songIDs(songs []player.Song) []int64 {
	ids := make([]int64, 0, len(songs))
	for _, s := range songs {
		ids = append(ids, s.SongID)
	}
	return ids
}
