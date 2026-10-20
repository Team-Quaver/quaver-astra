package appui

import (
	"image/color"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 正在播放页上的播放队列浮窗 =====
//
// 队列原先只有「内容区右侧的停靠栏」一种形态，而正在播放页是铺满内容区的
// 常驻浮层：开着队列再打开正在播放页，队列就被整页盖住；在正在播放页上点
// 播放条的队列钮，状态是切了、画面却毫无变化。现在正在播放页上的队列改走
// 浮窗（queueFloat），盖在页面上、也收得到点击。

// queueNPApp 造一个「正在播放页开着、播放列表也开着」的界面，队列里三首。
func queueNPApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	app, tester := npTestApp(t, color.RGBA{R: 255, A: 255})
	app.PL.EnqueueMany([]player.Song{
		{Mid: "m2", Title: "七里香", Artists: "周杰伦", Interval: 269},
		{Mid: "m3", Title: "夜曲", Artists: "周杰伦", Interval: 269},
	})
	app.queueOpen = true
	// 浮窗的右滑淡入从它被建出来的那一帧起算，先渲染一帧把它挂上，
	// 再等正在播放页的上滑淡入与它一起落定（两者都走 Transition）。
	tester.Frame()
	tester.Advance(450 * time.Millisecond)
	tester.Frame()
	return app, tester
}

// TestNowPlayingQueueIsOneFloatingPanel：队列面板只能建一份。停靠栏与浮窗
// 共用一份 ListState（滚动位置、拖拽换序），两个都建的话等于两棵列表抢同一
// 份状态，文字也会出现两遍。
func TestNowPlayingQueueIsOneFloatingPanel(t *testing.T) {
	_, tester := queueNPApp(t)

	const title = "播放列表 · 3 首"
	n := 0
	for _, s := range tester.Texts() {
		if s == title {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("播放列表标题出现 %d 次，应为 1 次（停靠栏与浮窗同时建了）", n)
	}

	r, ok := tester.Find(title)
	if !ok {
		t.Fatalf("正在播放页上看不到播放列表；当前帧文本：%v", tester.Texts())
	}
	// 1000 宽的窗口里，320 宽的浮窗右边距 16：标题在浮窗内再缩 12px 内边距。
	if min := float32(testWinW - 320 - 16); r.X < min {
		t.Errorf("播放列表标题 X=%v，应在窗口右侧的浮窗里（≥ %v）", r.X, min)
	}
	if r.Y > 60 {
		t.Errorf("播放列表标题 Y=%v，浮窗应悬在页面顶部（16px 边距 + 头行内）", r.Y)
	}
}

// TestNowPlayingQueueFloatCoversPage：浮窗要画在正在播放页【之上】。
//
// 封面是一整块 300px 宽的纯红（落在 x≈628..928）。只扫浮窗压住的那几行：
// 浮窗在时，纯红只剩封面左边那条约 36px 的缝；浮窗要是被整页压住，穿过
// 封面的这一行会是一整段 300px 的红。
func TestNowPlayingQueueFloatCoversPage(t *testing.T) {
	_, tester := queueNPApp(t)

	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	head := mustFind(t, tester, "播放列表 · 3 首")
	last := mustFind(t, tester, "夜曲")
	nearRed := func(x, y int) bool {
		c := at(img, x, y)
		return c.R >= 200 && c.G <= 32 && c.B <= 32
	}
	best := 0
	for y := int(head.Y) - 4; y <= int(last.Y+last.H)+4 && y < img.Bounds().Dy(); y++ {
		if y < 0 {
			continue
		}
		run := 0
		for x := 0; x < img.Bounds().Dx(); x++ {
			if nearRed(x, y) {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
	}
	// 队列行里 34px 的小封面、封面露出的一条 36px 左缝，都在这个阈值内。
	if best > 80 {
		t.Errorf("浮窗压住的那几行里最宽的纯红横段有 %dpx——封面（300px）还盖在播放列表浮窗上面", best)
	}
}

// TestNowPlayingQueueFloatRowsAreClickable：浮窗里的行要收得到点击，而不是被
// 底下的封面/歌词层吃掉（MyGo 的命中链取最上面那个元素，晚建的浮窗才拿得到）。
func TestNowPlayingQueueFloatRowsAreClickable(t *testing.T) {
	app, tester := queueNPApp(t)

	if err := tester.Click("七里香"); err != nil {
		t.Fatalf("点不到浮窗里的队列行：%v", err)
	}
	tester.Frame()
	if cur, ok := app.PL.Current(); !ok || cur.Mid != "m2" {
		t.Fatalf("点了队列里的「七里香」后当前曲是 %q（ok=%v），应为 m2：点击被正在播放页吃掉了", cur.Mid, ok)
	}
}

// TestQueueFallsBackToDockWhenNowPlayingCloses：收起正在播放页，队列还在，
// 而且回到顶栏下方的停靠栏——浮窗是这一页上的形态，不是队列的唯一形态。
func TestQueueFallsBackToDockWhenNowPlayingCloses(t *testing.T) {
	app, tester := queueNPApp(t)

	app.npOpen = false
	tester.Frame()

	r, ok := tester.Find("播放列表 · 3 首")
	if !ok {
		t.Fatalf("收起正在播放页后播放列表没了；当前帧文本：%v", tester.Texts())
	}
	if r.Y < 44 {
		t.Errorf("播放列表标题 Y=%v，收起正在播放页后应回到顶栏（44px）下方的停靠栏", r.Y)
	}
	if err := tester.Click("夜曲"); err != nil {
		t.Fatalf("点不到停靠栏里的队列行：%v", err)
	}
	tester.Frame()
	if cur, ok := app.PL.Current(); !ok || cur.Mid != "m3" {
		t.Fatalf("点了队列里的「夜曲」后当前曲是 %q（ok=%v），应为 m3", cur.Mid, ok)
	}
}
