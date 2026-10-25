package audio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PCM 兼容模式（对齐上游 Quaver Music 的 MPVCompat）：mpv 仍负责解码/缓存/
// Range 拉流，但 --ao=pcm 把 48kHz/stereo/float32 的裸 PCM 写进本进程持有的
// 管道，由应用侧（pcmSpeaker）打开音频设备。音频会话因此归属 quaver-astra，
// OBS/Discord/Oopz 的窗口音频捕获能直接看到它。
//
// 与 native 直出的差异都在本文件内消化：
//   - ao_pcm 是无时钟输出：解码完就写，位置/缓冲以「已喂给设备的样本」为准。
//   - FIFO 生命周期跟曲走：每首（含 seek 重挂）换一条新 FIFO，旧会话的尾巴
//     由代次（pcmGen）物理隔离，不做「排空多久才算干净」的时序猜测。
//   - EOF 只说明 mpv 写完了 FIFO；要把尾巴也送进设备后位置才算到头。
const (
	pcmChunkFrames = 2400                      // 50ms；读 FIFO 与写设备的帧步长
	pcmAheadBytes  = pcmRate * pcmChannels * 4 // 设备侧最多提前 1s
)

// pcmModeTag 是 --ao=pcm 输出文件的 GOOS 差异封装。
//
// POSIX 用 mkfifo（mpv fopen 写端，这里 O_RDWR 读端：writer 关闭后不会立刻
// EOF，换曲时同名 FIFO 可复用）；Windows 用命名管道服务端（mpv 用 fopen 打开
// \\.\pipe\... 写端，这里建监听端收数据）。
type pcmPipe struct {
	path   string
	closed bool

	// POSIX
	fifo    *os.File
	tmpDir  string
	windows bool
	ln      net.Listener
	conn    net.Conn
	connMu  sync.Mutex

	onData func([]byte)
	onErr  func(error)
}

