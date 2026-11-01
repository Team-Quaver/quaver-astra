// Package audio 是基于 Liino 的原生播放引擎。
//
// Liino（third_party/Liino）是 Quaver 的纯 Go 音频管线：解码、DSP、缓冲与
// 系统输出全部在本进程内完成，没有 mpv 子进程、没有 IPC、没有 CGO。
//
//	FFmpeg / 平台解码器 → 48 kHz float32 → DSP → 环形缓冲 → PipeWire / WASAPI / Core Audio
//
// 这就是 Liino 的 Native 模式：音频会话属于本进程（Windows 上按应用捕获
// 才能只录到 Astra 自己的声音），输出直连系统音频服务。解码后端留空，由
// Liino 按平台选择：Linux 走 decode/native（ffgo 动态加载系统 FFmpeg），
// Windows 优先 WMF，macOS 优先 AVFoundation，缺容器时再回退 ffgo。
//
// 本包把 Liino 的 Player 适配成 player.Engine：载入/失败以事件回报，位置
// 取 Liino 的高精度时钟，其余行为与旧引擎保持一致（player 不感知后端）。
package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/team-quaver/liino"
	"github.com/team-quaver/liino/decode"
)

// appName 是系统混音器里显示的名字；Windows 按应用捕获依赖它保持稳定。
const appName = "Quaver Astra"

// eventBuffer 是事件订阅队列长度。事件只用来翻转少量状态位，溢出时
// Liino 会丢弃最旧的，不影响正确性。
const eventBuffer = 64

// Engine 实现 player.Engine，把 Liino 的 Player 包装为异步载入引擎。
//
// 并发模型：Liino 的 Open/Seek/SetVolume 都是非阻塞的，事件在独立
// goroutine 里汇入本地状态；所有本地字段由 mu 保护。任何方法都不会持有
// mu 去调用 Liino（避免和音频回调/解码线程互等）。
type Engine struct {
	p *liino.Player

	// openMu 串行化载入与停止：载入代数必须在 Open 之前就确定下来，
	// 否则本地文件「事件先于 Open 返回」时 KindLoaded 会被当成旧代丢掉。
	openMu sync.Mutex

	mu           sync.Mutex
	initErr      error  // 构造失败（解码后端/配置不可用）；此后所有载入直接判死
	active       bool   // 当前是否有一次载入在等结果（Stop 后置 false）
	gen          uint64 // 当前载入代数，只采纳同代事件
	loaded       bool   // 本次载入已成形（KindLoaded）
	loadErr      string // 本次载入失败原因（空 = 正常）
	loadErrFatal bool   // 失败与音质档位无关（解码后端/输出设备不可用）
	wantPaused   bool   // 本地暂停意图（对齐旧引擎：本地 + 播放器状态取或）
	volume       float64
	closed       bool
	obs          func()

	events <-chan liino.Event
	cancel func()
	done   chan struct{}
}

// New 构造原生引擎。它不打开音频设备：Liino 在首次真正出声时才建立输出
// 会话，因此无声卡的机器也能正常构造（载入时报错并走降档/停止逻辑）。
func New() *Engine { return newEngine(defaultConfig()) }

func defaultConfig() liino.Config {
	cfg := liino.Config{
		// Decoder 留空 = 按源自动选择：Linux 的 native（ffgo）、Windows 的
		// WMF、macOS 的 AVF，OS 解不了的容器再回退 ffgo。显式写 "ffgo"
		// 可把整条链钉死在 native 后端（见 liino.decode.native）。
		AppName:    appName,
		StreamName: appName,
		// 切歌时 120ms 淡出，避免设备回调里的硬切。
		FadeOut: liino.DefaultFadeOut,
	}
	if os.Getenv("QAA_AUDIO_LOG") != "" {
		// 排障开关：把 Liino/FFmpeg 的诊断原样打到 stderr。
		cfg.Log = func(msg string) { fmt.Fprintln(os.Stderr, "audio:", msg) }
		return cfg
	}
	// 默认静音：拖动进度时 FLAC 的帧重同步告警是预期行为（Liino 会跳过
	// 损坏帧继续播），不能让它每拖一次就刷一遍终端。Log 非 nil 时 Liino
	// 会接管 FFmpeg 回调；没有 libffshim 的机器由 silenceFFmpegLogs 兜底。
	cfg.Log = func(string) {}
	silenceFFmpegLogs()
	return cfg
}

// newEngine 是测试钩子：允许换成 null 输出或无网络配置。
func newEngine(cfg liino.Config) *Engine {
	e := &Engine{volume: 1, done: make(chan struct{})}
	p, err := liino.New(cfg)
	if err != nil {
		e.initErr = fmt.Errorf("liino: %w", err)
		close(e.done)
		return e
	}
	e.p = p
	e.events, e.cancel = p.Events(eventBuffer)
	go e.loop()
	return e
}

