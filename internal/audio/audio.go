// Package audio 是纯 Go 的播放引擎：oto 输出 + go-mp3 / mewkiz-flac / oggvorbis 解码。
//
// IO 模型：mp3/flac 走 HTTP Range 流式解码（预缓冲后即播，seek = 按新偏移
// 重发 Range 请求重建解码链），ogg（变块长的库限制）整曲解码为 PCM。
// 支持 mp3（128/320）、flac、ogg-vorbis（320ogg/640ogg）档位；atmos 不在支持范围。
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
	"github.com/jfreymuth/oggvorbis"
	flac "github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

const (
	ctxRate = 48000
	ctxCh   = 2
)

// Engine 播放一路音频。全部方法并发安全。
//
// 解码链是不可变的：seek = 从整曲内存重建一条新链后原子换上（chainSwap），
// oto 混音协程每次 Read 取当前链，旧链自然排空——热路径无锁、无共享可变
// 状态，根治“seek 与混音并发改解码器状态”的数据竞争（表现为噪波）。
type Engine struct {
	mu      sync.Mutex   // 保护 op / paused / volume / kind / srcf / size / data / srcRate
	ctx     *oto.Context // nil = 无声设备（哑引擎）
	op      *oto.Player
	paused  bool
	volume  float64
	srcRate int
	kind    string // "mp3" | "flac" | "ogg"
	srcf    StreamSource
	size    int64  // mp3 seek 的字节总量（来自 resolve 探测）
	data    []byte // 仅 ogg：一次性解码后的 s16 PCM

	chain atomic.Pointer[streamChain]
}

// StreamSource 按 byte 偏移打开音频流（实现方发 HTTP Range 请求）。
type StreamSource func(offset int64) (io.ReadCloser, error)

