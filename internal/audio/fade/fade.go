// Package fade provides the track-change fade presets and the linear
// amplitude envelope shared by Astra's audio backends.
//
// 音量包络必须作用在用户音量之后（输出侧），这样预解码的缓冲不会让
// 滑杆和淡入淡出慢半拍。引擎只负责把用户音量乘上当前包络值再交给
// 实际音频设备；这里不碰任何后端细节。
package fade

import (
	"math"
	"sync"
	"time"
)

// ConfigKey 是「淡入淡出」档位在 quaver-astra.json 里的键。
const ConfigKey = "Playing.Fade"

// DefaultPreset 与主项目一致：默认 0.4s（起播 400ms / 淡出 250ms）。
const DefaultPreset = "normal"

// Preset 是一档淡入淡出设置。In 是起播淡入时长，Out 是暂停/停止/切歌
// 时的淡出时长；两个值不必相同（显示档位按 In 命名）。
type Preset struct {
	Key   string
	Label string
	In    time.Duration
	Out   time.Duration
}

// Presets 是设置页展示的四档，顺序即展示顺序。
var Presets = []Preset{
	{Key: "off", Label: "0s", In: 0, Out: 0},
	{Key: "short", Label: "0.15s", In: 150 * time.Millisecond, Out: 120 * time.Millisecond},
	{Key: "normal", Label: "0.4s", In: 400 * time.Millisecond, Out: 250 * time.Millisecond},
	{Key: "long", Label: "0.8s", In: 800 * time.Millisecond, Out: 500 * time.Millisecond},
}

// Normalize 把未知/空值收敛到默认档位，避免旧配置或手改配置把音量
// 误留在某个中间档。
func Normalize(key string) string {
	for _, p := range Presets {
		if p.Key == key {
			return key
		}
	}
	// 兼容手写配置：设置页显示的是 0s / 0.15s / 0.4s / 0.8s，
	// 直接抄标签进配置文件也应该能认出来。
	switch key {
	case "0s":
		return "off"
	case "0.15s":
		return "short"
	case "0.4s":
		return "normal"
	case "0.8s":
		return "long"
	}
	return DefaultPreset
}

// Lookup 返回 key 对应的档位；未知值回落到默认档。
func Lookup(key string) Preset {
	key = Normalize(key)
	for _, p := range Presets {
		if p.Key == key {
			return p
		}
	}
	return Presets[2]
}

// Durations 返回档位的淡入/淡出时长。
func Durations(key string) (in, out time.Duration) {
	p := Lookup(key)
	return p.In, p.Out
}

// rampStep 与主项目一致：30ms 一步，线性包络。高频命令只是写不等回执，
// 不会把命令队列排满。
const rampStep = 30 * time.Millisecond

// Envelope 是线程安全的淡入淡出包络。调用方通过 New 注入 apply：
// apply 每次变化都收到当前包络（0..1），由各后端乘上用户音量后落到
// 输出增益。
//
// RampTo 的 pending 动作只能有一份：新的播放意图（换歌 load、恢复播放）
// 会作废旧动作，避免「淡出后 pause/stop」落到下一首上——mpv 的 stop
// 会卸载刚挂好的新流，Liino 同理。
type Envelope struct {
	mu      sync.Mutex
	level   float64
	target  float64
	in      time.Duration
	out     time.Duration
	pending func()
	seq     uint64
	closed  bool
	apply   func(level float64)
}

// New 创建包络。默认不淡入淡出（引擎单独使用时行为与旧版一致），
// 播放器会按设置调用 SetDurations。
func New(apply func(level float64)) *Envelope {
	return &Envelope{level: 1, target: 1, apply: apply}
}

// SetDurations 更新淡入/淡出时长。关闭淡入淡出时如果正处于淡入途中，
// 立即回到满音量；淡出途中有 pending 动作，不能在半路把它拉回去。
func (e *Envelope) SetDurations(in, out time.Duration) {
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.in, e.out = in, out
	snap := in == 0 && out == 0 && e.target == 1 && e.level < 1
	e.mu.Unlock()

	if snap {
		e.RampTo(1, 0, nil)
	}
}