// loop 把 Liino 事件折叠成本地状态。只采纳当前代次的事件：换歌后排队的
// 上一条载入尾巴（旧代的 failed/loaded）不会污染新一次判断。
func (e *Engine) loop() {
	defer close(e.done)
	for ev := range e.events {
		e.handle(ev)
	}
}

func (e *Engine) handle(ev liino.Event) {
	e.mu.Lock()
	if e.closed || !e.active || ev.Gen < e.gen {
		e.mu.Unlock()
		return
	}
	// Liino 在「初始 seek 失败」时会自行在目标位置重建解码器（reopenIf），
	// 这会递增载入代次。那不是换歌，而是同一次载入的延续，必须跟上去；
	// 旧载入的尾巴代次只会更小，已经被上面的 < 挡掉。
	if ev.Gen > e.gen {
		e.gen = ev.Gen
	}
	notify := false
	switch ev.Kind {
	case liino.KindLoaded:
		e.loaded = true
		e.loadErr, e.loadErrFatal = "", false
		notify = true
	case liino.KindFailed:
		e.loadErr = ev.Err
		e.loadErrFatal = fatalError(ev.Err)
		notify = true
	case liino.KindPlaying, liino.KindPaused, liino.KindReady, liino.KindEnded, liino.KindDeviceChanged:
		notify = true
	}
	fn := e.obs
	e.mu.Unlock()
	if notify && fn != nil {
		fn()
	}
}

// fatalError 区分「这条流打不开」与「整个后端没起来」。
//
// 前者（404/410、坏的容器、CDN 超时）应该交给 player 降档重试；后者
// （没装 FFmpeg、解码后端不可用、player 已关闭）换多少档都救不回来，
// 必须让 player 停止自动跳歌。
func fatalError(msg string) bool {
	m := strings.ToLower(msg)
	for _, frag := range []string{
		"no decoder backend",
		"backend unavailable",
		"no audio driver",
		"driver unavailable",
		"player closed",
		"invalid config",
		"unsupported platform",
	} {
		if strings.Contains(m, frag) {
			return true
		}
	}
	return false
}

// OpenURL 异步载入 url，并从 startFrac（0..1）处开始。
//
// 与旧引擎一致：命令立刻返回，结果通过 Loaded / LoadError 回报。
func (e *Engine) OpenURL(url string, startFrac, dur float64, autoplay bool) error {
	e.openMu.Lock()
	defer e.openMu.Unlock()

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("音频引擎已关闭")
	}
	if e.initErr != nil {
		err := e.initErr
		e.mu.Unlock()
		return err
	}
	p := e.p
	e.mu.Unlock()

	// Open 会做一次 nextLoad()，所以本次载入的代数就是当前代数 + 1。
	// 在调用前就登记，才能接住本地文件「loaded 事件先于 Open 返回」的情况。
	// 取代数不持 mu：p.Snapshot 要拿 Liino 的锁，不能嵌进我们的锁里。
	gen := p.Snapshot().Generation + 1

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("音频引擎已关闭")
	}
	e.active = true
	e.gen = gen
	e.loaded = false
	e.loadErr, e.loadErrFatal = "", false
	e.wantPaused = !autoplay
	e.mu.Unlock()

	at := startFrac * dur
	if at < 0.5 {
		at = 0
	}
	err := p.Open(context.Background(), decode.Source{URI: url}, liino.OpenOptions{
		StartAt:  time.Duration(at * float64(time.Second)),
		AutoPlay: autoplay,
	})
	if err != nil {
		e.mu.Lock()
		e.active = false
		e.loadErr = err.Error()
		e.loadErrFatal = fatalError(err.Error())
		e.mu.Unlock()
		return err
	}
	return nil
}

// Play 恢复播放。
func (e *Engine) Play() {
	e.mu.Lock()
	if e.closed || e.initErr != nil {
		e.mu.Unlock()
		return
	}
	e.wantPaused = false
	p := e.p
	e.mu.Unlock()
	_ = p.Play()
}

// Pause 暂停，但保留设备会话（恢复无缺口）。
func (e *Engine) Pause() {
	e.mu.Lock()
	if e.closed || e.initErr != nil {
		e.mu.Unlock()
		return
	}
	e.wantPaused = true
	p := e.p
	e.mu.Unlock()
	_ = p.Pause()
}

// Paused 报告是否暂停（本地意图与播放器状态取或）。
func (e *Engine) Paused() bool {
	e.mu.Lock()
	paused, p, closed := e.wantPaused, e.p, e.closed
	e.mu.Unlock()
	if closed || p == nil {
		return paused
	}
	return paused || p.State() == liino.StatePaused
}