// streamChain 是一条不可变解码链：解码器 →（重采样）→ 计数。
type streamChain struct {
	dec    any // *mp3.Decoder / *flacReader / *bytes.Reader（ogg）
	closer io.Closer
	cnt    *countReader
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

// Read 实现 io.Reader，oto 播放器以此为源；每次调用取当前解码链。
func (e *Engine) Read(p []byte) (int, error) {
	c := e.chain.Load()
	if c == nil {
		return 0, io.EOF
	}
	return c.cnt.Read(p)
}

// Load 载入一段完整内存音频（测试/工具用），等价于对它做 OpenStream。
func (e *Engine) Load(data []byte) error {
	srcf := func(off int64) (io.ReadCloser, error) {
		if off >= int64(len(data)) {
			return io.NopCloser(bytes.NewReader(nil)), nil
		}
		return io.NopCloser(bytes.NewReader(data[off:])), nil
	}
	return e.OpenStream(srcf, int64(len(data)), 0, 0, true)
}

// OpenStream 打开一路音频流：先嗅探 16 字节头部定格式（不可解码的流在
// 下载前就失败，调用方可降档），mp3/flac 流式解码即播，ogg 整曲解码。
// startFrac 是起始位置（0..1），autoplay=false 时打开后保持暂停。
func (e *Engine) OpenStream(srcf func(offset int64) (io.ReadCloser, error), size int64, startFrac, dur float64, autoplay bool) error {
	e.Stop()
	probe, err := srcf(0)
	if err != nil {
		return fmt.Errorf("音频流打开失败: %w", err)
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(probe, head)
	probe.Close()
	if n < 4 {
		return fmt.Errorf("音频数据为空")
	}
	kind := sniffKind(head)
	if kind == "" {
		return fmt.Errorf("不支持的音频格式（仅 mp3/flac/ogg-vorbis）")
	}
	e.mu.Lock()
	e.kind, e.srcf, e.size, e.data = kind, srcf, size, nil
	e.srcRate = 0
	e.mu.Unlock()

	if kind == "ogg" {
		// oggvorbis 的分块 Read 在变块长流上有越界问题且对畸形页会 panic：
		// 整曲一次性解码成 s16 PCM（recover 兜底），seek 退化为按字节偏移
		rc, err := srcf(0)
		if err != nil {
			return fmt.Errorf("ogg 下载失败: %w", err)
		}
		raw, derr := io.ReadAll(rc)
		rc.Close()
		if derr != nil {
			return fmt.Errorf("ogg 下载失败: %w", derr)
		}
		vals, format, derr := decodeOgg(raw)
		if derr != nil {
			return derr
		}
		e.mu.Lock()
		e.data = interleaveS16(vals, max(1, format.Channels))
		e.srcRate = format.SampleRate
		e.mu.Unlock()
	}

	ch, err := e.buildChain(startFrac, dur)
	if err != nil {
		e.mu.Lock()
		e.kind, e.srcf, e.size, e.data = "", nil, 0, nil
		e.srcRate = 0
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	e.chain.Store(ch)
	if e.ctx != nil {
		e.op = e.ctx.NewPlayer(e)
		e.op.SetVolume(e.volume)
	}
	if autoplay {
		e.paused = false
		if e.op != nil {
			e.op.Play()
		}
	} else {
		e.paused = true
		if e.op != nil {
			e.op.Pause()
		}
	}
	e.mu.Unlock()
	return nil
}

// sniffKind 按头部字节判定格式；"" = 不支持。
func sniffKind(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte("fLaC")):
		return "flac"
	case bytes.HasPrefix(head, []byte("OggS")):
		return "ogg"
	case bytes.HasPrefix(head, []byte("ID3")),
		len(head) > 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0: // mp3 帧同步
		return "mp3"
	}
	return ""
}

// buildChain 建一条起点为 frac（0..1）× dur 秒的解码链。
// mp3/flac 从 rangeReader 流式读（seek 由新的 Range GET 承担）；
// ogg 读整曲 PCM。必须在持有 e.mu 时调用（写 e.srcRate）。
func (e *Engine) buildChain(frac float64, dur float64) (*streamChain, error) {
	frac = math.Max(0, math.Min(1, frac))
	var src io.Reader
	var closer io.Closer
	switch e.kind {
	case "mp3":
		rr := newRangeReader(e.srcf, int64(frac*float64(e.size)))
		dec, err := mp3.NewDecoder(rr) // go-mp3 会跳过垃圾头同步到帧
		if err != nil {
			rr.Close()
			return nil, err
		}
		e.srcRate = dec.SampleRate()
		src, closer = dec, rr
	case "flac":
		rr := newRangeReader(e.srcf, 0) // 先过元数据
		st, err := flac.NewSeek(rr)
		if err != nil {
			rr.Close()
			return nil, err
		}
		if frac > 0 && st.Info.NSamples > 0 {
			if _, serr := st.Seek(uint64(frac * float64(st.Info.NSamples))); serr != nil {
				rr.Close()
				return nil, serr
			}
		}
		ch := int(st.Info.NChannels)
		if ch <= 0 {
			ch = 2
		}
		e.srcRate = int(st.Info.SampleRate)
		src = &flacReader{st: st, channels: ch, bits: int(st.Info.BitsPerSample)}
		closer = rr
	case "ogg":
		br := bytes.NewReader(e.data)
		if frac > 0 && br.Size() > 0 {
			off := int64(frac * float64(br.Size()))
			off -= off % 4 // 4 字节帧对齐
			_, _ = br.Seek(off, io.SeekStart)
		}
		src = br
	default:
		return nil, fmt.Errorf("没有可播放的音频")
	}
	// 链路：解码器 →（重采样）→ 计数。计数在 oto 侧，位置才是输出时间轴。
	var feed io.Reader = src
	if e.srcRate != ctxRate {
		feed = &resampler{src: src, ratio: float64(e.srcRate) / float64(ctxRate)}
	}
	cnt := &countReader{r: feed}
	// 位置以 seek 目标为基准重新累计
	if t := frac * dur; t > 0 {
		cnt.add(int64(t * float64(ctxRate*ctxCh*2)))
	}
	return &streamChain{dec: src, closer: closer, cnt: cnt}, nil
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
	if e.paused {
		return false
	}
	c := e.chain.Load()
	return c != nil && c.cnt.eof.Load()
}

// IsPlaying 报告是否在出声。
func (e *Engine) IsPlaying() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.paused && (e.op == nil || e.op.IsPlaying())
}