// In 返回起播淡入时长。
func (e *Envelope) In() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.in
}

// Out 返回淡出时长。
func (e *Envelope) Out() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.out
}

// Level 返回当前包络值。
func (e *Envelope) Level() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.level
}

// Target 返回包络的目标值。
func (e *Envelope) Target() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.target
}

// RampTo 把包络线性推向 level，时长 dur。after 是淡出到点后要执行的
// 动作（nil = 不执行，并清掉旧动作）。level=1 总是清掉旧动作。
func (e *Envelope) RampTo(level float64, dur time.Duration, after func()) {
	level = clamp01(level)

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.target = level
	e.pending = after
	e.seq++
	seq := e.seq
	from := e.level
	apply := e.apply
	e.mu.Unlock()

	if dur <= 0 || math.Abs(from-level) < 0.002 {
		e.settle(seq, level)
		return
	}

	start := time.Now()
	go func() {
		ticker := time.NewTicker(rampStep)
		defer ticker.Stop()
		for range ticker.C {
			k := float64(time.Since(start)) / float64(dur)
			if k >= 1 {
				e.settle(seq, level)
				return
			}

			e.mu.Lock()
			if e.closed || e.seq != seq {
				e.mu.Unlock()
				return
			}
			e.level = from + (level-from)*k
			cur := e.level
			e.mu.Unlock()

			if apply != nil {
				apply(cur)
			}
		}
	}()
}

// BeginLoad 是新流挂载前的包络处理：作废旧动作（切歌时那条延迟 stop
// 不能落到新歌上）。如果已经在淡出，就沿用剩余时间继续降到 0；否则
// 立刻压到 0，让新流出声即从静音起步。
func (e *Envelope) BeginLoad() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	fadingOut := e.target == 0 && e.level > 0
	out := e.out
	e.mu.Unlock()

	if fadingOut {
		e.RampTo(0, out, nil)
		return
	}
	e.Reset(0)
}

// Reset 立即把包络放到 level，并清掉进行中的渐变与待执行动作。
func (e *Envelope) Reset(level float64) {
	level = clamp01(level)

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.seq++
	e.level, e.target = level, level
	e.pending = nil
	apply := e.apply
	e.mu.Unlock()

	if apply != nil {
		apply(level)
	}
}

// FinishFadeOut 在淡出途中收到 seek（MPRIS Stop = pause + seek(0)）时
// 立刻收尾：否则会先听见一小段歌头再静音。仅在目标为 0 时生效，
// 淡入途中不打断。
func (e *Envelope) FinishFadeOut() {
	e.mu.Lock()
	if e.closed || e.target != 0 {
		e.mu.Unlock()
		return
	}
	e.seq++
	seq := e.seq
	e.level, e.target = 0, 0
	act := e.pending
	e.pending = nil
	apply := e.apply
	e.mu.Unlock()

	if apply != nil {
		apply(0)
	}
	if act != nil && e.alive(seq) {
		act()
	}
}

// Close 取消渐变与待执行动作。幂等。
func (e *Envelope) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.seq++
	e.pending = nil
	e.mu.Unlock()
}

// settle 结束一次渐变：落到目标值、应用音量，再执行 pending 动作。
// 如果动作执行前已经出现更新的意图（seq 变化），旧动作作废。
func (e *Envelope) settle(seq uint64, level float64) {
	e.mu.Lock()
	if e.closed || e.seq != seq {
		e.mu.Unlock()
		return
	}
	e.level, e.target = level, level
	act := e.pending
	e.pending = nil
	apply := e.apply
	e.mu.Unlock()

	if apply != nil {
		apply(level)
	}
	if act != nil && e.alive(seq) {
		act()
	}
}

// alive 判断某次渐变是否仍是当前意图。动作执行前再确认一次，避免
// 淡出期间已经切歌/恢复播放后旧 stop/pause 落地。
func (e *Envelope) alive(seq uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.closed && e.seq == seq
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
