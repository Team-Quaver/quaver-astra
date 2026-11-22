package appui

import (
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

func homeNavApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	app := loggedApp(t)
	app.Router = ui.NewRouter("/")
	app.home = homeState{
		loaded: true,
		recs: []backend.SonglistSummary{
			{ID: 1, Title: "今日循环", Listennum: 328000},
			{ID: 2, Title: "深夜通勤", Listennum: 182000},
			{ID: 3, Title: "华语新声", Listennum: 92000},
			{ID: 4, Title: "独立摇滚", Listennum: 76000},
		},
		newsong: []player.Song{
			{Mid: "n1", Title: "自由的你", Artists: "G.E.M.邓紫棋"},
			{Mid: "n2", Title: "以我之见", Artists: "谭维维"},
			{Mid: "n3", Title: "醒时歌", Artists: "周传雄/希林娜依高"},
		},
		ai:       []backend.SonglistSummary{{ID: 11, Title: "AI · 雨夜 Synthwave"}, {ID: 12, Title: "AI · 清晨 Neo Soul"}},
		aiLoaded: true,
		topGroups: []backend.ToplistGroup{{
			Name: "精选",
			Toplists: []backend.ToplistSummary{
				{ID: 4, Title: "热歌榜", UpdateTips: "每日更新"},
				{ID: 26, Title: "新歌榜", UpdateTips: "每日更新"},
			},
		}},
		topLoaded: true,
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()
	return app, tester
}

// TestHomeQuickNavRevealsAtRightEdgeAndHidesAway 快捷跳转在右侧中部
// 出现，指针离开后自动收起；它不能常驻盖住封面和卡片。
func TestHomeQuickNavRevealsAtRightEdgeAndHidesAway(t *testing.T) {
	app, tester := homeNavApp(t)
	if app.home.navShown {
		t.Fatal("首页首次渲染时快捷跳转不应展开")
	}

	gripX := float32(testWinW) - 4 - homeGripWidth/2
	gripY := float32(topBarH + (testWinH-topBarH-barH)/2)
	tester.Move(gripX, gripY)
	tester.Advance(220 * time.Millisecond)
	if !app.home.navShown {
		t.Fatal("指针移到右侧把手后快捷跳转没有展开")
	}
	if _, ok := tester.Find("跳转到排行榜"); !ok {
		t.Fatalf("展开后没有找到排行榜入口；当前帧文本：%v", tester.Texts())
	}

	tester.Move(testWinW/2, testWinH/2)
	tester.Advance(600 * time.Millisecond)
	if app.home.navShown {
		t.Fatalf("指针离开后快捷跳转没有自动收起：hideAt=%v now=%v",
			app.home.navHideAt, tester.Now())
	}
}

// TestHomeQuickNavJumpsToSection 点排行榜入口后，首页滚动容器应滚到
// 排行榜区块，而不是把点击吞掉或跳去别的路由。
func TestHomeQuickNavJumpsToSection(t *testing.T) {
	app, tester := homeNavApp(t)

	gripX := float32(testWinW) - 4 - homeGripWidth/2
	gripY := float32(topBarH + (testWinH-topBarH-barH)/2)
	tester.Move(gripX, gripY)
	tester.Advance(220 * time.Millisecond)

	if err := tester.Click("跳转到排行榜"); err != nil {
		t.Fatalf("点击排行榜入口失败：%v", err)
	}
	tester.Advance(1500 * time.Millisecond)

	if app.home.scroll.Y <= 0 {
		t.Fatal("点击排行榜后首页没有滚动")
	}
	if app.home.navActive != 4 {
		t.Fatalf("点击排行榜后激活项是 %d，想看到 4", app.home.navActive)
	}
	if app.Router.Path() != "/" {
		t.Fatalf("快捷跳转把路由切到了 %q", app.Router.Path())
	}
}
