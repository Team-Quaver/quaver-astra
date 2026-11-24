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
	app := loggedApp(t)
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

// TestHomePlaylistGridAligns 首页推荐网格沿用统一卡片几何。
func TestHomePlaylistGridAligns(t *testing.T) {
	app := loggedApp(t)
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

}

// TestPlaylistGridIncompleteRowKeepsCardWidth 末行卡片不能因为数量少而被拉宽。
// 这是用户看到“后面两张突然变大”的直接回归保护。
func TestPlaylistGridIncompleteRowKeepsCardWidth(t *testing.T) {
	app := loggedApp(t)
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

// TestPlaylistGridCardWidthFillsRow：宽窗口不能只按最小卡宽排完就收工，
// 否则右侧会留下接近一整张卡的空洞。列数不变，余量均分给每张卡。
func TestPlaylistGridCardWidthFillsRow(t *testing.T) {
	for _, width := range []float32{932, 1092, 1144, 1452, 2048} {
		cardWidth := playlistGridCardWidth(width)
		cols := int((width + playlistGridGapX) / (playlistCardWidth + playlistGridGapX))
		filled := float32(cols)*cardWidth + float32(cols-1)*playlistGridGapX
		if math.Abs(float64(filled-width)) > 1e-3 {
			t.Errorf("宽度 %.1f 下排满后为 %.1f，未铺满整行", width, filled)
		}
		if cardWidth < playlistCardWidth-1e-3 {
			t.Errorf("宽度 %.1f 下卡宽 %.1f 小于最小值 %d", width, cardWidth, playlistCardWidth)
		}
	}
}

// TestPlaylistGridWideWindowFillsRow：整行最后一张卡的右缘要贴到内容区
// 右缘，而不是停在旧固定卡宽算出的位置。
func TestPlaylistGridWideWindowFillsRow(t *testing.T) {
	app := loggedApp(t)
	const windowWidth = 1500
	titles := []string{
		"宽屏 01", "宽屏 02", "宽屏 03", "宽屏 04", "宽屏 05", "宽屏 06",
		"宽屏 07", "宽屏 08",
	}
	lists := make([]backend.SonglistSummary, len(titles))
	for i, title := range titles {
		lists[i] = backend.SonglistSummary{ID: int64(i + 1), Title: title}
	}
	app.favLists = favListsState{loaded: true, lists: lists, total: int64(len(lists))}
	tester := ui.NewTester(app.favListsView, windowWidth, 800)

	last := mustFind(t, tester, titles[6])
	const pagePad = 24.0
	wantRight := float32(windowWidth) - pagePad
	if got := last.X + last.W; math.Abs(float64(got-wantRight)) > 0.5 {
		t.Errorf("首行最后一张卡右缘 %.1f，应贴住内容区右缘 %.1f", got, wantRight)
	}
}

func TestHomeExtendedSections(t *testing.T) {
	app := loggedApp(t)
	app.Router = ui.NewRouter("/")
	app.home = homeState{
		loaded: true,
		ai: []backend.SonglistSummary{
			{ID: 11, Title: "AI 测试歌单", Listennum: 12000},
		},
		aiLoaded: true,
		topGroups: []backend.ToplistGroup{{
			Name: "精选",
			Toplists: []backend.ToplistSummary{
				{ID: 4, Title: "热歌榜", UpdateTips: "每日更新", Listennum: 90000},
			},
		}},
		topLoaded: true,
	}
	tester := ui.NewTester(app.View, 1280, 1200)
	tester.Frame()
	for _, s := range []string{"AI 歌单", "AI 测试歌单", "排行榜", "精选", "热歌榜", "每日更新"} {
		if !tester.HasText(s) {
			t.Errorf("首页扩展区缺少 %q；当前文本：%v", s, tester.Texts())
		}
	}
}
