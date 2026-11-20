package audio

import (
	"github.com/team-quaver/liino/output"
)

// OutputSettings 是构造引擎时的输出配置（来自设置页的持久化键）。
// 零值跟随平台默认：Linux 上是 PipeWire 会话默认 sink。
type OutputSettings struct {
	Driver       string // "pipewire" / "alsa"；空 = 平台默认
	DeviceID     string // 具体设备 id；空 = 该驱动的默认端点
	Exclusive    bool   // ALSA hw 独占；其他驱动会被 Liino 强制关闭
	AdaptiveRate bool   // 跟随源码率/图时钟，而不是钉死 48 kHz
}

// NewWithOutput 按输出配置构造原生引擎。设备仍然惰性打开：构造不占声卡。
func NewWithOutput(settings OutputSettings) *Engine {
	cfg := defaultConfig()
	cfg.Driver = settings.Driver
	cfg.DeviceID = settings.DeviceID
	cfg.Exclusive = settings.Exclusive
	cfg.AdaptiveRate = settings.AdaptiveRate
	return newEngine(cfg)
}

// OutputDevice 是设置页设备菜单的一项。
type OutputDevice struct {
	ID      string // 传给 SetOutput 的稳定 id（"<driver>/<endpoint>"）
	Driver  string
	Name    string // 人类可读标签
	Default bool   // 该驱动的默认端点
}

// Devices 枚举指定驱动的可用输出端点。driver 为空时枚举全部可用驱动。
// 枚举只读系统设备表（PipeWire registry / ALSA name hints），不打开设备，
// 因此不会和会话服务器抢声卡。调用可能花上一两百毫秒，放后台 goroutine。
func (e *Engine) Devices(driver string) ([]OutputDevice, error) {
	e.mu.Lock()
	p := e.p
	closed := e.closed || e.initErr != nil
	e.mu.Unlock()
	if closed || p == nil {
		return nil, output.ErrUnavailable
	}
	var devs []output.Device
	var err error
	if driver == "" {
		devs, err = output.AllDevices()
	} else {
		devs, err = p.DevicesOf(driver)
	}
	if err != nil {
		return nil, err
	}
	out := make([]OutputDevice, 0, len(devs))
	for _, dev := range devs {
		if dev.Driver == "null" {
			continue
		}
		out = append(out, OutputDevice{ID: dev.ID, Driver: dev.Driver, Name: dev.Name, Default: dev.Default})
	}
	return out, nil
}

// SetOutput 切换输出驱动/设备并立即生效：正在播放时保持进度重建会话。
// exclusive 只对 ALSA 有效；Liino 对其他驱动强制关闭独占，因此从 ALSA
// 切回 PipeWire 时设备独占一定被释放。
func (e *Engine) SetOutput(driver, deviceID string, exclusive bool) error {
	e.mu.Lock()
	p := e.p
	closed := e.closed || e.initErr != nil
	e.mu.Unlock()
	if closed || p == nil {
		return output.ErrUnavailable
	}
	return p.SetOutput(driver, deviceID, exclusive)
}

// OutputConfig 报告当前生效的输出配置（驱动、设备、独占）。
func (e *Engine) OutputConfig() (driver, deviceID string, exclusive bool) {
	e.mu.Lock()
	p := e.p
	closed := e.closed || e.initErr != nil
	e.mu.Unlock()
	if closed || p == nil {
		return "", "", false
	}
	return p.OutputConfig()
}

// SampleRate 报告管线当前运行的采样率（设置页/关于页展示用）。
func (e *Engine) SampleRate() int {
	e.mu.Lock()
	p := e.p
	closed := e.closed || e.initErr != nil
	e.mu.Unlock()
	if closed || p == nil {
		return 0
	}
	return p.SampleRate()
}

// SetAdaptiveRate 切换自适应采样率。正在播放时下一段解码立即按新策略
// 产出；已打开的会话在码率变化点重建。
func (e *Engine) SetAdaptiveRate(on bool) {
	e.mu.Lock()
	p := e.p
	closed := e.closed || e.initErr != nil
	e.mu.Unlock()
	if closed || p == nil {
		return
	}
	p.SetAdaptiveRate(on)
}
