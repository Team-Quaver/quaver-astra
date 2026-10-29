package appui

import (
	"image/color"
	"testing"
	"time"
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
	if !app.queueOpen {
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
