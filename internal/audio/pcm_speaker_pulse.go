//go:build linux

package audio

// PulseAudio native 协议客户端（纯 Go，无 cgo）：连 $XDG_RUNTIME_DIR/pulse/native
// unix socket，音频流注册成 PulseAudio/PipeWire 上的一个普通 client——混音、
// 按应用音量、OBS 捕获全部走桌面音频栈，不会像 /dev/snd hw 直通那样把
// PipeWire sink 踢下线、占住整块声卡。
//
// 只实现播放所需的最小协议面（握手 → 建流 → 写 memblock），帧格式与编码
// 逐字段对照 pulseaudio 源码（pstream.c / tagstruct.c / protocol-native.c），
// 协议版本钉在 32：AUTH 回包跳过 SHM 协商，CREATE 回包按固定字段序读。
import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// paProtoVersion 钉在 13：实测（PipeWire 1.6.10）AUTH 报 34 时服务器按
	// v13+ 的扩展字段路径解析 CREATE，会回 EPROTO；13 是可选 flag 段最短的
	// 版本，AUTH 回包的 server 版本（35）不代表我们声称的客户端版本。
	paProtoVersion = 13

	paCmdError          = 0
	paCmdReply          = 2
	paCmdCreatePlayback = 3
	paCmdDeletePlayback = 4
	paCmdAuth           = 8
	paCmdSetClientName  = 9
	paCmdDrain          = 12
	paCmdCork           = 41
	paCmdFlush          = 42
	paCmdTrigger        = 43
	paCmdPrebuf         = 60
	paCmdRequest        = 61 // 服务器→客户端：可写 N 字节
	paCmdUnderflow      = 63
	paCmdPlaybackKilled = 64
	paCmdStarted        = 86

	paSampleFloat32LE = 5

	paChannelFrontLeft  = 1
	paChannelFrontRight = 2

	paTagString     = 't'
	paTagStringNull = 'N'
	paTagU32        = 'L'
	paTagU8         = 'B'
	paTagArbitrary  = 'x'
	paTagTrue       = '1'
	paTagFalse      = '0'
	paTagProplist   = 'P'

	paDescriptorSize = 20 // 5×u32：length, channel, offset_hi, offset_lo, flags

	paCookieLen = 256
)

// paConn 是一条 native 连接。写侧（tagstruct 命令 + memblock）由 paWriter
// 串行化；读侧 goroutine 收 REQUEST/UNDERFLOW/应答并驱动写许可。
type paConn struct {
	c        net.Conn
	protocol uint32
	cookie   []byte

	writeMu sync.Mutex
	tag     atomic.Uint32
	seq     atomic.Uint32

	mu       sync.Mutex
	replies  map[uint32]chan *paPacket // tag → 应答
	requests chan uint32               // channel → 可写字节数
	closed   chan struct{}
}

type paPacket struct {
	channel uint32
	payload []byte
}

func dialPulseAudio() (*paConn, error) {
	runtime, ok := os.LookupEnv("XDG_RUNTIME_DIR")
	if !ok || runtime == "" {
		return nil, errors.New("pcm: XDG_RUNTIME_DIR 未设置，找不到 PulseAudio socket")
	}
	c, err := net.Dial("unix", filepath.Join(runtime, "pulse", "native"))
	if err != nil {
		return nil, fmt.Errorf("pcm: 连接 PulseAudio 失败: %w", err)
	}
	cookie, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "pulse", "cookie"))
	if err != nil || len(cookie) != paCookieLen {
		// 无 cookie 时 AUTH 退回同 uid 判定（本地 socket 下大多数服务器接受）。
		cookie = make([]byte, paCookieLen)
	}
	p := &paConn{
		c:        c,
		protocol: paProtoVersion,
		cookie:   cookie,
		replies:  map[uint32]chan *paPacket{},
		requests: make(chan uint32, 8),
		closed:   make(chan struct{}),
	}
	p.tag.Store(1)
	go p.readLoop()
	return p, nil
}

