//go:build linux

package audio

import (
	"math"
	"os"
	"testing"
	"time"
)

// 环境守卫：只在有 PA socket 的 Linux 桌面上跑；CI/无头环境自动跳过。
// 真机冒烟：要求本机有 PulseAudio/PipeWire socket，无头 CI 会因拨号失败退出。
// 0.5s 音频应耗时约 0.5s——REQUEST 流控在按 sink 消费时钟背压。
func TestPulseSpeakerSmoke(t *testing.T) {
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("XDG_RUNTIME_DIR 未设置，无 PulseAudio 环境")
	}
	s, err := newSpeakerFor("")
	if err != nil {
		t.Fatal("newSpeakerFor:", err)
	}
	if _, ok := s.(*pulseSpeaker); !ok {
		t.Fatalf("expected pulseSpeaker, got %T", s)
	}
	defer s.Close()
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
	t.Logf("0.5s audio took %v", time.Since(start))
}
