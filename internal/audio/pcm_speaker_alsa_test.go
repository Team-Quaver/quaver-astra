//go:build linux

package audio

import (
	"math"
	"testing"
	"time"
)

func TestALSASpeakerRealDevice(t *testing.T) {
	s, err := newALSASpeaker()
	if err != nil {
		t.Skipf("no usable ALSA playback: %v", err)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatal("Start:", err)
	}
	as, ok := s.(*alsaSpeaker)
	if !ok {
		t.Fatalf("期望 *alsaSpeaker，得到 %T", s)
	}
	// 写 0.5 秒 440Hz 正弦（48k/2ch）；ioctl 阻塞写应该用 ~0.5s。
	frames := pcmRate / 2
	samples := make([]float32, frames*pcmChannels)
	for i := 0; i < frames; i++ {
		v := float32(math.Sin(2*math.Pi*440*float64(i)/pcmRate) * 0.2)
		samples[i*pcmChannels] = v
		samples[i*pcmChannels+1] = v
	}
	start := time.Now()
	if err := s.Play(samples); err != nil {
		t.Fatal("Play:", err)
	}
	elapsed := time.Since(start)
	// 阻塞写的理论下限 = 音频时长 − 设备缓冲能预先吞掉的帧数（buffer 4×period ≈ 200ms），
	// 也就是约 300ms。原先把 300ms 写死当断言阈值，正好压在下限上，实测 299.9ms
	// 就会红；这里按下限算，再留 50ms 调度余量。
	audio := time.Duration(frames) * time.Second / pcmRate
	lead := time.Duration(as.buffer) * time.Second / pcmRate
	floor := audio - lead - 50*time.Millisecond
	if floor < 50*time.Millisecond {
		floor = 50 * time.Millisecond
	}
	if elapsed < floor {
		t.Fatalf("blocking write returned too fast (%v) — device clock not driving，期望 ≥ %v", elapsed, floor)
	}
	t.Logf("0.5s audio took %v via blocking write (buffer %d frames, floor %v)", elapsed, as.buffer, floor)
	// 二次写确认 underrun 恢复路径。
	if err := s.Play(samples[:4800*pcmChannels]); err != nil {
		t.Fatal("Play #2:", err)
	}
}
