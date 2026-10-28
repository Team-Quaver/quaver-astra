//go:build linux

package audio

import (
	"math"
	"testing"
	"time"
)

// TestPulseSpeakerSmoke 是真机冒烟：只在本机确实有 PulseAudio/PipeWire 服务器时跑，
// 无头环境（CI 的 ubuntu runner 没有 PA socket，也没有 /dev/snd）自动跳过。
//
// 这里刻意直连 newPulseSpeaker()，不用 newSpeakerFor("")：后者在 Pulse 拨号失败时会
// 兜底去开 ALSA hw 直通，于是"能不能跑"要看两台设备的脸色，失败时报的还是 ALSA 的
// /dev/snd 错误（CI 上就是这么炸的）。Pulse 优先于 ALSA 的分派顺序改由 fake 服务器
// 测试覆盖（TestPulseSpeakerFakeBackpressure），不依赖真声卡。
func TestPulseSpeakerSmoke(t *testing.T) {
	s, err := newPulseSpeaker()
	if err != nil {
		t.Skipf("本机无可用 PulseAudio 服务器: %v", err)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		t.Fatal("Start:", err)
	}
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