// Position 当前秒数（输出时间轴；oto 预读会让它领先实际播放，仅调试用）。
func (e *Engine) Position() float64 {
	c := e.chain.Load()
	if c == nil {
		return 0
	}
	return float64(c.cnt.n.Load()) / float64(ctxRate*ctxCh*2)
}

// SeekTo 跳到 frac（0..1）× dur 秒：整条解码链重建后原子换上。
func (e *Engine) SeekTo(frac, dur float64) {
	frac = math.Max(0, math.Min(1, frac))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.data == nil {
		return
	}
	ch, err := e.buildChain(frac, dur)
	if err != nil {
		return
	}
	e.chain.Store(ch)
	if e.op != nil {
		e.op.Reset() // 丢掉 oto 缓冲里的旧音频
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

// Detach 只断开 oto 播放器（保留解码链），供测试等需要直接消费解码输出的场景。
func (e *Engine) Detach() {
	e.mu.Lock()
	if e.op != nil {
		e.op.Reset()
		_ = e.op.Close()
		e.op = nil
	}
	e.mu.Unlock()
}

// Stop 停止并释放当前曲目（含流式链上的网络连接）。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.op != nil {
		e.op.Reset()
		_ = e.op.Close()
		e.op = nil
	}
	if c := e.chain.Load(); c != nil && c.closer != nil {
		_ = c.closer.Close() // 退休 rangeReader 的填充协程与 GET
	}
	e.chain.Store(nil)
	e.kind = ""
	e.srcf = nil
	e.size = 0
	e.data = nil
}

// rangeReader 把“按偏移重发 HTTP Range GET”适配成 io.ReadSeeker。
//
// 读是预缓冲的：首次消费前先缓冲 gate 字节（约几秒的音频）再放行，吸收
// 网络抖动；缓冲满 1MB 时回收已消费前缀。seek = 退休当前填充协程、按新
// 偏移重开 GET（服务端中继支持 Range），flac 的元数据读取与帧 seek 都
// 落在这个 Seeker 上。
type rangeReader struct {
	srcf StreamSource
	pos  int64
	gate int64

	mu      sync.Mutex
	cond    *sync.Cond
	rc      io.ReadCloser
	buf     []byte
	off     int
	eof     bool
	err     error
	started bool
	gen     int
}

func newRangeReader(srcf StreamSource, start int64) *rangeReader {
	r := &rangeReader{srcf: srcf, pos: start, gate: 512 << 10}
	r.cond = sync.NewCond(&r.mu)
	go r.fill()
	return r
}

// fill 在后台拉取数据进缓冲；每次世代（seek/Close）只有一个活跃 fill。
func (r *rangeReader) fill() {
	r.mu.Lock()
	gen, start, rc := r.gen, r.pos, r.rc
	r.mu.Unlock()
	if rc == nil {
		var err error
		rc, err = r.srcf(start)
		if err != nil {
			r.mu.Lock()
			if r.gen == gen {
				r.err = err
				r.cond.Broadcast()
			}
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		if r.gen != gen {
			r.mu.Unlock()
			rc.Close()
			return
		}
		r.rc = rc
		r.mu.Unlock()
	}
	for {
		chunk := make([]byte, 64<<10)
		n, err := rc.Read(chunk)
		r.mu.Lock()
		if r.gen != gen {
			r.mu.Unlock()
			return
		}
		if n > 0 {
			r.buf = append(r.buf, chunk[:n]...)
		}
		if err == io.EOF {
			r.eof = true
			r.cond.Broadcast()
			r.mu.Unlock()
			return
		}
		if err != nil {
			r.err = err
			r.cond.Broadcast()
			r.mu.Unlock()
			return
		}
		r.cond.Broadcast()
		r.mu.Unlock()
	}
}

func (r *rangeReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		// 预缓冲门：攒够 gate 字节（或流已结束）才开始供给
		for int64(len(r.buf)-r.off) < r.gate && !r.eof && r.err == nil && r.rc != nil {
			r.cond.Wait()
		}
		r.started = true
	}
	for len(r.buf) == r.off && !r.eof && r.err == nil {
		r.cond.Wait()
	}
	if len(r.buf) > r.off {
		n := copy(p, r.buf[r.off:])
		r.off += n
		r.pos += int64(n)
		if r.off > 1<<20 { // 回收已消费前缀
			r.buf = append([]byte(nil), r.buf[r.off:]...)
			r.off = 0
		}
		return n, nil // 出错的数据先供完，错误由下一次 Read 返回
	}
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

func (r *rangeReader) Seek(off int64, whence int) (int64, error) {
	r.mu.Lock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = r.pos + off
	default:
		r.mu.Unlock()
		return 0, fmt.Errorf("audio: rangeReader 不支持该 whence")
	}
	if abs < 0 {
		r.mu.Unlock()
		return 0, fmt.Errorf("audio: 负偏移")
	}
	// 退休当前填充协程，按新偏移重开
	r.gen++
	if r.rc != nil {
		r.rc.Close()
		r.rc = nil
	}
	r.buf, r.off, r.eof, r.err, r.started = nil, 0, false, nil, false
	r.pos = abs
	r.cond.Broadcast()
	r.mu.Unlock()
	go r.fill()
	return abs, nil
}

