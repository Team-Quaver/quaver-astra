package appui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"
)

// seekEngine 是可写播放位置的假引擎：Paused 恒为 false，心跳才会把
// Position 同步进 Player。
type seekEngine struct {
	loadEngine
	pos float64
}

func (e *seekEngine) Position() float64        { return e.pos }
func (e *seekEngine) Paused() bool             { return false }
func (e *seekEngine) SeekTo(frac, dur float64) { e.pos = frac * dur }

// TestLyricFollowGlides 守住换行跟随是「缓动滚过去」而不是瞬间跳位：
// 当前句变了之后，列表偏移应当从原位逐步走到目标，走完才停下。
func TestLyricFollowGlides(t *testing.T) {
	var qrc strings.Builder
	qrc.WriteString(`<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1"><Lyric_1 LyricType="1" Lyrics="`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&qrc, "&#10;[%d,1000]第%d句", (i+1)*1000, i+1)
	}
	qrc.WriteString(`" /></LyricInfo></QrcInfos>`)

	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, qrc.String(), "", eng)
	// 心跳（200ms）把假引擎位置同步进 Player；等到第 4 句真正成为当前句。
	app.PL.StartTicker()
	deadline := time.Now().Add(3 * time.Second)
	for app.PL.LyricIndex(app.PL.Position()) != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("播放位置未推进（pos=%.2f）", app.PL.Position())
		}
		time.Sleep(5 * time.Millisecond)
	}
	tester.Frame()

	if app.npFollow.pending {
		t.Fatal("当前句已经在视图里，不该退回瞬间跳转")
	}
	if !app.npFollow.active {
		t.Fatal("换行没有开始跟随动画")
	}
	from, to := app.npScroll.Y, app.npFollow.to
	if to <= from {
		t.Fatalf("目标偏移 %.1f 不大于起点 %.1f", to, from)
	}

	// 走到一半：偏移应当落在两端之间，而不是已经在终点。
	tester.Advance(160 * time.Millisecond)
	tester.Frame()
	mid := app.npScroll.Y
	if mid <= from || mid >= app.npFollow.to {
		t.Fatalf("动画中途偏移 %.1f 不在 (%.1f, %.1f) 之间", mid, from, app.npFollow.to)
	}

	// 动画时长 320ms：再推进一段后应当停在终点。
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	if app.npFollow.active {
		t.Fatal("动画超时仍未停下")
	}
	if got := app.npScroll.Y; got != app.npFollow.to {
		t.Fatalf("停下时偏移 %.1f，不等于目标 %.1f", got, app.npFollow.to)
	}
}
