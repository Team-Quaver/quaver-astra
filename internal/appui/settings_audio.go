package appui

import (
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"

	"github.com/egoist/mygo/ui"
)

// 「音频输出」设置键。驱动/设备/独占/自适应采样率都持久化在这里；
// 非 ALSA 驱动的独占标记在读取与写入两侧都被强制清掉。
const (
	audioDriverKey       = "Audio.Driver"
	audioDeviceKey       = "Audio.DeviceID"
	audioExclusiveKey    = "Audio.Exclusive"
	audioAdaptiveRateKey = "Audio.AdaptiveRate"
)

// audioDeviceCache 是设备枚举的后台缓存：PipeWire registry 往返和 ALSA
// name hints 都不该在 UI 帧里跑。进入设置页触发一次刷新，之后按驱动缓存。
type audioDeviceCache struct {
	mu       sync.Mutex
	byDriver map[string][]audio.OutputDevice
	loading  map[string]bool
	at       time.Time
}

func (cache *audioDeviceCache) devices(a *App, driver string) ([]audio.OutputDevice, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.byDriver == nil {
		cache.byDriver = map[string][]audio.OutputDevice{}
		cache.loading = map[string]bool{}
	}
	if devs, ok := cache.byDriver[driver]; ok && time.Since(cache.at) < 30*time.Second {
		return devs, true
	}
	if !cache.loading[driver] {
		cache.loading[driver] = true
		go func() {
			devs, _ := a.PL.OutputDevices(driver)
			cache.mu.Lock()
			cache.byDriver[driver] = devs
			cache.loading[driver] = false
			cache.at = time.Now()
			cache.mu.Unlock()
			a.invalidate()
		}()
	}
	return nil, false
}

func (cache *audioDeviceCache) invalidate() {
	cache.mu.Lock()
	cache.byDriver = nil
	cache.mu.Unlock()
}

// settingsAudioOutput 渲染「音频输出」区：驱动选择（PipeWire/ALSA）、
// 设备菜单、ALSA 独占开关与自适应采样率。仅 Liino 后端显示；mpv 自己
// 管理输出，这里不出现。
func (a *App) settingsAudioOutput(c *ui.Context, t *ui.Theme) {
	if a.PL == nil || a.mpvBackendEnabled() {
		return
	}
	if _, _, _, ok := a.PL.OutputConfig(); !ok {
		return
	}
	ui.Divider(c)
	ui.Text(c, "音频输出").FontSize(fz(14)).FontWeight(700)

	driver := a.Conf.String(audioDriverKey, "")
	if driver == "" {
		driver = defaultAudioDriver()
	}
	a.audioDriverRow(c, t, driver)
	a.audioDeviceRow(c, t, driver)
	a.audioExclusiveRow(c, t, driver)
	a.audioAdaptiveRow(c, t)
}

// defaultAudioDriver 是「跟随系统」在菜单里的落点：Linux 用 PipeWire，
// 其他平台只有系统驱动可选，保持空值即可。
func defaultAudioDriver() string {
	if runtime.GOOS == "linux" {
		return "pipewire"
	}
	return ""
}

func (a *App) audioDriverRow(c *ui.Context, t *ui.Theme, driver string) {
	ids := []string{"pipewire", "alsa"}
	labels := []string{"PipeWire（系统混音）", "ALSA（独占硬件）"}
	if runtime.GOOS != "linux" {
		// Windows/macOS 的输出由系统驱动承担，没有可切换的第二驱动。
		return
	}
	curLabel := labels[0]
	for i, id := range ids {
		if id == driver {
			curLabel = labels[i]
		}
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "输出驱动").FontSize(fz(13)).Grow(1)
		sel := ui.Select(c, &curLabel, labels).Width(280)
		if sel.Changed() {
			for i, label := range labels {
				if label == curLabel {
					a.setAudioDriver(ids[i])
					break
				}
			}
		}
	})
	ui.Text(c, "PipeWire 走系统会话混音，可与其他应用同时出声；ALSA 直接打开硬件设备，"+
		"播放期间独占声卡（其他应用无声），适合追求源码率直出的场景。").
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
}