// auth 握手：version|0x80000000（不开 SHM）。cookie 读 ~/.config/pulse/cookie
// （PipeWire 复用同一份）；同时按协议要求随帧附 SCM_CREDENTIALS。
func (p *paConn) auth() error {
	t := newTAG()
	t.u32(p.protocol | 0x80000000)
	t.arbitrary(p.cookie)
	reply, err := p.command(paCmdAuth, t)
	if err != nil {
		return err
	}
	// version 带 'L' tag（'L' + 4B），跟其余应答字段一致
	if len(reply) < 5 || reply[0] != paTagU32 {
		return errors.New("pcm: PulseAudio AUTH 应答异常")
	}
	serverVersion := binary.BigEndian.Uint32(reply[1:]) & 0xffff
	if serverVersion < 13 {
		return fmt.Errorf("pcm: PulseAudio 协议过旧: %d", serverVersion)
	}
	return nil
}

func (p *paConn) setClientName(name string) error {
	t := newTAG()
	// v13+ 只收 proplist（name 并进 application.pulse 属性由服务器处理）。
	t.proplist(map[string]string{
		"application.name": name,
	})
	_, err := p.command(paCmdSetClientName, t)
	return err
}

// createPlayback 建播放流：FLOAT32LE/48k/2ch，tlength 约 250ms。返回流通道号。
// 载荷编码按 v13（与 libpulse/pipewire 源码逐字段核对）：sample spec、
// channel map、cvolume 的内部字段是裸大端值，其余 u32 带 'L' tag。
func (p *paConn) createPlayback(appName string) (uint32, uint32, error) {
	t := newTAG()
	// sample spec 'a'：format(u8) channels(u8) rate(u32)，内部无 tag
	t.raw(append([]byte{'a', paSampleFloat32LE, pcmChannels},
		binary.BigEndian.AppendUint32(nil, pcmRate)...))
	// channel map 'm'：count(u8) + positions(u8×n)，内部无 tag
	t.raw([]byte{'m', pcmChannels, paChannelFrontLeft, paChannelFrontRight})
	t.u32(0xFFFFFFFF)                    // sink_index: default
	t.stringNull()                       // sink_name: default
	t.u32(0)                             // maxlength: 服务器定
	t.bool(false)                        // corked
	t.u32(pcmRate * pcmChannels * 4 / 4) // tlength: 250ms
	t.u32(0)                             // prebuf: 服务器定
	t.u32(0)                             // minreq: 服务器定
	t.u32(0)                             // syncid
	// cvolume 'v'：count(u8) + 每通道裸 u32（PA_VOLUME_NORM=0x10000）
	cvol := append([]byte{'v', pcmChannels}, binary.BigEndian.AppendUint32(nil, 0x10000)...)
	cvol = binary.BigEndian.AppendUint32(cvol, 0x10000)
	t.raw(cvol)
	// v12 段：7 个 no_* flag
	t.bool(false) // no_remap
	t.bool(false) // no_remix
	t.bool(false) // fix_format
	t.bool(false) // fix_rate
	t.bool(false) // fix_channels
	t.bool(false) // no_move
	t.bool(false) // variable_rate
	// v13 段：muted + adjust_latency + proplist（proplist 必须在最后，
	// v13 的 CREATE 在 proplist 后即停）。
	t.bool(false) // muted
	t.bool(false) // adjust_latency
	t.proplist(map[string]string{
		"media.name":       "quaver-astra-playback",
		"application.name": appName,
	})
	reply, err := p.command(paCmdCreatePlayback, t)
	if err != nil {
		return 0, 0, err
	}
	r := newTAGReader(reply)
	channel := r.u32() // 流通道（写 memblock 用）
	_ = r.u32()        // sink-input index
	initial := r.u32() // 服务器允许立即写入的字节数（之后由 REQUEST 持续授予）
	_ = r.u32()        // maxlength
	_ = r.u32()        // tlength
	_ = r.u32()        // prebuf
	_ = r.u32()        // minreq
	r.expect('a')      // sample spec
	r.skip(6)          // format+channels+rate（裸字段）
	r.expect('m')      // channel map
	n := r.rawU8()
	r.skip(int(n))
	_ = r.u32() // sink index
	_ = r.skipString()
	r.skip(1)     // suspended bool
	_ = r.skip(9) // v13: TAG_USEC lat_usec（'U' tag + 8B 裸 u64）
	return channel, initial, nil
}

