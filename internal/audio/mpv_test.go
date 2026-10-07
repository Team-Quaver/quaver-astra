package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEngineHelpers 覆盖 Engine 的纯函数侧：音量夹取、属性值解析、
// mpv 路径解析（QAA_MPV 覆盖）。这些是 IPC 协议层的地基。
func TestEngineHelpers(t *testing.T) {
	t.Run("clamp01", func(t *testing.T) {
		for _, c := range []struct{ in, want float64 }{
			{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1},
		} {
			if got := clamp01(c.in); got != c.want {
				t.Errorf("clamp01(%v) = %v, want %v", c.in, got, c.want)
			}
		}
	})

	t.Run("toFloat", func(t *testing.T) {
		if f, ok := toFloat(float64(3.5)); !ok || f != 3.5 {
			t.Errorf("float64: %v %v", f, ok)
		}
		if f, ok := toFloat(7); !ok || f != 7 {
			t.Errorf("int: %v %v", f, ok)
		}
		// mpv 的 time-pos 在暂停/无流时会给 null
		if _, ok := toFloat(nil); ok {
			t.Error("nil 不应被接受")
		}
		if _, ok := toFloat("x"); ok {
			t.Error("字符串不应被接受")
		}
	})

	t.Run("定位顺序", func(t *testing.T) {
		// 显式路径优先，且不替他纠错（让 spawn 报真实错误）
		t.Setenv("QAA_MPV", "/custom/mpv")
		t.Setenv("QAA_MPV_DIR", "")
		bin, err := resolveMPV()
		if err != nil || bin.Path != "/custom/mpv" || bin.Origin != "explicit" {
			t.Errorf("显式路径未生效: %+v %v", bin, err)
		}

		// 随包目录：造一个可执行文件，验证 AppRun 与裸 mpv 两种形态
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("QAA_MPV", "")
		t.Setenv("QAA_MPV_DIR", dir)
		bin, err = resolveMPV()
		if err != nil || bin.Origin != "bundled" {
			t.Errorf("随包目录未生效: %+v %v", bin, err)
		}

		// 目录里没有可执行文件时回落到 PATH，而不是报死
		t.Setenv("QAA_MPV_DIR", t.TempDir())
		bin, _ = resolveMPV()
		if bin.Origin != "PATH" {
			t.Errorf("应回落到 PATH，实际 %+v", bin)
		}
	})

	t.Run("ProbeMPV", func(t *testing.T) {
		t.Setenv("QAA_MPV", "/definitely/not/here/mpv")
		t.Setenv("QAA_MPV_DIR", "")
		if _, _, err := ProbeMPV(); err == nil {
			t.Error("指向不存在的路径时应报错")
		}
	})

	t.Run("childEnv", func(t *testing.T) {
		// 裸名字 → 沿用宿主环境，不设 LD_LIBRARY_PATH
		if env := childEnv("mpv"); env != nil {
			t.Error("裸名字不应改写环境")
		}
		// 目录形态：存在 lib 子目录才追加
		dir := t.TempDir()
		bin := filepath.Join(dir, "AppRun")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if env := childEnv(bin); env != nil {
			t.Error("没有 lib 目录时不应改写环境")
		}
		if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
			t.Fatal(err)
		}
		env := childEnv(bin)
		if env == nil {
			t.Fatal("有 lib 目录时应追加 LD_LIBRARY_PATH")
		}
		found := false
		for _, kv := range env {
			if strings.HasPrefix(kv, "LD_LIBRARY_PATH=") &&
				strings.Contains(kv, filepath.Join(dir, "lib")) {
				found = true
			}
		}
		if !found {
			t.Error("LD_LIBRARY_PATH 未包含随包的 lib 目录")
		}
	})
}

// TestEngineRealMPV 在装有 mpv 的机器上做真实往返：起播本地生成的 WAV，
// 验证位置随时长推进、暂停生效、seek 落点正确。
// 用 QAA_TEST_MPV=1 开启（CI 无 mpv，默认跳过）。
func TestEngineRealMPV(t *testing.T) {
	if os.Getenv("QAA_TEST_MPV") != "1" {
		t.Skip("设 QAA_TEST_MPV=1 开启真实 mpv 往返测试")
	}
	if _, err := mpvPath(); err != nil {
		t.Skip("本机无 mpv")
	}
	// 真实播放需要音频设备；无声卡（容器/CI 常见）时用 null AO 顶替，
	// 协议层与时间轴同样能被完整验证。
	if _, err := os.Stat("/dev/snd"); err == nil {
		t.Setenv("QAA_MPV_AO", "pulse")
	} else {
		t.Log("无声卡，改用 null AO 验证 IPC 与时间轴")
		t.Setenv("QAA_MPV_AO", "null")
	}

	wav := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(wav, 3); err != nil {
		t.Fatal(err)
	}

	e := New()
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
		t.Fatalf("位置未推进: %v", pos)
	}
	if d := e.Duration(0); d < 2.5 || d > 3.5 {
		t.Fatalf("时长异常: %v", d)
	}

	e.Pause()
	time.Sleep(200 * time.Millisecond)
	if !e.Paused() {
		t.Fatal("暂停未生效")
	}

	e.SeekTo(0.5, 3)
	time.Sleep(400 * time.Millisecond)
	if p := e.Position(); p < 1.2 || p > 2.2 {
		t.Fatalf("seek 落点异常: %v", p)
	}
}