// IsPlaying 报告是否正在真实出声。
func (e *Engine) IsPlaying() bool {
	e.mu.Lock()
	p, closed := e.p, e.closed
	e.mu.Unlock()
	if closed || p == nil {
		return false
	}
	return p.State() == liino.StatePlaying
}

// Ended 报告当前曲目是否播完。载入后发生的解码/设备故障同样视为结束，
// 让 player 的 200ms 心跳能推进队列而不是卡在一首已经死掉的曲子上。
func (e *Engine) Ended() bool {
	e.mu.Lock()
	p, closed, loaded := e.p, e.closed, e.loaded
	e.mu.Unlock()
	if closed || p == nil {
		return false
	}
	switch p.State() {
	case liino.StateEnded:
		return true
	case liino.StateFailed:
		return loaded
	}
	return false
}

// Position 当前可听位置（秒）。Liino 的时钟已经减掉输出延迟，歌词与进度条
// 不会因为 5 秒预解码缓冲而超前。
func (e *Engine) Position() float64 {
	e.mu.Lock()
	p, closed := e.p, e.closed
	e.mu.Unlock()
	if closed || p == nil {
		return 0
	}
	return p.Clock().Position.Seconds()
}

// Duration 当前曲目时长（秒）；元数据尚未就绪时回退到已知值。
func (e *Engine) Duration(fallback float64) float64 {
	e.mu.Lock()
	p, closed := e.p, e.closed
	e.mu.Unlock()
	if closed || p == nil {
		return fallback
	}
	if d := p.Snapshot().Duration.Seconds(); d > 0 {
		return d
	}
	return fallback
}

// SeekTo 跳到 frac（0..1）× dur 秒。Liino 的 seek 是异步的，失败会自行
// 在目标位置重建解码器（见 liino.rebuildAfterSeek）。
func (e *Engine) SeekTo(frac, dur float64) {
	e.mu.Lock()
	p, closed := e.p, e.closed
	e.mu.Unlock()
	if closed || p == nil {
		return
	}
	sec := clamp01(frac) * dur
	if sec < 0 {
		sec = 0
	}
	_ = p.SeekTo(sec)
}

// SetVolume 设置线性音量 0..1。
func (e *Engine) SetVolume(v float64) {
	v = clamp01(v)
	e.mu.Lock()
	if e.closed || e.initErr != nil {
		e.mu.Unlock()
		return
	}
	e.volume = v
	p := e.p
	e.mu.Unlock()
	p.SetVolume(v)
}

// SetStartVolume 是 player 的启动期快路径：Liino 在构造时就持有 Player，
// 不需要等后台启动握手，直接落到输出增益即可。
func (e *Engine) SetStartVolume(v float64) { e.SetVolume(v) }

// Stop 停止当前曲目并卸载数据源。
func (e *Engine) Stop() {
	// 与 OpenURL 共用一把锁：停止期间旧代的 loaded/failed 事件一律不采纳。
	e.openMu.Lock()
	defer e.openMu.Unlock()

	e.mu.Lock()
	if e.closed || e.initErr != nil {
		e.mu.Unlock()
		return
	}
	e.active = false
	e.loaded = false
	e.loadErr, e.loadErrFatal = "", false
	e.wantPaused = false
	p := e.p
	e.mu.Unlock()
	_ = p.Stop()
}

// Loaded 报告本次载入是否已成形（解码器成功打开）。这是「这一档能不能播」
// 的权威信号，不依赖时长元数据。
func (e *Engine) Loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loaded
}

// LoadError 返回本次载入失败的原因；空串表示当前无失败。
func (e *Engine) LoadError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadErr
}

// LoadErrorFatal 报告 LoadError 是否与音质档位无关。
func (e *Engine) LoadErrorFatal() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadErrFatal
}

// OnObserve 注册状态变化回调。只在载入/播放/暂停/结束/故障等粗粒度
// 状态上触发，不随逐帧事件重绘。
func (e *Engine) OnObserve(fn func()) {
	e.mu.Lock()
	e.obs = fn
	e.mu.Unlock()
}

// Version 返回 Liino 库版本（设置页展示用）。
func (e *Engine) Version() string { return liino.Version }

// Available 报告引擎是否可用（构造成功且未关闭）。
func (e *Engine) Available() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initErr == nil && !e.closed
}

// Close 停止播放并释放设备会话。幂等，应用退出时调用。
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.active = false
	p, cancel := e.p, e.cancel
	e.mu.Unlock()

	if cancel != nil {
		cancel() // 关闭事件通道，loop 退出（done 随之关闭）
	}
	if p != nil {
		_ = p.Close()
	}
	select {
	case <-e.done:
	case <-time.After(2 * time.Second):
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