// write 向流通道写一块 PCM。offset 恒用 RELATIVE 语义（flags=0），
// 服务器按 REQUEST 许可节流。
func (p *paConn) write(channel uint32, data []byte) error {
	desc := make([]byte, paDescriptorSize)
	binary.BigEndian.PutUint32(desc[0:], uint32(len(data)))
	binary.BigEndian.PutUint32(desc[4:], channel)
	// offset 0 / flags 0：顺序追加
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.c.Write(append(desc, data...)); err != nil {
		return err
	}
	return nil
}

func (p *paConn) streamCommand(channel uint32, cmd uint32) error {
	t := newTAG()
	t.u32(channel)
	_, err := p.command(cmd, t)
	return err
}

// command 发 tagstruct 命令并等对应 tag 的应答。
func (p *paConn) command(cmd uint32, t *tagWriter) ([]byte, error) {
	tag := p.tag.Add(1)
	ch := make(chan *paPacket, 1)
	p.mu.Lock()
	p.replies[tag] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.replies, tag)
		p.mu.Unlock()
	}()

	// 命令帧 payload 本身是 tagstruct：cmd 与 tag 都带 'L' tag 字节。
	body := append([]byte{'L'}, binary.BigEndian.AppendUint32(nil, cmd)...)
	body = append(body, 'L')
	body = binary.BigEndian.AppendUint32(body, tag)
	body = append(body, t.buf...)
	p.writeMu.Lock()
	desc := make([]byte, paDescriptorSize)
	binary.BigEndian.PutUint32(desc[0:], uint32(len(body)))
	binary.BigEndian.PutUint32(desc[4:], 0xFFFFFFFF) // channel = -1: packet
	// 协议要求 AUTH 帧附 SCM_CREDENTIALS（服务器按 ucred 判同 uid）。
	oc := unixControlMessage{pid: int32(os.Getpid()), uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}
	_, _, werr := p.c.(*net.UnixConn).WriteMsgUnix(append(desc, body...), oc.encode(), nil)
	p.writeMu.Unlock()
	if werr != nil {
		return nil, werr
	}

	select {
	case pkt := <-ch:
		if pkt.channel == 0 {
			// 错误应答：payload = 'L'cmd 'L'tag 'L'errCode
			var code uint32
			if len(pkt.payload) >= 15 {
				code = binary.BigEndian.Uint32(pkt.payload[11:])
			}
			return nil, fmt.Errorf("pcm: PulseAudio 错误码 %d (tag=%d)", code, tag)
		}
		return pkt.payload, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("pcm: PulseAudio 应答超时")
	case <-p.closed:
		return nil, errors.New("pcm: PulseAudio 连接已关闭")
	}
}

func (p *paConn) readLoop() {
	for {
		desc := make([]byte, paDescriptorSize)
		if _, err := ioReadFull(p.c, desc); err != nil {
			p.shutdown()
			return
		}
		length := binary.BigEndian.Uint32(desc[0:])
		channel := binary.BigEndian.Uint32(desc[4:])
		flags := binary.BigEndian.Uint32(desc[16:])
		if length == 0 || length > 64<<20 {
			p.shutdown()
			return
		}
		payload := make([]byte, length)
		if _, err := ioReadFull(p.c, payload); err != nil {
			p.shutdown()
			return
		}
		if channel == 0xFFFFFFFF {
			// 包帧：tagstruct(cmd, tag, ...)
			r := newTAGReader(payload)
			cmd := r.u32()
			tag := r.u32()
			switch cmd {
			case paCmdReply:
				// 应答 payload 前缀是带 tag 的 cmd+tag（'L'+4B ×2），
				// 真正的字段从第 10 字节开始。
				p.mu.Lock()
				ch := p.replies[tag]
				p.mu.Unlock()
				if ch != nil && len(payload) >= 10 {
					ch <- &paPacket{channel: 0xFFFFFFFF, payload: payload[10:]}
				}
			case paCmdError:
				// 错误应答 payload: 'L'errCode。转成 REPLY 通道并附标记，
				// 让 command() 报出具体错误码。
				p.mu.Lock()
				ch := p.replies[tag]
				p.mu.Unlock()
				if ch != nil {
					ch <- &paPacket{channel: 0, payload: payload}
				}
			case paCmdRequest:
				_ = r.u32() // 流通道（单流连接，无需区分）
				avail := r.u32()
				select {
				case p.requests <- avail:
				default:
				}
			case paCmdUnderflow, paCmdPlaybackKilled:
				// 欠载时继续写即可恢复；KILLED 视作连接坏。
				if cmd == paCmdPlaybackKilled {
					p.shutdown()
					return
				}
			}
		} else if flags != 0 {
			// 意外 memblock（不该出现在播放客户端），忽略。
		}
	}
}

