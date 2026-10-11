//go:build darwin

package smedia

import "testing"

// TestNowPlayingLifecycle 在真实 macOS 上跑通会话生命周期：Attach 前推送
// 只缓存、Attach 后回写、Close 幂等。系统投影依赖 .app 的 Bundle ID，
// 因此这里只保证调用路径不 panic/不泄漏（控制中心的可视验证见 README）。
func TestNowPlayingLifecycle(t *testing.T) {
	cmds := &Commands{
		Play:        func() {},
		Pause:       func() {},
		PlayPause:   func() {},
		Stop:        func() {},
		Next:        func() {},
		Prev:        func() {},
		Seek:        func(float64) {},
		SetPosition: func(float64, string) {},
	}
	ctrl := New("Quaver Astra", cmds)

	snap := Snapshot{
		HasTrack: true,
		TrackID:  "0039MnYb0qxYhV",
		Title:    "晴天",
		Artist:   "周杰伦",
		Album:    "叶惠美",
		Playing:  true,
		Position: 12.5,
		Duration: 269.3,
		Loop:     "all",
	}
	ctrl.Update(snap) // Attach 之前
	ctrl.AttachWindow(0)
	snap.Position = 30
	ctrl.Update(snap)
	ctrl.Update(Snapshot{HasTrack: false})
	ctrl.Close()
	ctrl.Close()
}
