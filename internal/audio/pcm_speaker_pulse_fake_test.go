//go:build linux

package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePA 是一个最小 PulseAudio native 服务器，只服务于本包的客户端测试：
// 在临时目录起 unix socket，把握手 → 建流 → REQUEST 节流 → 错误应答走完，
// 不碰声卡，所以无头 CI 也能跑（真机行为由 TestPulseSpeakerSmoke 负责）。
//
// 它守得住的：客户端字段编码、应答解析的字节偏移、credit 记账、错误码透传、
// "Pulse 优先于 ALSA 兜底"的分派顺序——手写 tagstruct 最容易回归的正是这些。
// 它守不住的：与真实 libpulse/PipeWire 的一致性（fake 按同一份约定收发包）。
type fakePA struct {
	t  *testing.T
	ln net.Listener

	// initial 是建流应答里给的初始可写字节数；0 表示按客户端 tlength。
	initial int
	// grantDelay 是每收到一块 PCM 后延后多久再发 REQUEST，模拟 sink 消费时钟。
	grantDelay time.Duration

	stopped atomic.Bool

	mu sync.Mutex
	// 可变配置：握手/建流阶段读，测试用 setter 改（handler 在别的 goroutine）。
	errCode       uint32
	serverVersion uint32
	// 观察项：测试结束后回读的协议字段与流量统计。其余建流参数（声道映射、
	// 音量、各类 flag）在 handler 里就地断言，不跨 goroutine 传递。
	cookieLen     int
	clientName    string
	sampleFormat  byte
	sampleRate    uint32
	tlength       uint32
	adjustLatency bool
	createProps   map[string]string

	conn      net.Conn
	streamCh  uint32
	received  int
	credit    int
	overdrawn int
	chunks    int
}

// startFakePA 起 fake 服务器，并把 XDG_RUNTIME_DIR/HOME 指到它身上。
func startFakePA(t *testing.T, initial int, grantDelay time.Duration) *fakePA {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pulse"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "pulse", "native"))
	if err != nil {
		t.Fatalf("fake PA 监听失败: %v", err)
	}
	f := &fakePA{t: t, ln: ln, initial: initial, grantDelay: grantDelay, serverVersion: 35}
	go f.acceptLoop()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	// cookie 读的是 $HOME/.config/pulse/cookie：指到空目录，别依赖真机那份。
	t.Setenv("HOME", dir)
	t.Cleanup(f.stop)
	return f
}

func (f *fakePA) setErrCode(code uint32) {
	f.mu.Lock()
	f.errCode = code
	f.mu.Unlock()
}

func (f *fakePA) setServerVersion(v uint32) {
	f.mu.Lock()
	f.serverVersion = v
	f.mu.Unlock()
}

func (f *fakePA) stop() {
	f.stopped.Store(true)
	_ = f.ln.Close()
	f.mu.Lock()
	c := f.conn
	f.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// errorf 让 handler goroutine 只在测试还活着时报错，避免迟到连接的错误
// 打在已经结束的 test 上。
func (f *fakePA) errorf(format string, a ...any) {
	if f.stopped.Load() {
		return
	}
	f.t.Errorf(format, a...)
}

func (f *fakePA) acceptLoop() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return // 监听已关闭
		}
		f.mu.Lock()
		f.conn = c
		f.mu.Unlock()
		f.handle(c) // 一条连接一个 handler，串行处理命令与 REQUEST
	}
}