// Close 退休填充协程并关闭底层连接。
func (r *rangeReader) Close() error {
	r.mu.Lock()
	r.gen++
	if r.rc != nil {
		r.rc.Close()
		r.rc = nil
	}
	r.cond.Broadcast()
	r.mu.Unlock()
	return nil
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
		// 插值需要当前帧和下一帧
		if int(r.pos)*2+3 >= len(r.pending) {
			if err := r.pumpOne(); err != nil {
				if err != io.EOF {
					return out, err
				}
				// 尾部：不足两帧可插值时，把剩余的帧原样吐完
				if len(r.pending) >= 2 {
					l, rr := r.pending[0], r.pending[1]
					p[out], p[out+1], p[out+2], p[out+3] = byte(l), byte(l>>8), byte(rr), byte(rr>>8)
					out += 4
					r.pending = r.pending[2:]
					r.pos = 0
					continue
				}
				if out == 0 {
					return 0, io.EOF
				}
				return out, nil
			}
		}
		i := int(r.pos) * 2
		if i+3 >= len(r.pending) {
			continue // pump 后仍不够（EOF 已在上面处理），防御性兜底
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

// pumpOne 从源读入一帧（4 字节）；EOF（含半帧）返回 io.EOF。
func (r *resampler) pumpOne() error {
	frame := make([]byte, 4)
	if _, err := io.ReadFull(r.src, frame); err != nil {
		r.eof = true
		return io.EOF
	}
	l := int16(uint16(frame[0]) | uint16(frame[1])<<8)
	rr := int16(uint16(frame[2]) | uint16(frame[3])<<8)
	r.pending = append(r.pending, l, rr)
	return nil
}

// Reset 在底层跳转后清空缓冲状态。
func (r *resampler) Reset() {
	r.pending = r.pending[:0]
	r.pos = 0
	r.eof = false
}

// decodeOgg 整曲解码 ogg-vorbis。oggvorbis 对畸形页会 panic 而不是报错
// （这是不可信的网络字节流），统一 recover 成 error。
func decodeOgg(data []byte) (vals []float32, format *oggvorbis.Format, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ogg 流损坏（%v）", r)
		}
	}()
	return oggvorbis.ReadAll(bytes.NewReader(data))
}

// interleaveS16 把 ReadAll 的交错 float32 采样转成 s16le 双声道（单声道上混，
// 多于两声道混到前两路）。
func interleaveS16(vals []float32, ch int) []byte {
	frames := len(vals) / ch
	out := make([]byte, frames*4)
	j := 0
	for i := 0; i < frames; i++ {
		l := f2s16(vals[i*ch])
		var r int32
		if ch == 1 {
			r = l
		} else {
			r = f2s16(vals[i*ch+1])
		}
		out[j], out[j+1], out[j+2], out[j+3] = byte(l), byte(l>>8), byte(r), byte(r>>8)
		j += 4
	}
	return out
}

func f2s16(v float32) int32 {
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	return int32(v * 32767)
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
	// 畸形帧可能让解码库 panic：流中段损坏按提前结束处理（fail-quiet，不崩进程）
	defer func() {
		if r := recover(); r != nil {
			f.eof = true
		}
	}()
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
