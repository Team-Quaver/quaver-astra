package appui

import (
	"math"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/egoist/mygo/ui"
)

// gridFindRects 取一组唯一标签或文案的实际盒。
func gridFindRects(t *testing.T, tester *ui.Tester, labels []string) []ui.Rect {
	t.Helper()
	out := make([]ui.Rect, len(labels))
	for i, label := range labels {
		out[i] = mustFind(t, tester, label)
	}
	return out
}

func assertGridRowAligned(t *testing.T, name string, boxes []ui.Rect) {
	t.Helper()
	if len(boxes) < 2 {
		return
	}
	for i := 1; i < len(boxes); i++ {
		if math.Abs(float64(boxes[i].Y-boxes[0].Y)) > 0.5 {
			t.Errorf("%s：第 %d 张卡片文字 y=%.1f，与首张 %.1f 不齐", name, i+1, boxes[i].Y, boxes[0].Y)
		}
		if i > 1 {
			step0 := boxes[1].X - boxes[0].X
			step := boxes[i].X - boxes[i-1].X
			if math.Abs(float64(step-step0)) > 0.5 {
				t.Errorf("%s：第 %d 列 x 间距 %.1f，与首列 %.1f 不一致", name, i+1, step, step0)
			}
		}
	}
}

// TestPlaylistGridCardsShareOneRowGeometry 钉住歌单流的两个对齐问题：
// 标题一/两行不能把同排卡片顶歪；卡片顶到播放量的距离也必须统一。
func TestPlaylistGridCardsShareOneRowGeometry(t *testing.T) {
	app := sideLoggedApp(t, false)
	titles := []string{
		"歌单 01｜短",
		"歌单 02｜两行标题会换行啦啦啦",
		"歌单 03｜A Long English Playlist Name",
		"歌单 04｜一行",
		"歌单 05｜今晚听什么｜通勤与深夜电台精选",
	}
	lists := make([]backend.SonglistSummary, len(titles))
	for i, title := range titles {
		lists[i] = backend.SonglistSummary{
			ID:        int64(i + 1),
			Title:     title,
			Listennum: int64(1000 + i),
		}
	}
	app.favLists = favListsState{loaded: true, lists: lists, total: int64(len(lists))}
	tester := ui.NewTester(app.favListsView, 1200, 800)

	titleBoxes := gridFindRects(t, tester, titles)
	metaLabels := []string{"1000 次播放", "1001 次播放", "1002 次播放", "1003 次播放", "1004 次播放"}
	metaBoxes := gridFindRects(t, tester, metaLabels)
	assertGridRowAligned(t, "歌单卡片", titleBoxes)
	assertGridRowAligned(t, "播放量", metaBoxes)
	for i := range titleBoxes {
		if math.Abs(float64((metaBoxes[i].Y-titleBoxes[i].Y)-(metaBoxes[0].Y-titleBoxes[0].Y))) > 0.5 {
			t.Errorf("第 %d 张卡片顶到播放量的垂直距离 %.1f，与首张 %.1f 不一致",
				i+1, metaBoxes[i].Y-titleBoxes[i].Y, metaBoxes[0].Y-titleBoxes[0].Y)
		}
	}
}

// TestHomePlaylistGridSkipsHeroAndAligns 首页 hero 已经展示首条推荐，
// 下方歌单网格不能再重复它；余下卡片沿用同一套对齐几何。
func TestHomePlaylistGridSkipsHeroAndAligns(t *testing.T) {
	app := sideLoggedApp(t, false)
	app.Router = ui.NewRouter("/")
	app.home = homeState{
		loaded: true,
		recs: []backend.SonglistSummary{
			{ID: 1, Title: "今日精选 Hero"},
			{ID: 2, Title: "推荐 01｜短"},
			{ID: 3, Title: "推荐 02｜两行标题会换行啦啦啦"},
			{ID: 4, Title: "推荐 03｜A Long English Playlist Name"},
			{ID: 5, Title: "推荐 04｜一行"},
			{ID: 6, Title: "推荐 05｜今晚听什么｜通勤与深夜电台精选"},
		},
		newsong: []player.Song{
			{Mid: "m1", Title: "新歌 01", Artists: "歌手"},
			{Mid: "m2", Title: "新歌 02", Artists: "歌手"},
		},
	}
	tester := ui.NewTester(app.homeView, 1200, 800)

	titles := []string{
		"推荐 01｜短",
		"推荐 02｜两行标题会换行啦啦啦",
		"推荐 03｜A Long English Playlist Name",
		"推荐 04｜一行",
		"推荐 05｜今晚听什么｜通勤与深夜电台精选",
	}
	assertGridRowAligned(t, "首页推荐歌单", gridFindRects(t, tester, titles))

	heroCount := 0
	for _, text := range tester.Texts() {
		if text == "今日精选 Hero" {
			heroCount++
		}
	}
	if heroCount != 1 {
		t.Errorf("今日精选 Hero 应只出现一次，实测 %d 次", heroCount)
	}
}

// TestPlaylistGridIncompleteRowKeepsCardWidth 末行卡片不能因为数量少而被拉宽。
// 这是用户看到“后面两张突然变大”的直接回归保护。
func TestPlaylistGridIncompleteRowKeepsCardWidth(t *testing.T) {
	app := sideLoggedApp(t, false)
	titles := []string{
		"宽度 01 短", "宽度 02 两行标题会换行啦啦啦", "宽度 03 A Long English Playlist Name",
		"宽度 04", "宽度 05 今晚听什么｜通勤与深夜电台精选", "宽度 06",
		"宽度 07 两行标题会换行啦啦啦", "宽度 08 A Long English Playlist Name",
	}
	lists := make([]backend.SonglistSummary, len(titles))
	for i, title := range titles {
		lists[i] = backend.SonglistSummary{ID: int64(i + 1), Title: title, Listennum: int64(2000 + i)}
	}
	app.favLists = favListsState{loaded: true, lists: lists, total: int64(len(lists))}
	tester := ui.NewTester(app.favListsView, 1200, 800)

	first := mustFind(t, tester, titles[0])
	for _, title := range titles[1:] {
		got := mustFind(t, tester, title)
		if math.Abs(float64(got.W-first.W)) > 0.5 {
			t.Errorf("%s 宽度 %.1f，与首张 %.1f 不一致：末行卡片被拉宽了", title, got.W, first.W)
		}
	}
}