func newPCMPipe(onData func([]byte), onErr func(error)) (*pcmPipe, error) {
	p := &pcmPipe{onData: onData, onErr: onErr}
	if err := p.open(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *pcmPipe) open() error {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	tag := "quaver-astra-pcm-" + hex.EncodeToString(raw[:])
	if isWindows() {
		p.windows = true
		p.path = `\\.\pipe\` + tag
		ln, err := startWinPipeServer(p.path, func(c net.Conn) { p.attach(c) })
		if err != nil {
			return err
		}
		p.ln = ln
		return nil
	}
	dir, err := os.MkdirTemp("", "quaver-astra-pcm-")
	if err != nil {
		return err
	}
	p.tmpDir = dir
	p.path = filepath.Join(dir, tag)
	if err := makeFIFO(p.path); err != nil {
		return err
	}
	f, err := openFIFOReadWrite(p.path)
	if err != nil {
		return err
	}
	p.fifo = f
	go p.drainFile(f)
	return nil
}

func (p *pcmPipe) drainFile(f *os.File) {
	buf := make([]byte, pcmChunkFrames*pcmChannels*4)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			p.onData(buf[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !p.closed {
				p.onErr(err)
			}
			return
		}
	}
}

func (p *pcmPipe) attach(c net.Conn) {
	p.connMu.Lock()
	old := p.conn
	p.conn = c
	p.connMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go func() {
		buf := make([]byte, pcmChunkFrames*pcmChannels*4)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				p.onData(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}

func (p *pcmPipe) close() {
	if p.closed {
		return
	}
	p.closed = true
	if p.fifo != nil {
		_ = p.fifo.Close()
	}
	p.connMu.Lock()
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.connMu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
	}
	if p.tmpDir != "" {
		_ = os.RemoveAll(p.tmpDir)
	}
}

// pcmOutput 是引擎的兼容模式输出会话：一代一条 FIFO + 一个 speaker。
//
// 时间轴模型：ao_pcm 无时钟，解码完就写。sentSec 是「mpv 已写进 FIFO 的
// 时长」，playedSec 是「设备侧已消费的时长」——后者用墙钟向前追赶、上限
// 钳到 sentSec（mpv 暂停时 FIFO 断流，追赶自然停住）。位置 = pos + playedSec。
type pcmOutput struct {
	pipe    *pcmPipe
	speaker pcmSpeaker
	gen     uint64

	// 时间轴：pos 是时间轴起点锚（续播/seek 后不是 0）。
	pos       float64
	sentSec   float64
	playedSec float64
	eof       bool // mpv 已写完 FIFO（end-file eof）
	fed       bool // 已经往设备写过数据（音量包络/清理用）

	mu       sync.Mutex
	vol      pcmVolume
	lastTick time.Time
}

// beginGen 开新会话（start-file 到来时调用）。gen=0 表示无会话：
// 所有在途 PCM 都会被丢弃。pos 由 OpenURL 预先写入（续播起点）。
func (o *pcmOutput) beginPending(pos float64) {
	o.mu.Lock()
	o.gen = 0 // start-file 到来前在途数据全部作废
	o.pos = pos
	o.sentSec, o.playedSec = 0, 0
	o.eof, o.fed = false, false
	o.lastTick = time.Time{}
	o.mu.Unlock()
}

func (o *pcmOutput) beginGen(gen uint64) {
	o.mu.Lock()
	o.gen = gen
	o.sentSec, o.playedSec = 0, 0
	o.eof, o.fed = false, false
	o.lastTick = time.Time{}
	o.mu.Unlock()
}

// stop 会话作废（Engine.Stop / 引擎关闭）。
func (o *pcmOutput) stop() {
	o.mu.Lock()
	o.gen = 0
	o.mu.Unlock()
}

func (o *pcmOutput) markEOF() {
	o.mu.Lock()
	o.eof = true
	o.mu.Unlock()
}

func (o *pcmOutput) setVolume(v float64) { o.vol.set(v) }

func (o *pcmOutput) active() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.gen != 0
}

// position 返回设备侧已消费到的位置（秒）。
func (o *pcmOutput) position() float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if !o.lastTick.IsZero() {
		o.playedSec += now.Sub(o.lastTick).Seconds()
	}
	o.lastTick = now
	if o.playedSec > o.sentSec {
		// 墙钟追赶上限：mpv 暂停/缓冲时 FIFO 断流，不能越过已解码的量。
		o.playedSec = o.sentSec
	}
	return o.pos + o.playedSec
}

// ended 报告 mpv 侧 EOF 且尾巴已消费完（player 的自动切歌依据）。
func (o *pcmOutput) ended() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.gen == 0 || !o.eof {
		return false
	}
	now := time.Now()
	if !o.lastTick.IsZero() {
		o.playedSec += now.Sub(o.lastTick).Seconds()
	}
	o.lastTick = now
	if o.playedSec > o.sentSec {
		o.playedSec = o.sentSec
	}
	return o.sentSec-o.playedSec <= 0.05
}

func (o *pcmOutput) onData(chunk []byte) {
	o.mu.Lock()
	gen := o.gen
	vol := o.vol.get()
	o.mu.Unlock()
	if gen == 0 {
		return
	}
	// float32 interleaved；截齐帧边界。
	n := len(chunk) - len(chunk)%(pcmChannels*4)
	if n <= 0 {
		return
	}
	samples := make([]float32, n/4)
	for i := 0; i < n/4; i++ {
		off := i * 4
		bits := uint32(chunk[off]) | uint32(chunk[off+1])<<8 | uint32(chunk[off+2])<<16 | uint32(chunk[off+3])<<24
		samples[i] = float32FromBits(bits) * float32(vol)
	}
	o.mu.Lock()
	if o.gen != gen {
		o.mu.Unlock()
		return // 旧会话在途尾巴：直接丢弃
	}
	if err := o.speaker.Play(samples); err != nil {
		o.mu.Unlock()
		fmt.Fprintf(os.Stderr, "audio: pcm 输出失败: %v\n", err)
		return
	}
	o.fed = true
	o.sentSec += float64(len(samples)) / (pcmChannels * pcmRate)
	o.mu.Unlock()
}

func (o *pcmOutput) onErr(err error) {
	fmt.Fprintf(os.Stderr, "audio: pcm 管道错误: %v\n", err)
}

func float32FromBits(b uint32) float32 { return math.Float32frombits(b) }
