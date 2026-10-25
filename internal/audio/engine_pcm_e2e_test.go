package audio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEngineRealPCMCompat 是 MPV 兼容模式的端到端冒烟：mpv --ao=pcm 解码
// 进 FIFO，应用侧经 ALSA 出声。真机 + 有 mpv 才跑（QAA_TEST_MPV=1）。
func TestEngineRealPCMCompat(t *testing.T) {
	if os.Getenv("QAA_TEST_MPV") != "1" {
		t.Skip("设 QAA_TEST_MPV=1 开启真实 mpv 测试")
	}
	if _, err := mpvPath(); err != nil {
		t.Skip("本机无 mpv")
	}
	if _, err := newALSASpeaker(); err != nil {
		t.Skipf("无可用 ALSA 设备: %v", err)
	} else {
		// 探测用的设备立即还回去，引擎自己会再开一个。
		// （speakerFactory 由 init 挂好，这里只验证环境。）
	}

	wav := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(wav, 3); err != nil {
		t.Fatal(err)
	}

	e := NewCompatIf(true)
	t.Cleanup(e.Close)
	if !e.Available() {
		t.Fatal("mpv 不可用")
	}
	if err := e.OpenURL("file://"+wav, 0, 3, true); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && e.Position() < 0.3 {
		time.Sleep(50 * time.Millisecond)
	}
	if pos := e.Position(); pos < 0.3 {
		t.Fatalf("PCM 模式位置未推进: %v", pos)
	}
	if d := e.Duration(0); d < 2.5 || d > 3.5 {
		t.Fatalf("时长异常: %v", d)
	}

	e.Pause()
	time.Sleep(200 * time.Millisecond)
	if !e.Paused() {
		t.Fatal("暂停未生效")
	}

	// seek 触发重挂 FIFO：验证代次切换不串音、时间轴锚点正确。
	e.SeekTo(0.5, 3)
	time.Sleep(400 * time.Millisecond)
	if p := e.Position(); p < 1.2 || p > 2.2 {
		t.Fatalf("seek 落点异常: %v", p)
	}
}