func (f *fakePA) handle(c net.Conn) {
	defer c.Close()
	for {
		desc := make([]byte, paDescriptorSize)
		if _, err := io.ReadFull(c, desc); err != nil {
			if !f.stopped.Load() && err != io.EOF && !strings.Contains(err.Error(), "use of closed") {
				f.errorf("fake PA 读描述符失败: %v", err)
			}
			return
		}
		length := binary.BigEndian.Uint32(desc[0:])
		channel := binary.BigEndian.Uint32(desc[4:])
		if length == 0 || length > 4<<20 {
			f.errorf("fake PA 收到异常帧长 %d", length)
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c, payload); err != nil {
			f.errorf("fake PA 读载荷失败: %v", err)
			return
		}
		if channel != 0xFFFFFFFF {
			f.onMemblock(c, channel, payload)
			continue
		}
		// 协议解析失败用 panic 中断本次连接（已 errorf 记账），recover 兜住：
		// 断言不该把测试进程炸掉，只需让客户端拿到"服务器挂了"并让测试红。
		f.dispatch(c, payload)
	}
}

func (f *fakePA) dispatch(c net.Conn, payload []byte) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if _, ok := v.(protocolError); !ok {
			panic(v)
		}
	}()
	r := newFR(f, payload)
	cmd, tag := r.u32('L'), r.u32('L')
	switch cmd {
	case paCmdAuth:
		f.onAuth(c, tag, r)
	case paCmdSetClientName:
		f.onSetClientName(c, tag, r)
	case paCmdCreatePlayback:
		f.onCreatePlayback(c, tag, r)
	default:
		f.errorf("fake PA 收到未预期的命令 %d", cmd)
	}
}

func (f *fakePA) onAuth(c net.Conn, tag uint32, r *fr) {
	v := r.u32('L')
	cookie := r.arbitrary()
	r.eof()
	f.mu.Lock()
	wantVersion := f.serverVersion
	f.cookieLen = len(cookie)
	f.mu.Unlock()
	// 客户端必须显式关掉 SHM（走 memblock 直写），并只声称自己钉住的 v13。
	if v&0x80000000 == 0 {
		f.errorf("AUTH 未置 SHM 关闭位: %#x", v)
		return
	}
	if ver := v & 0x7fffffff; ver > paProtoVersion {
		f.errorf("AUTH 声称版本 %d，高于客户端钉住的 %d", ver, paProtoVersion)
		return
	}
	if len(cookie) != paCookieLen {
		f.errorf("AUTH cookie 长 %d，应为 %d", len(cookie), paCookieLen)
		return
	}
	f.reply(c, tag, putU32(nil, wantVersion))
}

func (f *fakePA) onSetClientName(c net.Conn, tag uint32, r *fr) {
	props := r.proplist() // v13+ 只收一个 proplist
	r.eof()
	f.mu.Lock()
	f.clientName = props["application.name"]
	f.mu.Unlock()
	f.reply(c, tag, nil)
}

