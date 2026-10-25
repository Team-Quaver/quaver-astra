package audio

import (
	"errors"
	"fmt"
	"sync"
)

// pcmSpeaker 是 MPV 兼容模式的应用侧音频出口：mpv 只解码，48kHz/stereo 的
// 实时 PCM 交给这里的系统音频 API 出声。音量在写设备前乘上，捕获端
// （OBS/Discord）拿到的就是处理后的声音。
type pcmSpeaker interface {
	// Start 打开（或复用）设备流。同一 engine 只有一个并发消费者。
	Start() error
	// Play 写一段 interleaved float32 采样并阻塞到设备接受完毕。
	// 音量已由调用方乘好；实现不得再缩放。
	Play(samples []float32) error
	// Close 收掉设备流。幂等。
	Close()
}

// pcmFormat 是 MPV 兼容模式的固定 PCM 形状（见 engine_pcm.go）。
const (
	pcmRate     = 48000
	pcmChannels = 2
)

// newSpeakerFor 返回当前平台可用的实时输出。Linux 上默认 PipeWire/JACK
// （都讲 PulseAudio native 协议，走同一份纯 Go 客户端），显式 "alsa" 才
// 强制 hw 直通；其余平台只有一个出口，name 忽略。
func newSpeakerFor(name string) (pcmSpeaker, error) {
	switch name {
	case "", "auto", "pipewire", "jack", "pulse":
		return newDefaultSpeaker()
	case "alsa":
		return newALSASpeakerForced()
	default:
		return nil, fmt.Errorf("pcm: 不支持的音频输出 %q", name)
	}
}

// speakerFactory 是各平台实现挂接点（见 pcm_speaker_*.go）。
var speakerFactory func() (pcmSpeaker, error)

// newALSASpeakerForced 是显式 "alsa" 的分派点：Linux 由
// pcm_speaker_alsa.go 提供（强制 hw 直通，绕过 Pulse）；其他平台没有
// ALSA，直接报不支持。
var newALSASpeakerForced = func() (pcmSpeaker, error) {
	return nil, errors.New(`pcm: 当前平台不支持音频输出 "alsa"`)
}

func newDefaultSpeaker() (pcmSpeaker, error) {
	if speakerFactory == nil {
		return nil, errors.New("pcm: 当前平台没有可用的实时音频输出")
	}
	return speakerFactory()
}

// pcmVolume 线程安全地保存 0..1 的音量；写 PCM 前逐采样相乘。
type pcmVolume struct {
	mu sync.Mutex
	v  float64
}

func (p *pcmVolume) set(v float64) {
	p.mu.Lock()
	p.v = v
	p.mu.Unlock()
}

func (p *pcmVolume) get() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.v
}
