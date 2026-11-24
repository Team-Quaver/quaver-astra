package appui

import (
	"image/color"
	"slices"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"
)

// TestPlayerBarProgressSpansWholeBar 是「整个控制条即进度条」的回归保护：
// 滑块就是整条播放条，不再是一条贴在窗口边缘、容易被裁掉的细轨。
func TestPlayerBarProgressSpansWholeBar(t *testing.T) {
	_, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find("播放进度")
	if !ok {
		t.Fatalf("seek rail not found; texts: %v", tester.Texts())
	}
	if r.H < barH-1 {
		t.Errorf("progress surface height = %.1f, want the whole %.0fpx control bar", r.H, barH)
	}
	if r.W < testWinW-2 {
		t.Errorf("progress surface width = %.1f, want the full window width %d", r.W, testWinW)
	}
}

func TestSeekFracSnapsToExactEndpoints(t *testing.T) {
	for _, tc := range []struct {
		x    float32
		want float32
	}{
		{-20, 0},
		{0, 0},
		{9, 0},
		{20, 0.02},
		{500, 0.5},
		{990, 1},
		{1000, 1},
		{1200, 1},
	} {
		if got := seekFracAt(tc.x, 1000); got != tc.want {
			t.Errorf("seekFracAt(%v, 1000) = %v, want %v", tc.x, got, tc.want)
		}
	}
	if got := seekFracAt(5, 0); got != 0 {
		t.Errorf("zero-width rail should seek to 0, got %v", got)
	}
}

func TestQualityLabelDefaultsToAuto(t *testing.T) {
	if got := qualityLabel("", false); got != "自动" {
		t.Fatalf("empty quality label = %q, want 自动", got)
	}
}

// TestPlayerBarTrackMenuStructure 钉住播放条歌曲信息区的右键菜单：
// 跳转歌手（多歌手用子菜单）/ 跳转专辑 / 同名搜索，顺序固定。
func TestPlayerBarTrackMenuStructure(t *testing.T) {
	_, _, tester := npMenuApp(t, "")
	if err := tester.RightClick("正在播放"); err != nil {
		t.Fatalf("右键播放条歌曲信息区失败：%v", err)
	}
	want := []string{"跳转歌手", "跳转专辑", "同名搜索"}
	if got := tester.Menu(); !slices.Equal(got, want) {
		t.Fatalf("右键菜单 = %v，期望 %v", got, want)
	}
	tester.CloseMenu()
}

