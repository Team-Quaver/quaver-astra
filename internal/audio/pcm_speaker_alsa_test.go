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
	if elapsed < 300*time.Millisecond {
		t.Fatalf("blocking write returned too fast (%v) — device clock not driving", elapsed)
	}
	t.Logf("0.5s audio took %v via blocking write", elapsed)
	// 二次写确认 underrun 恢复路径。
	if err := s.Play(samples[:4800*pcmChannels]); err != nil {
		t.Fatal("Play #2:", err)
	}
}
