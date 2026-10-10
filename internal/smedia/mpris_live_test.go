//go:build linux

// MPRIS 往返测试：QAA_TEST_MPRIS=1 时连接会话总线，注册服务后用
// playerctl 验证元数据投影与命令回传（需要系统装有 playerctl）。
package smedia

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	liveBusName = "org.mpris.MediaPlayer2.QuaverAstra"
	livePlayer  = "QuaverAstra"
)

func playerctl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("playerctl", append([]string{"-p", livePlayer}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("playerctl %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMPRISLive(t *testing.T) {
	if e := exec.Command("playerctl", "--version").Run(); e != nil {
		t.Skip("playerctl 不可用")
	}
	if e := exec.Command("dbus-send", "--session", "--dest=org.freedesktop.DBus",
		"--type=method_call", "--print-reply", "/org/freedesktop/DBus",
		"org.freedesktop.DBus.ListNames").Run(); e != nil {
		t.Skip("无会话 D-Bus")
	}

	type evt struct {
		name string
		vals []float64
	}
	events := make(chan evt, 32)
	cmds := &Commands{}
	set := func(name string) func() {
		return func() { events <- evt{name: name} }
	}
	cmds.PlayPause = set("playpause")
	cmds.Play = set("play")
	cmds.Pause = set("pause")
	cmds.Stop = set("stop")
	cmds.Next = set("next")
	cmds.Prev = set("prev")
	cmds.Seek = func(d float64) { events <- evt{name: "seek", vals: []float64{d}} }
	cmds.SetPosition = func(p float64, id string) {
		events <- evt{name: "setpos", vals: []float64{p}}
	}
	cmds.SetVolume = func(v float64) { events <- evt{name: "volume", vals: []float64{v}} }
	cmds.SetLoop = func(m string) { events <- evt{name: "loop", vals: []float64{loopKey(m)}} }
	cmds.SetShuffle = func(on bool) {
		v := 0.0
		if on {
			v = 1
		}
		events <- evt{name: "shuffle", vals: []float64{v}}
	}

	ctrl := New("Quaver Astra", cmds)
	defer ctrl.Close()
	ctrl.Update(Snapshot{
		HasTrack: true,
		TrackID:  "0039MnYb0qxYhV",
		Title:    "晴天",
		Artist:   "周杰伦",
		Album:    "叶惠美",
		Playing:  true,
		Position: 12.5,
		Duration: 269.3,
		Volume:   0.8,
		Loop:     "all",
	})
	time.Sleep(300 * time.Millisecond)

	if names := playerctl(t, "--list-all"); !strings.Contains(names, livePlayer) {
		t.Fatalf("播放器未注册: %s", names)
	}
	if st := playerctl(t, "status"); st != "Playing" {
		t.Fatalf("status = %q, want Playing", st)
	}
	md := playerctl(t, "metadata")
	for _, want := range []string{"晴天", "周杰伦", "叶惠美", "0039MnYb0qxYhV"} {
		if !strings.Contains(md, want) {
			t.Fatalf("metadata 缺少 %q:\n%s", want, md)
		}
	}
	if ps, e := strconv.ParseFloat(playerctl(t, "position"), 64); e != nil || ps < 12 || ps > 13 {
		t.Fatalf("position = %v, want ~12s", ps)
	}

	expect := func(name string, check func(vals []float64)) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-events:
				if e.name == name {
					if check != nil {
						check(e.vals)
					}
					return
				}
				// 其他事件（例如播放器同步）忽略
			case <-deadline:
				t.Fatalf("等待事件 %s 超时", name)
			}
		}
	}

	playerctl(t, "next")
	expect("next", nil)
	playerctl(t, "previous")
	expect("prev", nil)
	playerctl(t, "play-pause")
	expect("playpause", nil)

	playerctl(t, "volume", "0.5")
	expect("volume", func(v []float64) {
		if v[0] < 0.49 || v[0] > 0.51 {
			t.Fatalf("volume = %v, want 0.5", v[0])
		}
	})

	playerctl(t, "loop", "playlist")
	expect("loop", func(v []float64) {
		if v[0] != loopKey("all") {
			t.Fatalf("loop = %v, want all(%v)", v[0], loopKey("all"))
		}
	})

	playerctl(t, "shuffle", "on")
	expect("shuffle", func(v []float64) {
		if v[0] != 1 {
			t.Fatalf("shuffle = %v, want on", v[0])
		}
	})

	playerctl(t, "position", "30")
	expect("setpos", func(v []float64) {
		if v[0] < 29 || v[0] > 31 {
			t.Fatalf("setpos = %v, want 30", v[0])
		}
	})

	// 相对 seek：playerctl 没有直接入口，用 dbus-send 触发。
	out, err := exec.Command("dbus-send", "--session",
		"--dest="+liveBusName, "--type=method_call", "--print-reply",
		"/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2.Player.Seek",
		"int64:5000000").CombinedOutput()
	if err != nil {
		t.Fatalf("Seek 调用失败: %v: %s", err, out)
	}
	expect("seek", func(v []float64) {
		if v[0] < 4.9 || v[0] > 5.1 {
			t.Fatalf("seek = %v, want 5s", v[0])
		}
	})
}

// loopKey 供测试把 LoopStatus 文本折算成可比较值。
func loopKey(mode string) float64 {
	switch mode {
	case "one":
		return 1
	case "all":
		return 2
	}
	return 0
}