func (f *fakePA) onCreatePlayback(c net.Conn, tag uint32, r *fr) {
	// sample spec 'a'：format(u8) channels(u8) rate(u32)，内部是裸字段。
	r.must('a')
	format, chans := r.byte(), r.byte()
	rate := r.u32raw()
	// channel map 'm'：count(u8) + positions(u8×n)。
	r.must('m')
	n := int(r.byte())
	positions := r.block(n)
	sinkIndex := r.u32('L')
	sinkName := r.cstring()
	maxlength := r.u32('L')
	corked := r.boolean()
	tlength := r.u32('L')
	prebuf := r.u32('L')
	minreq := r.u32('L')
	syncid := r.u32('L')
	// cvolume 'v'：count(u8) + 每通道裸 u32。
	r.must('v')
	volN := int(r.byte())
	vols := make([]uint32, 0, volN)
	for i := 0; i < volN; i++ {
		vols = append(vols, r.u32raw())
	}
	// v12 段的 7 个 no_*/fix_* flag，再接 v13 的 muted、adjust_latency。
	flags := make([]bool, 9)
	for i := range flags {
		flags[i] = r.boolean()
	}
	props := r.proplist()
	r.eof()

	f.mu.Lock()
	f.sampleFormat, f.sampleRate, f.tlength = format, rate, tlength
	f.adjustLatency, f.createProps = flags[8], props
	f.mu.Unlock()

	muted, adjust := flags[7], flags[8]
	// 建流参数必须与 ao_pcm 的固定形状一致，且默认 sink + 满格音量。
	switch {
	case format != paSampleFloat32LE:
		f.errorf("采样格式 %d，应为 FLOAT32LE(%d)", format, paSampleFloat32LE)
		return
	case rate != pcmRate:
		f.errorf("采样率 %d，应为 %d", rate, pcmRate)
		return
	case chans != pcmChannels || n != pcmChannels:
		f.errorf("声道数 %d/map 条目 %d，应为 %d", chans, n, pcmChannels)
		return
	case len(positions) != pcmChannels || positions[0] != paChannelFrontLeft || positions[1] != paChannelFrontRight:
		f.errorf("声道映射 %+v，应为 [FL FR]", positions)
		return
	case sinkIndex != 0xFFFFFFFF || sinkName != nil:
		f.errorf("sink 未指向默认设备: index=%#x name=%v", sinkIndex, sinkName)
		return
	case maxlength != 0 || prebuf != 0 || minreq != 0 || syncid != 0:
		f.errorf("缓冲参数应交给服务器: maxlength=%d prebuf=%d minreq=%d syncid=%d", maxlength, prebuf, minreq, syncid)
		return
	case corked || muted || adjust:
		f.errorf("流状态异常 corked=%v muted=%v adjust_latency=%v", corked, muted, adjust)
		return
	case len(vols) != pcmChannels:
		f.errorf("音量条目 %d，应为 %d", len(vols), pcmChannels)
		return
	case props["media.name"] != "quaver-astra-playback" || props["application.name"] != "quaver-astra":
		f.errorf("proplist 缺少期望的媒体/应用属性: %+v", props)
		return
	default:
		for _, v := range vols {
			if v != 0x10000 { // PA_VOLUME_NORM
				f.errorf("通道音量 %#x，应为 PA_VOLUME_NORM", v)
				return
			}
		}
	}
	f.mu.Lock()
	code := f.errCode
	f.mu.Unlock()
	if code != 0 {
		f.errorReply(c, tag, code)
		return
	}

	f.streamCh = 7 // 随便一个非 0 流通道号
	initial := f.initial
	if initial == 0 {
		initial = int(tlength)
	}
	f.mu.Lock()
	f.credit = initial
	f.mu.Unlock()
	fields := putU32(nil, f.streamCh) // 流通道（写 memblock 时的 index）
	fields = putU32(fields, 42)       // sink-input index
	fields = putU32(fields, uint32(initial))
	fields = putU32(fields, 1<<20) // maxlength
	fields = putU32(fields, tlength)
	fields = putU32(fields, tlength/4) // prebuf
	fields = putU32(fields, 1)         // minreq
	fields = binary.BigEndian.AppendUint32(append(fields, 'a', paSampleFloat32LE, pcmChannels), pcmRate)
	fields = append(fields, 'm', pcmChannels, paChannelFrontLeft, paChannelFrontRight)
	fields = putU32(fields, 1) // sink index
	fields = append(fields, 'N')
	fields = append(fields, '0') // suspended
	fields = append(fields, 'U') // lat_usec（客户端按 9 字节跳过）
	fields = binary.BigEndian.AppendUint64(fields, 0)
	f.reply(c, tag, fields)
}

// onMemblock 收下 PCM 并做 credit 记账：客户端超出许可写入就是 bug。
func (f *fakePA) onMemblock(c net.Conn, channel uint32, payload []byte) {
	f.mu.Lock()
	f.received += len(payload)
	f.credit -= len(payload)
	f.chunks++
	credit := f.credit
	chunk := f.chunks
	if credit < 0 {
		f.overdrawn += -credit
	}
	f.mu.Unlock()
	if channel != f.streamCh {
		f.errorf("PCM 写到通道 %d，建流时给的是 %d", channel, f.streamCh)
		return
	}
	if credit < 0 {
		f.errorf("第 %d 块超出 REQUEST 许可 %d 字节", chunk, -credit)
		return
	}
	// 把刚收下的量等量补回去，但要先"消化" grantDelay：
	// 客户端必须睡在许可上，不能一路写完。
	if f.grantDelay > 0 {
		time.Sleep(f.grantDelay)
	}
	if f.stopped.Load() {
		return
	}
	f.mu.Lock()
	f.credit += len(payload)
	f.mu.Unlock()
	f.sendRequest(c, channel, uint32(len(payload)))
}