func (a *App) audioDeviceRow(c *ui.Context, t *ui.Theme, driver string) {
	devs, ready := a.settings.audioDevices.devices(a, driver)
	ids := []string{""}
	labels := []string{"默认设备"}
	for _, dev := range devs {
		ids = append(ids, dev.ID)
		label := dev.Name
		if dev.Default {
			label += "（默认）"
		}
		labels = append(labels, label)
	}
	cur := a.Conf.String(audioDeviceKey, "")
	curLabel := labels[0]
	for i, id := range ids {
		if id == cur {
			curLabel = labels[i]
		}
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "输出设备").FontSize(fz(13)).Grow(1)
		sel := ui.Select(c, &curLabel, labels).Width(280).Disabled(!ready)
		if sel.Changed() {
			for i, label := range labels {
				if label == curLabel {
					a.setAudioDevice(driver, ids[i])
					break
				}
			}
		}
	})
	if !ready {
		ui.Text(c, "正在枚举设备…").FontSize(fz(11.5)).TextColor(t.TextMuted)
	}
}

func (a *App) audioExclusiveRow(c *ui.Context, t *ui.Theme, driver string) {
	alsa := driver == "alsa"
	exclusive := alsa && a.Conf.Bool(audioExclusiveKey, false)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "ALSA 设备独占").FontSize(fz(13)).Grow(1)
		v := exclusive
		ui.Switch(c, &v).Disabled(!alsa)
		if v != exclusive {
			a.setAudioExclusive(driver, v)
		}
	})
	if alsa {
		ui.Text(c, "独占模式下 Liino 以 hw 设备直连声卡并禁用软件重采样；关闭后走 plughw，"+
			"保留系统混音兼容性，由设备支持的最近采样率协商。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	} else {
		ui.Text(c, "仅 ALSA 驱动支持设备独占；当前驱动下独占已关闭。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
}

func (a *App) audioAdaptiveRow(c *ui.Context, t *ui.Theme) {
	a.prefRow(c, "自适应采样率", func() bool {
		return a.Conf.Bool(audioAdaptiveRateKey, true)
	}, a.setAudioAdaptiveRate)
	hint := "跟随音源采样率运行整条管线（44.1 kHz 的 CD 音质不再重采样到 48 kHz）；" +
		"设备不支持时自动落到最近支持的采样率。"
	if rate := a.PL.OutputSampleRate(); rate > 0 {
		hint += "当前管线：" + strconv.Itoa(rate) + " Hz。"
	}
	ui.Text(c, hint).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
}

// setAudioDriver 切换驱动：设备选择回到该驱动默认，独占仅 ALSA 保留。
// 正在播放时 Liino 原地重建会话，进度不丢。
func (a *App) setAudioDriver(driver string) {
	if a.Conf == nil {
		return
	}
	exclusive := false
	if driver == "alsa" {
		exclusive = a.Conf.Bool(audioExclusiveKey, false)
	}
	a.Conf.Set(audioDriverKey, driver)
	a.Conf.Set(audioDeviceKey, "")
	a.Conf.Set(audioExclusiveKey, exclusive)
	a.settings.audioDevices.invalidate()
	if a.PL != nil {
		a.PL.SetOutput(driver, "", exclusive)
	}
	a.invalidate()
}

func (a *App) setAudioDevice(driver, deviceID string) {
	if a.Conf == nil {
		return
	}
	exclusive := driver == "alsa" && a.Conf.Bool(audioExclusiveKey, false)
	a.Conf.Set(audioDeviceKey, deviceID)
	if a.PL != nil {
		a.PL.SetOutput(driver, deviceID, exclusive)
	}
	a.invalidate()
}

func (a *App) setAudioExclusive(driver string, on bool) {
	if a.Conf == nil {
		return
	}
	if driver != "alsa" {
		on = false
	}
	a.Conf.Set(audioExclusiveKey, on)
	if a.PL != nil {
		deviceID := a.Conf.String(audioDeviceKey, "")
		a.PL.SetOutput(driver, deviceID, on)
	}
	a.invalidate()
}

func (a *App) setAudioAdaptiveRate(on bool) {
	if a.Conf == nil {
		return
	}
	a.Conf.Set(audioAdaptiveRateKey, on)
	if a.PL != nil {
		a.PL.SetAdaptiveRate(on)
	}
	a.invalidate()
}