// TestPlayerBarTrackMenuJumps 三组跳转落到对应路由，并先收起正在播放页
// （它是盖在内容区上的常驻浮层，只换路由仍会盖住目标页）。
func TestPlayerBarTrackMenuJumps(t *testing.T) {
	app, _, tester := npMenuApp(t, "")
	seedSinger(app)
	app.singers["s2"] = &singerState{loaded: true, name: "方文山"}
	seedAlbum(app)
	app.search.kw = "晴天"

	if err := tester.RightClick("正在播放"); err != nil {
		t.Fatalf("右键播放条歌曲信息区失败：%v", err)
	}
	if err := tester.ChooseMenuItem("跳转歌手", "方文山"); err != nil {
		t.Fatalf("选择子菜单歌手失败：%v", err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/singer/s2" {
		t.Errorf("跳转歌手后 path = %q，期望 /singer/s2", got)
	}
	if got := app.Router.Query("name"); got != "方文山" {
		t.Errorf("跳转歌手 name = %q，期望 方文山", got)
	}
	if app.npOpen {
		t.Error("跳转前应收起正在播放页")
	}

	reopenNP(t, app, tester)
	if err := tester.RightClick("正在播放"); err != nil {
		t.Fatalf("右键播放条歌曲信息区失败：%v", err)
	}
	if err := tester.ChooseMenuItem("跳转专辑"); err != nil {
		t.Fatalf("选择跳转专辑失败：%v", err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/album/al1" {
		t.Errorf("跳转专辑后 path = %q，期望 /album/al1", got)
	}

	reopenNP(t, app, tester)
	if err := tester.RightClick("正在播放"); err != nil {
		t.Fatalf("右键播放条歌曲信息区失败：%v", err)
	}
	if err := tester.ChooseMenuItem("同名搜索"); err != nil {
		t.Fatalf("选择同名搜索失败：%v", err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/search" {
		t.Errorf("同名搜索后 path = %q，期望 /search", got)
	}
	if got := app.Router.Query("kw"); got != "晴天" {
		t.Errorf("同名搜索 kw = %q，期望 晴天", got)
	}
}

// TestPlayerBarTrackMenuSingleSinger 单歌手时直接给菜单项，不套子菜单。
func TestPlayerBarTrackMenuSingleSinger(t *testing.T) {
	app, p, tester := npMenuApp(t, "")
	seedSinger(app)
	p.PlayList([]player.Song{{
		Mid: "m1", Name: "晴天", Artists: "周杰伦", Album: "叶惠美",
		AlbumMid: "al1", Interval: 269,
		Singers: []player.Singer{{Mid: "s1", Name: "周杰伦"}},
	}}, 0)
	tester.Frame()

	if err := tester.RightClick("正在播放"); err != nil {
		t.Fatalf("右键播放条歌曲信息区失败：%v", err)
	}
	if err := tester.ChooseMenuItem("跳转歌手"); err != nil {
		t.Fatalf("单歌手应可直接选择「跳转歌手」：%v", err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/singer/s1" {
		t.Errorf("跳转歌手后 path = %q，期望 /singer/s1", got)
	}
}

// TestQualityLabelAtmos2IsPremium 钉住 atmos2 的展示名：它对应 QQ 音乐的
// 「臻品音质 2.0」（Q000），不是 5.1/7.1 全景声，播放条不能缩写成「全景声」。
func TestQualityLabelAtmos2IsPremium(t *testing.T) {
	if got := qualityLabel("atmos2", false); got != "臻品2.0" {
		t.Fatalf("atmos2 label = %q, want 臻品2.0", got)
	}
}

// TestPlayerBarDragPastWindowEdges 守住「拖出窗口也能到 0 和结尾」：
// 指针越过窗口左右边界时，位置仍要按越界坐标夹到两端，而不是停在
// 最后一次窗口内的采样值上。
func TestPlayerBarDragPastWindowEdges(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find("播放进度")
	if !ok {
		t.Fatalf("seek rail not found; texts: %v", tester.Texts())
	}
	y := r.Y + r.H/2

	tester.Press(r.X+r.W*0.30, y)
	tester.Move(r.X-60, y)
	tester.Release(r.X-60, y)
	tester.Frame()
	if app.seekDragging {
		t.Fatal("拖出左边界后 seek 没有结束")
	}
	if app.seekFrac != 0 {
		t.Fatalf("拖出左边界 seek fraction = %v，期望 0", app.seekFrac)
	}

	tester.Press(r.X+r.W*0.70, y)
	tester.Move(r.X+r.W+60, y)
	tester.Release(r.X+r.W+60, y)
	tester.Frame()
	if app.seekFrac != 1 {
		t.Fatalf("拖出右边界 seek fraction = %v，期望 1", app.seekFrac)
	}
}

// TestPlayerBarCanDragToZero is the regression guard for issue #1. A drag
// that begins on the bar itself must keep receiving moves after it crosses
// the cover and transport buttons; otherwise it loses pressed and cannot
// reach 0:00. The outer 10px still snaps to exact endpoints.
func TestPlayerBarCanDragToZero(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find("播放进度")
	if !ok {
		t.Fatalf("seek rail not found; texts: %v", tester.Texts())
	}
	y := r.Y + r.H/2
	// Start in the empty part of the centre column, then drag left across
	// the cover, the favourite button and the transport controls.
	tester.Press(r.X+r.W*0.30, y)
	tester.Frame()
	tester.Move(r.X+2, y)
	tester.Frame()
	tester.Release(r.X+2, y)
	tester.Frame()

	if app.seekDragging {
		t.Fatal("seek drag did not finish")
	}
	if app.seekFrac != 0 {
		t.Fatalf("seek fraction = %v, want exact 0", app.seekFrac)
	}

	tester.Press(r.X+r.W*0.70, y)
	tester.Frame()
	tester.Move(r.X+r.W-2, y)
	tester.Frame()
	tester.Release(r.X+r.W-2, y)
	tester.Frame()
	if app.seekFrac != 1 {
		t.Fatalf("seek fraction at right edge = %v, want exact 1", app.seekFrac)
	}
}

// TestPlayerBarControlsStillWork 守住整条 bar 变成 slider 后按钮仍然可点：
// 按下按钮时手势必须留给按钮，而不是被全条 seek 面吃掉。
func TestPlayerBarControlsStillWork(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	tester.Advance(450 * time.Millisecond)
	tester.Frame()
	beforeSeek := app.seekFrac
	if err := tester.Click("播放队列"); err != nil {
		t.Fatalf("点不到队列按钮：%v", err)
	}
	tester.Frame()
	if !app.npQueue {
		t.Fatal("点击队列按钮被整条 seek 面吃掉了")
	}
	if app.seekFrac != beforeSeek {
		t.Fatal("点击控件不应同时触发整条 seek")
	}
}

// TestPlayerBarProgressFillUsesFullHeight 守住视觉还原：已播放部分的底色
// 必须铺满控制条高度，而不是只在中部画一条细线。
func TestPlayerBarProgressFillUsesFullHeight(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	tester.Advance(450 * time.Millisecond)
	app.seekDragging, app.seekFrac = true, 0.5
	tester.Frame()
	r, ok := tester.Find("播放进度")
	if !ok {
		t.Fatalf("seek surface not found; texts: %v", tester.Texts())
	}
	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	filled := at(img, int(r.X+80), int(r.Y+4))
	empty := at(img, int(r.X+r.W-80), int(r.Y+4))
	if absDiff(int(filled.R), int(empty.R))+absDiff(int(filled.G), int(empty.G))+absDiff(int(filled.B), int(empty.B)) < 15 {
		t.Fatalf("progress fill not visible across bar height: filled=%v empty=%v", filled, empty)
	}
}