// reply 发 REPLY 包帧：载荷 = 'L'cmd 'L'tag + fields。
func (f *fakePA) reply(c net.Conn, tag uint32, fields []byte) {
	body := putU32(nil, paCmdReply)
	body = putU32(body, tag)
	f.send(c, append(body, fields...))
}

// errorReply 发 ERROR 包帧：客户端从载荷第 11 字节起读错误码。
func (f *fakePA) errorReply(c net.Conn, tag, code uint32) {
	body := putU32(nil, paCmdError)
	body = putU32(body, tag)
	f.send(c, putU32(body, code))
}

// sendRequest 发服务器→客户端的 REQUEST：'L'cmd 'L'0 'L'流通道 'L'可写字节数。
func (f *fakePA) sendRequest(c net.Conn, channel, n uint32) {
	body := putU32(nil, paCmdRequest)
	body = putU32(body, 0)
	body = putU32(body, channel)
	f.send(c, putU32(body, n))
}

func (f *fakePA) send(c net.Conn, body []byte) {
	desc := make([]byte, paDescriptorSize)
	binary.BigEndian.PutUint32(desc[0:], uint32(len(body)))
	binary.BigEndian.PutUint32(desc[4:], 0xFFFFFFFF) // 包帧
	// 写失败不记账：测试收尾时 speaker.Close() 先于 fake 停止，handler 可能正
	// 睡在最后一次 grant 上，往已关闭的连接写是正常现象。真出问题时客户端会以
	// "应答超时/连接断开"报错，不需要这里再补一刀。
	_, _ = c.Write(append(desc, body...))
}

// fakeStats 是观察量快照（handler goroutine 与 test 之间靠 mu 同步）。
type fakeStats struct {
	received, overdrawn, chunks int
	cookieLen                   int
	clientName, mediaName       string
	sampleFormat                byte
	sampleRate                  uint32
	tlength                     uint32
	adjustLatency               bool
}

func (f *fakePA) snapshot() fakeStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeStats{
		received:      f.received,
		overdrawn:     f.overdrawn,
		chunks:        f.chunks,
		cookieLen:     f.cookieLen,
		clientName:    f.clientName,
		mediaName:     f.createProps["media.name"],
		sampleFormat:  f.sampleFormat,
		sampleRate:    f.sampleRate,
		tlength:       f.tlength,
		adjustLatency: f.adjustLatency,
	}
}

// waitReceived 等服务器把客户端写出的字节全部读走。Play 返回只意味着最后一块
// 已经进了 socket 缓冲，读端可能还差一拍，所以断言总量前要先把它等齐。
func (f *fakePA) waitReceived(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.snapshot().received >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("服务器只收到 %d 字节，应为 %d", f.snapshot().received, want)
}

func putU32(b []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(append(b, 'L'), v)
}

// fr 是 fake 侧的严格 tagstruct 读取器。与生产代码里的 tagReader 不同，
// 偏移或 tag 不对就立刻判失败——手写协议最典型的回归就是读错一个字节。
type fr struct {
	f *fakePA
	b []byte
	i int
}

func newFR(f *fakePA, b []byte) *fr { return &fr{f: f, b: b} }

func (r *fr) fail(at string) {
	msg := fmt.Sprintf("tagstruct 解析在 %s 处失败（偏移 %d/%d）", at, r.i, len(r.b))
	r.f.errorf("%s", msg)
	panic(protocolError(msg))
}

type protocolError string

func (e protocolError) Error() string { return string(e) }