func (p *paConn) shutdown() {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	_ = p.c.Close()
}

func (p *paConn) close() { p.shutdown() }

// pulseSpeaker 适配 paConn 到 pcmSpeaker 接口：REQUEST 给多少许可就写多少，
// 没有 FULL 阻塞语义，靠许可计数实现设备时钟背压。
type pulseSpeaker struct {
	conn *paConn

	mu       sync.Mutex
	channel  uint32
	avail    int64 // 服务器许可的可写字节数
	availCh  chan struct{}
	closed   bool
	writeErr error
}

func newPulseSpeaker() (pcmSpeaker, error) {
	conn, err := dialPulseAudio()
	if err != nil {
		return nil, err
	}
	if err := conn.auth(); err != nil {
		conn.close()
		return nil, err
	}
	if err := conn.setClientName("quaver-astra"); err != nil {
		conn.close()
		return nil, err
	}
	ch, initial, err := conn.createPlayback("quaver-astra")
	if err != nil {
		conn.close()
		return nil, err
	}
	s := &pulseSpeaker{
		conn:    conn,
		channel: ch,
		availCh: make(chan struct{}, 1),
	}
	s.avail = int64(initial)
	if initial > 0 {
		select {
		case s.availCh <- struct{}{}:
		default:
		}
	}
	go s.pumpRequests()
	return s, nil
}

func (s *pulseSpeaker) pumpRequests() {
	for {
		select {
		case n := <-s.conn.requests:
			s.mu.Lock()
			s.avail += int64(n)
			s.mu.Unlock()
			select {
			case s.availCh <- struct{}{}:
			default:
			}
		case <-s.conn.closed:
			return
		}
	}
}

func (s *pulseSpeaker) Start() error { return nil }

// Play 阻塞到服务器把这批样本全部收下。许可不足时睡在 availCh 上，
// 由 readLoop 的 REQUEST 唤醒——许可节奏就是 sink 的消费时钟。
func (s *pulseSpeaker) Play(samples []float32) error {
	data := encodeSamples(samples, alsaFmtF32LE)
	for len(data) > 0 {
		s.mu.Lock()
		if s.closed {
			err := s.writeErr
			s.mu.Unlock()
			if err != nil {
				return err
			}
			return errors.New("pcm: PulseAudio 流已关闭")
		}
		avail := s.avail
		s.mu.Unlock()
		if avail <= 0 {
			select {
			case <-s.availCh:
				continue
			case <-s.conn.closed:
				return errors.New("pcm: PulseAudio 连接断开")
			}
		}
		n := len(data)
		if int64(n) > avail {
			n = int(avail)
		}
		if err := s.conn.write(s.channel, data[:n]); err != nil {
			return err
		}
		s.mu.Lock()
		s.avail -= int64(n)
		s.mu.Unlock()
		data = data[n:]
	}
	return nil
}

func (s *pulseSpeaker) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	// 连接直接断：服务器按 PLAYBACK_STREAM_KILLED 清流，比走 DELETE 快。
	s.conn.close()
}

// 覆盖 alsa 文件里挂的兜底分派（Go 按文件名顺序跑 init，pulse > alsa）。
func init() {
	speakerFactory = func() (pcmSpeaker, error) {
		if s, err := newPulseSpeaker(); err == nil {
			return s, nil
		}
		return newALSASpeaker()
	}
}

// tagWriter 是简化版 pa_tagstruct（只放客户端需要的类型，网络字节序）。
type tagWriter struct{ buf []byte }

func newTAG() *tagWriter { return &tagWriter{} }

