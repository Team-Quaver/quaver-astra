package appui

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

func TestFetchAllSingerAlbumsLoadsEveryPage(t *testing.T) {
	pages := map[int][]backend.SingerAlbum{
		1: {{Mid: "a1", Name: "第一张"}, {Mid: "a2", Name: "第二张"}},
		2: {{Mid: "a3", Name: "太阳之子"}},
	}
	var calls []int
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		calls = append(calls, page)
		if num != 2 {
			t.Fatalf("page size = %d, want 2", num)
		}
		return backend.SingerAlbums{Total: 3, AlbumList: pages[page]}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	wantCalls := []int{1, 2}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("requested pages = %v, want %v", calls, wantCalls)
	}
	var names []string
	for _, album := range got.AlbumList {
		names = append(names, album.Name)
	}
	wantNames := []string{"第一张", "第二张", "太阳之子"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("album names = %v, want %v", names, wantNames)
	}
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3", got.Total)
	}
}

func TestFetchAllSingerAlbumsStopsOnEmptyPageWithoutTotal(t *testing.T) {
	pages := map[int][]backend.SingerAlbum{
		1: {{Mid: "a1"}, {Mid: "a2"}},
		2: {{Mid: "a3"}, {Mid: "a4"}},
		3: nil,
	}
	var calls []int
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		calls = append(calls, page)
		return backend.SingerAlbums{AlbumList: pages[page]}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	wantCalls := []int{1, 2, 3}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("requested pages = %v, want %v", calls, wantCalls)
	}
	if len(got.AlbumList) != 4 {
		t.Fatalf("album count = %d, want 4", len(got.AlbumList))
	}
}

func TestFetchAllSingerAlbumsKeepsPagesLoadedBeforeError(t *testing.T) {
	boom := errors.New("page 2 failed")
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		if page == 1 {
			return backend.SingerAlbums{
				Total:     3,
				AlbumList: []backend.SingerAlbum{{Mid: "a1"}, {Mid: "a2"}},
			}, nil
		}
		return backend.SingerAlbums{}, boom
	}, 2)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if len(got.AlbumList) != 2 {
		t.Fatalf("album count = %d, want 2", len(got.AlbumList))
	}
}

// TestFetchAllSingerAlbumsSortsNewestFirst 上游分页顺序不是发行时间顺序：
// 拉全后必须从新到旧归一化；日期缺失的条目沉底且保持原相对顺序。
func TestFetchAllSingerAlbumsSortsNewestFirst(t *testing.T) {
	pages := map[int][]backend.SingerAlbum{
		1: {
			{Mid: "b", Name: "六月二十", TimePublic: "2026-06-20"},
			{Mid: "a", Name: "七月二十九", TimePublic: "2026-07-29"},
			{Mid: "u1", Name: "未知一"},
			{Mid: "s", Name: "六月二十四", TimePublic: "2026-06-24 12:00:00"},
		},
		2: {
			{Mid: "w", Name: "七月十一", TimePublic: "2026-07-11"},
			{Mid: "old", Name: "去年", TimePublic: "2025-12-01"},
			{Mid: "u2", Name: "未知二"},
		},
	}
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		return backend.SingerAlbums{Total: 7, AlbumList: pages[page]}, nil
	}, 4)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, album := range got.AlbumList {
		names = append(names, album.Name)
	}
	want := []string{"七月二十九", "七月十一", "六月二十四", "六月二十", "去年", "未知一", "未知二"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("album order = %v, want %v", names, want)
	}
}

// TestSingerAlbumCardsTitleSingleLineAndTopAligned 钉住专辑格的两个问题：
// 长标题必须单行省略；同排卡片顶部对齐，不能因为标题行数不同把封面顶歪。
func TestSingerAlbumCardsTitleSingleLineAndTopAligned(t *testing.T) {
	app, tester := testApp(t)
	app.Router = ui.NewRouter("/singer/s1?name=周杰伦")
	titles := []string{
		"短名",
		"这是一个很长的专辑名字应该被省略号截断而不是换行",
		"Brand New Sky (新世界的天空)",
	}
	app.singers = map[string]*singerState{"s1": {
		loaded: true,
		name:   "周杰伦",
		tab:    2,
		albums: []backend.SingerAlbum{
			{Mid: "a1", Name: titles[0], AlbumType: "Single", TimePublic: "2026-07-29"},
			{Mid: "a2", Name: titles[1], AlbumType: "EP", TimePublic: "2026-07-11"},
			{Mid: "a3", Name: titles[2], AlbumType: "Single", TimePublic: "2026-06-20"},
		},
		albumN: 3,
	}}
	app.albums = map[string]*albumState{}
	app.Covers = NewCoverCache()
	tester.Frame()

	boxes := gridFindRects(t, tester, titles)
	for i := 1; i < len(boxes); i++ {
		if math.Abs(float64(boxes[i].Y-boxes[0].Y)) > 0.5 {
			t.Errorf("%q 标题 y=%.1f，与首张 %.1f 不齐：同排封面被长短标题顶歪了",
				titles[i], boxes[i].Y, boxes[0].Y)
		}
		if math.Abs(float64(boxes[i].H-boxes[0].H)) > 0.5 {
			t.Errorf("%q 标题高 %.1f，与首张 %.1f 不一致：标题没有保持单行",
				titles[i], boxes[i].H, boxes[0].H)
		}
	}
}