func (r *fr) need(n int, at string) []byte {
	if r.i+n > len(r.b) {
		r.fail(at + " 越界")
	}
	v := r.b[r.i : r.i+n]
	r.i += n
	return v
}

func (r *fr) byte() byte     { return r.need(1, "u8")[0] }
func (r *fr) u32raw() uint32 { return binary.BigEndian.Uint32(r.need(4, "u32")) }

func (r *fr) block(n int) []byte {
	v := r.need(n, "block")
	return append([]byte(nil), v...)
}

func (r *fr) u32(tag byte) uint32 {
	r.must(tag)
	return r.u32raw()
}

func (r *fr) must(tag byte) {
	if r.i >= len(r.b) || r.b[r.i] != tag {
		r.fail(fmt.Sprintf("期望 tag %q", tag))
	}
	r.i++
}

func (r *fr) boolean() bool {
	switch v := r.byte(); v {
	case '0':
		return false
	case '1':
		return true
	default:
		r.fail("期望 bool tag")
		return false
	}
}

// cstring 返回 nil 表示 TAG_END_OF_LIST（'N'）。
func (r *fr) cstring() *string {
	switch tag := r.byte(); tag {
	case paTagStringNull:
		return nil
	case paTagString:
		end := r.i
		for end < len(r.b) && r.b[end] != 0 {
			end++
		}
		if end >= len(r.b) {
			r.fail("cstring 缺少结尾 NUL")
		}
		v := string(r.b[r.i:end])
		r.i = end + 1
		return &v
	default:
		r.fail("期望 string tag")
		return nil
	}
}

// arbitrary：'x' + 裸 u32 长度 + 数据。
func (r *fr) arbitrary() []byte {
	r.must(paTagArbitrary)
	return r.block(int(r.u32raw()))
}

func (r *fr) proplist() map[string]string {
	r.must(paTagProplist)
	out := map[string]string{}
	for {
		k := r.cstring()
		if k == nil {
			return out
		}
		declared := int(r.u32('L'))
		v := r.arbitrary()
		// libpulse 约定：长度含结尾 NUL，所以声明长度必须等于实际字节数且末位是 0。
		// 差 1 会让服务器读端错位、整包 EPROTO。
		if declared != len(v) || len(v) == 0 || v[len(v)-1] != 0 {
			r.f.errorf("proplist[%q] 声明长度 %d 与实际 %d 不符或缺 NUL", *k, declared, len(v))
			panic(protocolError("proplist 编码错误"))
		}
		out[*k] = strings.TrimSuffix(string(v), "\x00")
	}
}

func (r *fr) eof() {
	if r.i != len(r.b) {
		r.fail("命令末尾仍有未解析字节")
	}
}

