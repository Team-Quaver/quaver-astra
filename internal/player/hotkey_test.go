package player

import "testing"

// 播放/暂停快捷键分「切换」和「单向」两种：媒体键里 Play 与 Pause 各走各的。
// 单向那两个动作必须只在状态真的需要改变时才动引擎、才通知——
// 否则「暂停」键按第二下会把停止态又开播起来（那正是 toggle 的语义）。
func TestPlayAndPauseAreDirectional(t *testing.T) {
	eng := &fakeEngine{}
	p := New(stubBackend{}, eng, fakePrefs{})
	notify := 0
	p.OnNotify(func() { notify++ })
	p.queue = []Song{{Mid: "a"}}
	p.index = 0

	// 暂停态 → Play 续播，并通知一次。
	p.playing, eng.paused = false, true
	before := notify
	p.Play()
	if !p.playing || eng.Paused() {
		t.Fatalf("Play 之后 playing=%v paused=%v", p.playing, eng.Paused())
	}
	if notify == before {
		t.Error("Play 起播后应当通知一次")
	}

	// 已在播 → Play 不动作、也不通知。
	before = notify
	p.Play()
	if notify != before {
		t.Errorf("重复 Play 不该通知（多了 %d 次）", notify-before)
	}

	// 在播 → Pause 停下。
	before = notify
	p.Pause()
	if p.playing || !eng.Paused() {
		t.Fatalf("Pause 之后 playing=%v paused=%v", p.playing, eng.Paused())
	}
	if notify == before {
		t.Error("Pause 之后应当通知一次")
	}

	// 已暂停 → Pause 不动作、也不通知。
	before = notify
	p.Pause()
	if notify != before {
		t.Errorf("重复 Pause 不该通知（多了 %d 次）", notify-before)
	}
	if p.Playing() {
		t.Error("Playing() 不该在暂停后仍为真")
	}
}

// TestPlayStartsWhenIdle 覆盖「停止/没有当前曲」时按播放键：从队列第一首起播。
// 队列为空时则彻底不动作，避免快捷键凭空弹通知刷托盘。
func TestPlayStartsWhenIdle(t *testing.T) {
	p := New(stubBackend{}, &fakeEngine{}, fakePrefs{})
	p.queue = []Song{{Mid: "a", SongID: 1, Interval: 100}}
	p.index = -1
	p.Play()
	if got := p.Index(); got != 0 {
		t.Errorf("Play 应从第一首起播，index = %d", got)
	}

	empty := New(stubBackend{}, &fakeEngine{}, fakePrefs{})
	notified := 0
	empty.OnNotify(func() { notified++ })
	empty.Play()
	empty.Pause()
	if notified != 0 {
		t.Errorf("空队列的 Play/Pause 不该通知，实际 %d 次", notified)
	}
	if got := empty.Index(); got != -1 {
		t.Errorf("空队列的 index 应保持 -1，实际 %d", got)
	}
}

// TestPlayOrPauseStillToggles 是回归护栏：加了 Play/Pause 之后，
// 原来「播放/暂停」切换的口径不能变——它在停止态要起播，在播时要暂停。
func TestPlayOrPauseStillToggles(t *testing.T) {
	eng := &fakeEngine{}
	p := New(stubBackend{}, eng, fakePrefs{})
	p.queue = []Song{{Mid: "a"}}
	p.index = 0
	p.playing, eng.paused = true, false

	p.PlayOrPause()
	if p.playing || !eng.Paused() {
		t.Fatalf("在播时 toggle 应暂停：playing=%v paused=%v", p.playing, eng.Paused())
	}
	p.PlayOrPause()
	if !p.playing || eng.Paused() {
		t.Fatalf("暂停时 toggle 应恢复：playing=%v paused=%v", p.playing, eng.Paused())
	}
}
