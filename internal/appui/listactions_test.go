package appui

import (
	"fmt"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// listActionSongs 造一列足够长的歌：列表必须真的能滚，定位/回顶才验得出来。
func listActionSongs(n int) []player.Song {
	songs := make([]player.Song, n)
	for i := range songs {
		songs[i] = player.Song{
			Mid: fmt.Sprintf("m%d", i+1), SongID: int64(i + 1), SongType: 1,
			Title: fmt.Sprintf("song %d", i+1), Interval: 180,
		}
	}
	return songs
}

// TestLikedViewLocateAndBackToTop 「我喜欢」右下的两个浮钮：定位把当前曲
// 滚进视口，回顶把它送回第一行；到顶后回顶钮自己收起。
func TestLikedViewLocateAndBackToTop(t *testing.T) {
	songs := listActionSongs(40)
	p := player.New(lovedBackend{liked: songs}, loadEngine{}, &nullPrefs{})
	p.RefreshUser()
	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (!p.LoggedIn() || len(p.LikedCache()) == 0) {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.LoggedIn() || len(p.LikedCache()) == 0 {
		t.Fatal("「我喜欢」没加载进来，测试无从谈起")
	}
	p.PlayList(songs, len(songs)-1)

	app := &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/liked"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()
	tester.Frame()

	if _, ok := tester.Find("定位到正在播放"); !ok {
		t.Fatalf("列表右下角没有定位钮；当前帧文本：%v", tester.Texts())
	}
	if _, ok := tester.Find("回到顶部"); ok {
		t.Fatal("列表还在顶部时不该显示回到顶部")
	}

	if err := tester.Click("定位到正在播放"); err != nil {
		t.Fatalf("点击定位钮失败：%v", err)
	}
	tester.Advance(40 * time.Millisecond)
	mid := app.liked.list.Offset()
	if mid <= 0 {
		t.Fatal("定位后在 40ms 内没有任何位移")
	}
	tester.Advance(600 * time.Millisecond)

	first, last := app.liked.list.Visible()
	if first <= 0 {
		t.Fatalf("定位后列表仍在顶部：可见行 %d..%d", first, last)
	}
	if end := app.liked.list.Offset(); mid >= end {
		t.Fatalf("定位是瞬跳：40ms 已到 %.0f（终点 %.0f）", mid, end)
	}
	if _, ok := tester.Find("song 40"); !ok {
		t.Fatalf("定位后当前曲没有出现在视口里；当前帧文本：%v", tester.Texts())
	}
	if _, ok := tester.Find("回到顶部"); !ok {
		t.Fatal("列表滚下去后应出现回到顶部")
	}

	if err := tester.Click("回到顶部"); err != nil {
		t.Fatalf("点击回到顶部失败：%v", err)
	}
	tester.Advance(40 * time.Millisecond)
	mid = app.liked.list.Offset()
	if mid <= 0 {
		t.Fatal("回顶是瞬跳：40ms 内已经回到 0")
	}
	tester.Advance(600 * time.Millisecond)
	if first, _ := app.liked.list.Visible(); first != 0 {
		t.Fatalf("回到顶部后列表停在 %d，想看到 0", first)
	}
	if off := app.liked.list.Offset(); off != 0 {
		t.Fatalf("回到顶部后偏移是 %.1f，想回到 0", off)
	}
	if _, ok := tester.Find("song 1"); !ok {
		t.Fatalf("回到顶部后第一行不在视口里；当前帧文本：%v", tester.Texts())
	}
}

// TestPlaylistViewLocateAndBackToTop 歌单详情整页滚动：定位目标行走
// ScrollIntoView（内层 List 自己不滚），回顶把页面 Scroll 平滑拉回 0。
func TestPlaylistViewLocateAndBackToTop(t *testing.T) {
	app, _, tester := playlistOrderFixture(t, 60, 60)
	st := app.playlist[77]
	app.PL.PlayList(st.songs, len(st.songs)-1)
	tester.Frame()

	if _, ok := tester.Find("定位到正在播放"); !ok {
		t.Fatalf("歌单页右下角没有定位钮；当前帧文本：%v", tester.Texts())
	}
	if err := tester.Click("定位到正在播放"); err != nil {
		t.Fatalf("点击定位钮失败：%v", err)
	}
	tester.Advance(40 * time.Millisecond)
	mid := st.scroll.Y
	if mid <= 0 {
		t.Fatal("定位后在 40ms 内没有任何位移")
	}
	tester.Advance(1500 * time.Millisecond)

	if st.scroll.Y <= 0 {
		t.Fatal("定位后歌单页没有滚动")
	}
	if st.scroll.Y <= mid {
		t.Fatalf("定位是瞬跳：40ms 已到 %.1f（终点 %.1f）", mid, st.scroll.Y)
	}
	row := mustFind(t, tester, "song 60")
	if row.Y < topBarH || row.Y+row.H > float32(testWinH-barH) {
		t.Fatalf("定位后当前曲不在视口内：行 y=%.1f h=%.1f", row.Y, row.H)
	}
	if _, ok := tester.Find("回到顶部"); !ok {
		t.Fatal("歌单页滚下去后应出现回到顶部")
	}

	if err := tester.Click("回到顶部"); err != nil {
		t.Fatalf("点击回到顶部失败：%v", err)
	}
	tester.Advance(40 * time.Millisecond)
	mid = st.scroll.Y
	if mid <= 0 {
		t.Fatal("回顶是瞬跳：40ms 内已经回到 0")
	}
	tester.Advance(1500 * time.Millisecond)
	if st.scroll.Y > 0.5 {
		t.Fatalf("回到顶部后页面停在 %.1f，想回到 0", st.scroll.Y)
	}
}