// TestPulseSpeakerFakeBackpressure 在 fake 服务器上跑完整播放路径：
// 握手 → 建流 → 按 REQUEST 许可分块写入。初始许可 32000B，服务器每收一块
// 就延后 grantDelay 再等量补回，客户端必须被许可卡住、不能一次写完。
func TestPulseSpeakerFakeBackpressure(t *testing.T) {
	const grant = 32000
	const delay = 20 * time.Millisecond
	f := startFakePA(t, grant, delay)

	// 走 newSpeakerFor("")：顺带验证"Pulse 可用时不会被 ALSA 兜底顶掉"。
	s, err := newSpeakerFor("")
	if err != nil {
		t.Fatal("newSpeakerFor:", err)
	}
	if _, ok := s.(*pulseSpeaker); !ok {
		t.Fatalf("期望 *pulseSpeaker，得到 %T", s)
	}
	t.Cleanup(s.Close)

	samples := make([]float32, pcmRate/2*pcmChannels) // 0.5s 立体声 = 192000B PCM
	start := time.Now()
	if err := s.Play(samples); err != nil {
		t.Fatal("Play:", err)
	}
	elapsed := time.Since(start)
	f.waitReceived(t, len(samples)*4)

	st := f.snapshot()
	if st.received != len(samples)*4 {
		t.Fatalf("服务器收到 %d 字节，应为 %d", st.received, len(samples)*4)
	}
	if st.overdrawn != 0 {
		t.Fatalf("客户端超出 REQUEST 许可 %d 字节", st.overdrawn)
	}
	// 192000/32000 = 6 块，最后一块写完即返回，所以至少等了 5 次 grantDelay。
	if min := 5 * delay; elapsed < min {
		t.Fatalf("Play 只耗时 %v，未受 REQUEST 背压约束（下限 %v）", elapsed, min)
	}
	if st.chunks < 6 {
		t.Fatalf("写了 %d 块，应被许可切成多块", st.chunks)
	}
	if st.cookieLen != paCookieLen {
		t.Fatalf("AUTH cookie %d 字节，应为 %d", st.cookieLen, paCookieLen)
	}
	if st.clientName != "quaver-astra" || st.mediaName != "quaver-astra-playback" {
		t.Fatalf("应用/媒体命名异常: %q / %q", st.clientName, st.mediaName)
	}
	if st.sampleFormat != paSampleFloat32LE || st.sampleRate != pcmRate {
		t.Fatalf("采样格式/率异常: %d / %d", st.sampleFormat, st.sampleRate)
	}
	if st.tlength != pcmRate*pcmChannels*4/4 {
		t.Fatalf("tlength %d，应为 250ms = %d", st.tlength, pcmRate*pcmChannels*4/4)
	}
	if st.adjustLatency {
		t.Fatal("adjust_latency 应关闭，否则服务器会自调缓冲、破坏背压假设")
	}
}

// TestPulseSpeakerFakeErrorReply 验证服务器的错误码能透到调用方
// （真实场景：sink 被权限策略拒绝）。
func TestPulseSpeakerFakeErrorReply(t *testing.T) {
	f := startFakePA(t, 0, 0)
	f.setErrCode(6) // PA_ERR_ACCESS 一类
	if _, err := newPulseSpeaker(); err == nil {
		t.Fatal("建流被服务器拒绝时应当返回错误")
	} else if !strings.Contains(err.Error(), "错误码 6") {
		t.Fatalf("错误未带上 PulseAudio 错误码: %v", err)
	}
	if st := f.snapshot(); st.received != 0 {
		t.Fatalf("建流失败后不应有 PCM 写出，收到 %d 字节", st.received)
	}
}

// TestPulseSpeakerFakeOldProtocol 验证协议版本过旧时握手即失败，
// 而不是继续发包被服务器 EPROTO。
func TestPulseSpeakerFakeOldProtocol(t *testing.T) {
	f := startFakePA(t, 0, 0)
	f.setServerVersion(12)
	if _, err := newPulseSpeaker(); err == nil || !strings.Contains(err.Error(), "协议过旧") {
		t.Fatalf("v12 服务器应在握手时失败: %v", err)
	}
}

// TestPulseSpeakerFakeDialFailure 验证没有服务器时快速失败且把原因说清楚
// （这就是无头 CI 的形态：拨号失败 → newSpeakerFor 兜底去开 ALSA，
// ALSA 那两条路径分别由 TestALSASpeakerRealDevice 与 fake 建流测试守着）。
func TestPulseSpeakerFakeDialFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	_, err := newPulseSpeaker()
	if err == nil || !strings.Contains(err.Error(), "连接 PulseAudio 失败") {
		t.Fatalf("缺少 socket 时拨号应失败: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "pulse", "native")) {
		t.Fatalf("错误里应带上尝试连接的 socket 路径，便于排查: %v", err)
	}
	// XDG_RUNTIME_DIR 空值等价于没设：直接说清"找不到 socket"，不要退化成
	// 一个莫名其妙的拨号错误。
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := newPulseSpeaker(); err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Fatalf("XDG_RUNTIME_DIR 为空时应直接报缺环境: %v", err)
	}
}
