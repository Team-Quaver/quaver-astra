package appui

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

// TestSearchSingerTabRendersAndJumps 搜索页「歌手」分类用歌手卡展示头像、
// 名称和歌曲数，点击落到歌手页。
func TestSearchSingerTabRendersAndJumps(t *testing.T) {
	app, tester := testApp(t)
	app.Router = ui.NewRouter("/search?kw=周杰伦")
	app.singers = map[string]*singerState{"s1": {loaded: true, name: "周杰伦"}}
	app.search = searchState{
		kw:        "周杰伦",
		tab:       searchTabSingers,
		loadedTab: searchTabSingers,
		tabs: [searchTabCount]searchTabData{
			searchTabSingers: {
				loaded:  true,
				singers: []backend.SearchSinger{{Mid: "s1", Name: "周杰伦", SongNum: 128}},
			},
		},
	}
	tester.Frame()

	if !tester.HasText("周杰伦") || !tester.HasText("128 首") {
		t.Fatalf("歌手结果未渲染；文本：%v", tester.Texts())
	}
	if err := tester.Click("周杰伦"); err != nil {
		t.Fatalf("点击歌手卡失败：%v", err)
	}
	if got := app.Router.Path(); got != "/singer/s1" {
		t.Errorf("path = %q，期望 /singer/s1", got)
	}
	if got := app.Router.Query("name"); got != "周杰伦" {
		t.Errorf("name = %q，期望 周杰伦", got)
	}
}

// TestSearchAlbumTabRendersAndJumps 搜索页「专辑」分类展示专辑名与
// 歌手/发行时间，点击落到专辑页。
func TestSearchAlbumTabRendersAndJumps(t *testing.T) {
	app, tester := testApp(t)
	app.Router = ui.NewRouter("/search?kw=周杰伦")
	app.albums = map[string]*albumState{"a1": {loaded: true, name: "叶惠美"}}
	app.search = searchState{
		kw:        "周杰伦",
		tab:       searchTabAlbums,
		loadedTab: searchTabAlbums,
		tabs: [searchTabCount]searchTabData{
			searchTabAlbums: {
				loaded: true,
				albums: []backend.SearchAlbum{{
					Mid: "a1", Name: "叶惠美", Singer: "周杰伦", TimePublic: "2003-07-31",
				}},
			},
		},
	}
	tester.Frame()

	if !tester.HasText("叶惠美") || !tester.HasText("周杰伦 · 2003-07-31") {
		t.Fatalf("专辑结果未渲染；文本：%v", tester.Texts())
	}
	if err := tester.Click("叶惠美"); err != nil {
		t.Fatalf("点击专辑卡失败：%v", err)
	}
	if got := app.Router.Path(); got != "/album/a1" {
		t.Errorf("path = %q，期望 /album/a1", got)
	}
	if got := app.Router.Query("name"); got != "叶惠美" {
		t.Errorf("name = %q，期望 叶惠美", got)
	}
}
