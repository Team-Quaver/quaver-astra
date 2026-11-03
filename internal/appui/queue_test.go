package appui

import (
	"image/color"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 正在播放页右栏的播放队列 =====
//
// 右栏默认是歌词。点「队列」标签后队列从右缘滑入、取代歌词；点「歌词」
// 标签歌词再淡回来。播放条的队列钮只负责非播放页的停靠栏。

// queueNPApp 造一个「正在播放页开着、右栏切到队列」的界面，队列里三首。
func queueNPApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	app, tester := npTestApp(t, color.RGBA{R: 255, A: 255})
	app.PL.EnqueueMany([]player.Song{
		{Mid: "m2", Title: "七里香", Artists: "周杰伦", Interval: 269},
		{Mid: "m3", Title: "夜曲", Artists: "周杰伦", Interval: 269},
	})
	app.npQueue = true
	// 右栏的滑入从被建出来的那一帧起算，先渲染一帧再等它落定。
	tester.Frame()
	tester.Advance(450 * time.Millisecond)
	tester.Frame()
	return app, tester
}

// TestNowPlayingQueueReplacesLyrics：切到队列后右栏是队列，封面保留在左栏，
// 歌词不再和队列同时出现。
func TestNowPlayingQueueReplacesLyrics(t *testing.T) {
	_, tester := queueNPApp(t)

	const title = "播放列表 · 3 首"
	r, ok := tester.Find(title)
	if !ok {
		t.Fatalf("正在播放页上看不到播放列表；当前帧文本：%v", tester.Texts())
	}
	// 右栏从封面列之后开始：内容层左右 56 内边距 + 340 封面列 + 56 列间距。
	if min := float32(npPadX + npSideW + npGap); r.X < min {
		t.Errorf("播放列表标题 X=%v，应在封面右侧的右栏里（≥ %v）", r.X, min)
	}
	if r.Y < npPadY || r.Y > npPadY+queueHeadH {
		t.Errorf("播放列表标题 Y=%v，应落在右栏头行（%v~%v）", r.Y, npPadY, npPadY+queueHeadH)
	}
	if tester.HasText("那一年") {
		t.Error("队列显示时歌词不应同时留在右栏里")
	}
}

// TestNowPlayingQueueSlidesInFromRight：点「队列」标签时，队列从右缘滑入，
// 而不是直接钉在终态。
func TestNowPlayingQueueSlidesInFromRight(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 255, A: 255})
	app.PL.EnqueueMany([]player.Song{
		{Mid: "m2", Title: "七里香", Artists: "周杰伦", Interval: 269},
	})
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	if err := tester.Click("显示播放队列"); err != nil {
		t.Fatalf("点不到队列标签：%v", err)
	}
	tester.Frame()
	start, ok := tester.Find("播放列表 · 2 首")
	if !ok {
		t.Fatalf("切到队列后看不到播放列表；当前帧文本：%v", tester.Texts())
	}
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	end, _ := tester.Find("播放列表 · 2 首")
	if start.X <= end.X {
		t.Errorf("队列出现时 X=%v，落定后 X=%v——没有从右缘滑入", start.X, end.X)
	}
}

// TestNowPlayingLyricsFadeBackIn：从队列点「歌词」标签，歌词淡回来、队列让位。
func TestNowPlayingLyricsFadeBackIn(t *testing.T) {
	_, tester := queueNPApp(t)

	if err := tester.Click("显示歌词"); err != nil {
		t.Fatalf("点不到歌词标签：%v", err)
	}
	tester.Frame()
	tester.Advance(350 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("那一年") {
		t.Fatalf("点歌词标签后歌词没有淡回来；当前帧文本：%v", tester.Texts())
	}
	if tester.HasText("播放列表 · 3 首") {
		t.Error("歌词显示时队列不应还留在右栏里")
	}
}

// TestNowPlayingQueueRowsAreClickable：右栏队列的行要收得到点击，而不是被
// 封面/背景层吃掉。
func TestNowPlayingQueueRowsAreClickable(t *testing.T) {
	app, tester := queueNPApp(t)

	if err := tester.Click("七里香"); err != nil {
		t.Fatalf("点不到队列里的行：%v", err)
	}
	tester.Frame()
	if cur, ok := app.PL.Current(); !ok || cur.Mid != "m2" {
		t.Fatalf("点了队列里的「七里香」后当前曲是 %q（ok=%v），应为 m2：点击被正在播放页吃掉了", cur.Mid, ok)
	}
}

// TestQueueDockSlidesInFromRight：非播放页的停靠队列栏从右缘外侧滑入，
// 与正在播放页右栏的队列是同一个动作。
func TestQueueDockSlidesInFromRight(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 255, A: 255})
	app.PL.EnqueueMany([]player.Song{
		{Mid: "m2", Title: "七里香", Artists: "周杰伦", Interval: 269},
	})
	app.npOpen = false
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	app.queueOpen = true
	tester.Frame()
	start, ok := tester.Find("播放列表 · 2 首")
	if !ok {
		t.Fatalf("打开停靠队列后看不到播放列表；当前帧文本：%v", tester.Texts())
	}
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	end, _ := tester.Find("播放列表 · 2 首")
	if start.X <= end.X {
		t.Errorf("停靠队列出现时 X=%v，落定后 X=%v——没有从右缘滑入", start.X, end.X)
	}
}

// TestQueueFallsBackToDockWhenNowPlayingCloses：收起正在播放页，队列还在，
// 而且回到顶栏下方的停靠栏——右栏只是队列在播放页上的形态。
func TestQueueFallsBackToDockWhenNowPlayingCloses(t *testing.T) {
	app, tester := queueNPApp(t)
	app.queueOpen = true

	app.npOpen = false
	tester.Frame()
	// 停靠栏从右缘外侧滑入，等它落定再找位置/点击。
	tester.Advance(400 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find("播放列表 · 3 首")
	if !ok {
		t.Fatalf("收起正在播放页后播放列表没了；当前帧文本：%v", tester.Texts())
	}
	if r.Y < 44 {
		t.Errorf("播放列表标题 Y=%v，收起正在播放页后应回到顶栏（46px）下方的停靠栏", r.Y)
	}
	if err := tester.Click("夜曲"); err != nil {
		t.Fatalf("点不到停靠栏里的队列行：%v", err)
	}
	tester.Frame()
	if cur, ok := app.PL.Current(); !ok || cur.Mid != "m3" {
		t.Fatalf("点了队列里的「夜曲」后当前曲是 %q（ok=%v），应为 m3", cur.Mid, ok)
	}
}