// TestEnginePlaybackSequence 按 player 的真实调用顺序走一遍：
// 起播 → 位置推进 → 暂停（位置不得漂移）→ 恢复 → seek → 播完 eof → Stop 归零。
// 它守的是协议层的时序正确性——单条命令各自通过，整条链仍可能因为
// 读锁竞争、事件与响应交错而出错（历史上真的死锁过一次）。
// 用 QAA_TEST_MPV=1 开启；无声卡时自动改用 null AO。
func TestEnginePlaybackSequence(t *testing.T) {
	if os.Getenv("QAA_TEST_MPV") != "1" {
		t.Skip("设 QAA_TEST_MPV=1 开启真实 mpv 测试")
	}
	if _, err := mpvPath(); err != nil {
		t.Skip("本机无 mpv")
	}
	t.Setenv("QAA_MPV_AO", "null")

	wav := filepath.Join(t.TempDir(), "seq.wav")
	if err := writeTestWAV(wav, 2); err != nil {
		t.Fatal(err)
	}
	e := New()
	t.Cleanup(e.Close)
	if !e.Available() {
		t.Fatal("mpv 不可用")
	}

	if err := e.OpenURL("file://"+wav, 0, 2, true); err != nil {
		t.Fatal("open:", err)
	}
	waitFor(t, 3*time.Second, func() bool { return e.Position() > 0.2 })
	if e.Paused() {
		t.Error("起播后不应是暂停态")
	}
	if !e.IsPlaying() {
		t.Error("起播后 IsPlaying 应为 true")
	}

	e.Pause()
	time.Sleep(200 * time.Millisecond)
	if !e.Paused() {
		t.Fatal("Pause 未生效")
	}
	if e.IsPlaying() {
		t.Error("暂停时 IsPlaying 应为 false")
	}
	// 暂停期间 mpv 仍会推 time-pos 的最后一个值，位置不该继续走。
	p1 := e.Position()
	time.Sleep(400 * time.Millisecond)
	if d := e.Position() - p1; d > 0.15 {
		t.Errorf("暂停期间位置仍在推进: %v", d)
	}

	e.Play()
	time.Sleep(300 * time.Millisecond)
	if e.Paused() {
		t.Fatal("Play 未恢复")
	}

	e.SeekTo(0.5, 2)
	waitFor(t, 2*time.Second, func() bool {
		p := e.Position()
		return p > 0.8 && p < 1.4
	})
	if e.Ended() {
		t.Error("seek 到中段后不该报 eof")
	}

	// 播完：mpv 报 eof-reached，player 靠它切下一首
	e.SeekTo(0.97, 2)
	waitFor(t, 4*time.Second, func() bool { return e.Ended() })

	e.Stop()
	time.Sleep(200 * time.Millisecond)
	if e.Ended() {
		t.Error("Stop 后不该仍报 eof")
	}
	if e.Position() != 0 {
		t.Errorf("Stop 后位置应为 0，实际 %v", e.Position())
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("条件在超时内未满足")
}

// writeTestWAV 生成指定秒数的静音 44.1kHz 单声道 WAV。
func writeTestWAV(path string, seconds float64) error {
	const rate, ch, bits = 44100, 1, 16
	dataLen := int(rate*seconds) * ch * bits / 8
	buf := make([]byte, 0, 44+dataLen)
	put32 := func(v int) {
		buf = append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	put16 := func(v int) {
		buf = append(buf, byte(v), byte(v>>8))
	}
	buf = append(buf, "RIFF"...)
	put32(36 + dataLen)
	buf = append(buf, "WAVEfmt "...)
	put32(16)
	put16(1)
	put16(ch)
	put32(rate)
	put32(rate * ch * bits / 8)
	put16(ch * bits / 8)
	put16(bits)
	buf = append(buf, "data"...)
	put32(dataLen)
	buf = append(buf, make([]byte, dataLen)...)
	return os.WriteFile(path, buf, 0o644)
}