func (t *tagWriter) u8(v byte)    { t.buf = append(t.buf, 'B', v) }
func (t *tagWriter) raw(d []byte) { t.buf = append(t.buf, d...) }
func (t *tagWriter) u32(v uint32) {
	t.buf = append(t.buf, 'L')
	t.buf = binary.BigEndian.AppendUint32(t.buf, v)
}
func (t *tagWriter) stringNull() { t.buf = append(t.buf, 'N') }
func (t *tagWriter) string(s string) {
	t.buf = append(t.buf, 't')
	t.buf = append(t.buf, s...)
	t.buf = append(t.buf, 0)
}
func (t *tagWriter) bool(v bool) {
	if v {
		t.buf = append(t.buf, '1')
	} else {
		t.buf = append(t.buf, '0')
	}
}
func (t *tagWriter) arbitrary(d []byte) {
	t.buf = append(t.buf, 'x')
	t.buf = binary.BigEndian.AppendUint32(t.buf, uint32(len(d)))
	t.buf = append(t.buf, d...)
}
func (t *tagWriter) proplist(kv map[string]string) {
	t.buf = append(t.buf, 'P')
	for k, v := range kv {
		t.string(k)
		// 条目结构：key(string) + length(u32 带 'L' tag) + value(arbitrary)。
		// 与 libpulse 一致：length 含结尾 NUL，value 也带 NUL——长度与实际
		// 字节数差 1 会让服务器读端错位、整包 EPROTO（gomain4 的教训）。
		t.u32(uint32(len(v)) + 1)
		t.arbitrary(append([]byte(v), 0))
	}
	t.stringNull()
}

// tagReader 是 tagWriter 的读侧（只解析 create 回包和 REQUEST）。
type tagReader struct {
	buf    []byte
	pos    int
	lastU8 byte
}

func newTAGReader(b []byte) *tagReader { return &tagReader{buf: b} }

func (r *tagReader) u8() byte {
	if r.pos >= len(r.buf) {
		return 0
	}
	r.pos++ // tag 字节
	v := r.buf[r.pos]
	r.pos++
	return v
}
func (r *tagReader) u32() uint32 {
	if r.pos >= len(r.buf) {
		return 0
	}
	r.pos++
	v := binary.BigEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v
}
func (r *tagReader) rawU8() byte {
	if r.pos >= len(r.buf) {
		return 0
	}
	v := r.buf[r.pos]
	r.pos++
	return v
}
func (r *tagReader) expect(tag byte) {
	if r.pos < len(r.buf) && r.buf[r.pos] == tag {
		r.pos++
	}
}
func (r *tagReader) skipString() []byte {
	if r.pos >= len(r.buf) {
		return nil
	}
	if r.buf[r.pos] == paTagStringNull {
		r.pos++
		return nil
	}
	if r.buf[r.pos] == paTagString {
		r.pos++
	}
	end := r.pos
	for end < len(r.buf) && r.buf[end] != 0 {
		end++
	}
	v := r.buf[r.pos:end]
	r.pos = end + 1
	return v
}
func (r *tagReader) skip(n int) []byte {
	if r.pos+n > len(r.buf) {
		n = len(r.buf) - r.pos
	}
	v := r.buf[r.pos : r.pos+n]
	r.pos += n
	return v
}

// unixControlMessage 手工组 SCM_CREDENTIALS 辅助消息（cmsg 格式：
// len|SOL_SOCKET|SCM_CREDENTIALS|padding|pid,uid,gid），避免依赖 x/sys/unix。
type unixControlMessage struct {
	pid int32
	uid uint32
	gid uint32
}

func (m unixControlMessage) encode() []byte {
	const sizeofUcred = 12 // uint32 pid, uid, gid
	dataLen := sizeofUcred
	total := 16 + dataLen // cmsghdr(16B on 64-bit) + data + 对齐
	buf := make([]byte, total)
	binary.LittleEndian.PutUint64(buf[0:], uint64(16+dataLen))
	binary.LittleEndian.PutUint64(buf[8:], uint64(unixSOLSocket)<<32|unixSCMCredentials)
	binary.LittleEndian.PutUint32(buf[16:], uint32(m.pid))
	binary.LittleEndian.PutUint32(buf[20:], m.uid)
	binary.LittleEndian.PutUint32(buf[24:], m.gid)
	return buf
}

const (
	unixSOLSocket      = 1
	unixSCMCredentials = 2
)

// ioReadFull 避免为这一个小助手引 io/ioutil 链。
func ioReadFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
