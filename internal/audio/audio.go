// Package audio 是纯 Go 的播放引擎：oto 输出 + go-mp3 / mewkiz-flac 解码。
// 整曲下载进内存后再解码（歌 3~15MB），换来 seek 简单可靠、无网络抖动问题。
// 轻量版只支持 mp3（128/320）与 flac 档位；ogg/atmos 不在支持范围。
package audio

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
	flac "github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

const (
	ctxRate = 48000
	ctxCh   = 2
)

// Engine 播放一路音频。除 Position/Paused/Ended/SetVolume 外的调用须在同一线程；
// 内部用 mu 保护，全部方法并发安全。
type Engine struct {
	mu     sync.Mutex
	ctx    *oto.Context // nil = 无声设备（哑引擎）
	op     *oto.Player
	dec    any          // *mp3.Decoder 或 *flacReader（无 Close 需求：源在内存）
	res    *resampler   // 采样率不匹配时的重采样层
	cnt    *countReader // 位置统计（统计 oto 拿到的 PCM）
	volume float64
	paused bool
}

// New 初始化 oto 上下文（48kHz 立体声 s16le；非 48k 源线性重采样）。
func New() *Engine {
	opts := &oto.NewContextOptions{SampleRate: ctxRate, ChannelCount: ctxCh, Format: oto.FormatSignedInt16LE}
	ctx, ready, err := oto.NewContext(opts)
	if err != nil {
		ctx = nil
	} else {
		go func() { <-ready }()
	}
	return &Engine{ctx: ctx, volume: 0.8}
}

// Load 停掉当前播放并载入一段完整音频（mp3 或 flac 字节）。
func (e *Engine) Load(data []byte) error {
	e.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(data) < 4 {
		return fmt.Errorf("音频数据为空")
	}
	var src io.Reader
	var rate int
	switch {
	case bytes.HasPrefix(data, []byte("fLaC")):
		st, err := flac.NewSeek(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("flac 打开失败: %w", err)
		}
		rate = int(st.Info.SampleRate)
		ch := int(st.Info.NChannels)
		if ch <= 0 {
			ch = 2
		}
		fr := &flacReader{st: st, channels: ch, bits: int(st.Info.BitsPerSample)}
		e.dec = fr
		src = fr
	default: // mp3（go-mp3 自行跳过 ID3/垃圾头）
		dec, err := mp3.NewDecoder(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("mp3 打开失败: %w", err)
		}
		rate = dec.SampleRate()
		e.dec = dec
		src = dec
	}
	// 链路：解码器 →（重采样）→ 计数 → oto。计数在 oto 侧，位置才是输出时间轴。
	var feed io.Reader = src
	if rate != ctxRate {
		e.res = &resampler{src: src, ratio: float64(rate) / float64(ctxRate)}
		feed = e.res
	} else {
		e.res = nil
	}
	e.cnt = &countReader{r: feed}
	if e.ctx != nil {
		e.op = e.ctx.NewPlayer(e.cnt)
		e.op.SetVolume(e.volume)
		e.op.Play()
	}
	return nil
}

// Play / Pause 恢复或暂停。
func (e *Engine) Play() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = false
	if e.op != nil {
		e.op.Play()
	}
}

func (e *Engine) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = true
	if e.op != nil {
		e.op.Pause()
	}
}

// Paused 报告是否处于暂停。
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// Ended 报告当前曲目是否已自然播完（读到 EOF 且不在暂停）。
func (e *Engine) Ended() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.paused && e.cnt != nil && e.cnt.eof.Load()
}

// IsPlaying 报告是否在出声。
func (e *Engine) IsPlaying() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.paused && (e.op == nil || e.op.IsPlaying())
}

// Position 当前秒数（输出时间轴）。
func (e *Engine) Position() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cnt == nil {
		return 0
	}
	return float64(e.cnt.n.Load()) / float64(ctxRate*ctxCh*2)
}

// SeekTo 跳到 frac（0..1）× dur 秒。
func (e *Engine) SeekTo(frac, dur float64) {
	frac = math.Max(0, math.Min(1, frac))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dec == nil {
		return
	}
	target := frac * dur
	switch dec := e.dec.(type) {
	case *mp3.Decoder:
		if total := dec.Length(); total > 0 {
			_, _ = dec.Seek(int64(frac*float64(total)), io.SeekStart)
		}
	case *flacReader:
		if st := dec.st; st != nil && st.Info.NSamples > 0 {
			_, _ = st.Seek(uint64(frac * float64(st.Info.NSamples)))
		}
	}
	if e.res != nil {
		e.res.Reset()
	}
	if e.cnt != nil {
		e.cnt.reset()
	}
	// 位置以 seek 目标为基准重新累计
	e.cnt.add(int64(target * float64(ctxRate*ctxCh*2)))
	if e.op != nil {
		e.op.Reset()
		if !e.paused {
			e.op.Play()
		}
	}
}

// SetVolume 0..1。
func (e *Engine) SetVolume(v float64) {
	e.mu.Lock()
	e.volume = clamp01(v)
	if e.op != nil {
		e.op.SetVolume(e.volume)
	}
	e.mu.Unlock()
}

// Stop 停止并释放当前曲目。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.op != nil {
		e.op.Reset()
		_ = e.op.Close()
		e.op = nil
	}
	if fr, ok := e.dec.(*flacReader); ok && fr != nil {
		_ = fr.Close()
	}
	e.dec = nil
	e.res = nil
	e.cnt = nil
}

// countReader 统计 oto 已消费的 PCM 字节；读到 EOF 时置位（原子）。
type countReader struct {
	r   io.Reader
	n   atomic.Int64
	eof atomic.Bool
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	if err == io.EOF {
		c.eof.Store(true)
	}
	return n, err
}

func (c *countReader) reset()      { c.n.Store(0); c.eof.Store(false) }
func (c *countReader) add(v int64) { c.n.Add(v) }

// resampler 把 s16le 交错双声道从 ratio 倍源采样率线性重采样到目标率。
type resampler struct {
	src     io.Reader
	ratio   float64 // srcRate / dstRate
	pending []int16 // 未消费的源采样（交错 L,R）
	pos     float64 // 输出位置，以 pending[0] 起算的源采样为单位
	eof     bool
}

func (r *resampler) Read(p []byte) (int, error) {
	out := 0
	for out+4 <= len(p) {
		if err := r.ensure(); err != nil {
			if err == io.EOF {
				// 尾部：把剩余样本原样吐完
				if len(r.pending) >= 2 && out == 0 {
					l, rr := r.pending[0], r.pending[1]
					p[out], p[out+1], p[out+2], p[out+3] = byte(l), byte(l>>8), byte(rr), byte(rr>>8)
					out += 4
					r.pending = r.pending[2:]
					continue
				}
				if out == 0 {
					return 0, io.EOF
				}
				return out, nil
			}
			return out, err
		}
		i := int(r.pos) * 2
		if i+3 >= len(r.pending) {
			continue // 样本不够，继续 pump
		}
		frac := float32(r.pos - math.Floor(r.pos))
		l := int16(float32(r.pending[i])*(1-frac) + float32(r.pending[i+2])*frac)
		rr := int16(float32(r.pending[i+1])*(1-frac) + float32(r.pending[i+3])*frac)
		p[out], p[out+1], p[out+2], p[out+3] = byte(l), byte(l>>8), byte(rr), byte(rr>>8)
		out += 4
		r.pos += r.ratio
		// 丢弃已整段消费的头部
		if drop := int(r.pos); drop > 0 {
			r.pending = r.pending[drop*2:]
			r.pos -= float64(drop)
		}
	}
	return out, nil
}

// ensure 保证 pending 里至少有两帧（当前帧 + 下一帧）可插值。
func (r *resampler) ensure() error {
	for int(r.pos)*2+3 >= len(r.pending) {
		if r.eof {
			if len(r.pending) == 0 {
				return io.EOF
			}
			return nil // 尾部停留，Read 里按残余处理
		}
		frame := make([]byte, 4)
		if _, err := io.ReadFull(r.src, frame); err != nil {
			r.eof = true
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				return err
			}
			if len(r.pending) == 0 {
				return io.EOF
			}
			return nil
		}
		l := int16(uint16(frame[0]) | uint16(frame[1])<<8)
		rr := int16(uint16(frame[2]) | uint16(frame[3])<<8)
		r.pending = append(r.pending, l, rr)
	}
	return nil
}

// Reset 在底层跳转后清空缓冲状态。
func (r *resampler) Reset() {
	r.pending = r.pending[:0]
	r.pos = 0
	r.eof = false
}

// flacReader 把 mewkiz/flac 的 Stream 适配成 s16le 交错双声道 io.Reader。
type flacReader struct {
	st       *flac.Stream
	channels int // 源声道数（单声道复制为双声道）
	bits     int // 源位深
	buf      []byte
	off      int
	eof      bool
}

func (f *flacReader) Read(p []byte) (int, error) {
	for f.off >= len(f.buf) {
		if f.eof {
			return 0, io.EOF
		}
		frame, err := f.st.Next()
		if err != nil || frame == nil {
			f.eof = true
			if len(f.buf) == 0 {
				return 0, io.EOF
			}
			break
		}
		if err := frame.Parse(); err != nil && len(f.buf) == 0 {
			f.eof = true
			return 0, io.EOF
		} else if err != nil {
			f.eof = true
			break
		}
		f.buf = append(f.buf[:0], encodeS16(frame, f.channels, f.bits)...)
		f.off = 0
	}
	n := copy(p, f.buf[f.off:])
	f.off += n
	return n, nil
}

func (f *flacReader) Close() error { return f.st.Close() }

// encodeS16 把 flac frame 转成 s16le 交错双声道（位深折算到 16bit，多声道混到前两路）。
func encodeS16(frame *frame.Frame, channels, bits int) []byte {
	ch := len(frame.Subframes)
	if ch == 0 {
		return nil
	}
	ns := len(frame.Subframes[0].Samples)
	use := ch
	if use > 2 {
		use = 2
	}
	if channels == 1 {
		use = 1
	}
	shift := 0
	if bits > 16 {
		shift = bits - 16
	} else if bits > 0 && bits < 16 {
		shift = -(16 - bits)
	}
	out := make([]byte, 0, ns*2*2)
	for i := 0; i < ns; i++ {
		var l, r int32
		if use == 1 {
			l = scale(frame.Subframes[0].Samples[i], shift)
			r = l
		} else {
			l = scale(frame.Subframes[0].Samples[i], shift)
			r = scale(frame.Subframes[1].Samples[i], shift)
		}
		out = append(out, byte(l), byte(l>>8), byte(r), byte(r>>8))
	}
	return out
}

func scale(v int32, shift int) int32 {
	if shift > 0 {
		return v >> uint(shift)
	}
	if shift < 0 {
		return v << uint(-shift)
	}
	return v
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
