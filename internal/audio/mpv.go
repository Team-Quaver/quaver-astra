// Package audio 是基于 mpv 的播放引擎。
//
// 播放全部交给 mpv 子进程（--no-video --idle --input-ipc-server），
// 本包只做 JSON IPC 的命令下发与属性观察。解码、seek、缓冲、gapless
// 以及各类容器/编码（mp3 / flac / ogg / aac / atmos / DTS）都由 mpv
// 内部的 FFmpeg 处理，因此不再需要 go-mp3、mewkiz/flac、oggvorbis
// 这些自研解码链，也不再需要为 seek 重建解码器。
//
// 为什么是子进程而不是 libmpv：本项目要交叉编译到
// linux/{amd64,arm64,loong64}、windows/{amd64,arm64}、darwin/arm64，
// cgo 会破坏交叉编译；mpv 二进制在各发行版可独立安装，经 IPC 通信后
// 播放后端与 UI 完全解耦。
package audio

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Engine 通过 mpv 子进程播放一路音频。全部方法并发安全。
//
// 状态模型：mpv 是唯一真相源——位置、时长、暂停、播完都问它。
// 本地不缓存位置，这消除了旧实现里「墙钟推算 vs 源消费量」的偏差，
// 也让 seek 的位置立即准确，无需重锚定。
type Engine struct {
	mu       sync.Mutex // 保护进程/连接/本地状态
	readMu   sync.Mutex // 串行化对 socket 的读（握手命令与观察协程共用）
	cmd      *exec.Cmd
	done     chan struct{} // cmd.Wait 完成；Close 等它确认子进程已回收
	doneOnce sync.Once
	started  chan struct{} // 后台 spawn 结束（成功或失败）
	startErr error
	conn     ipcConn
	rd       *bufio.Reader
	wr       *bufio.Writer
	nextID   int64
	closed   bool

	// 本地意图 + mpv 观测（Paused 取二者或）
	wantPaused bool
	volume     float64

	// 观察循环维护的状态
	pos      float64
	dur      float64
	eof      bool
	idle     bool // mpv 空闲（无播放）
	obsPause bool
	stalled  bool   // paused-for-cache：缓冲欠载，mpv 正在等待数据
	loadErr  string // 本次载入失败的原因（空 = 正常）

	// 载入代次：mpv 的 loadfile 是异步的，命令下发之后 start-file /
	// file-loaded / end-file 与命令回包才陆续到达。玩家在等不到结果时会换档
	// 重发 OpenURL，此时上一轮的尾巴可能后到——最坏的情况是「上一档的
	// end-file(error)」被当成「这一档刚开始就失败」，好档就此被判成坏档，
	// 整条回退链一起误判，最后自动跳到下一曲。所以每次 OpenURL 递增
	// loadGen，事件与回包都盖上当时的代次，读取时只认当前代次。
	loadGen      uint64 // 每次 OpenURL 递增
	startGen     uint64 // 收到 start-file 的代次
	loadedGen    uint64 // 收到 file-loaded 的代次
	loadErrGen   uint64 // loadErr 归属的代次
	loadErrFatal bool   // loadErr 与档位无关（命令被拒/引擎没了）
	loadReq      int64  // 本次 loadfile 命令的 request_id
	loadRetried  uint64 // 已换过参数布局重发的代次
	lastLoad     loadCmd

	// mpv 版本决定 loadfile 的参数布局（见 loadfileWantsIndex）
	mpvVersion    string
	loadfileIndex bool

	// MPV 兼容模式（PCM 输出）：mpv 只解码，实时 PCM 由本进程出声。
	// pcm nil = 原生直出；非 nil = 兼容模式会话（见 engine_pcm.go）。
	pcmCompat bool
	pcm       *pcmOutput

	// readTimeout 是 socket 单次读的兜底超时：默认 30s（兼作空闲连接的
	// keepalive），握手期的探测收紧——mpv 在无声卡的环境里可能迟迟不回应，
	// 不能让应用启动跟着一起卡住。
	readTimeout time.Duration

	obs   func()
	obsCh chan struct{}
}

// loadCmd 是一次 loadfile 的参数快照。留底是为了在 mpv 拒绝当前的参数布局时
// 原样换一种布局重发（自愈路径），不必再走一遍解析。
type loadCmd struct {
	url   string
	flags string
	opts  map[string]any
}

// ipcConn 是引擎与 mpv 子进程之间的双向通道。Unix/macOS 上是 AF_UNIX
// socket，Windows 上是命名管道；不支持超时的实现把 SetReadDeadline 置空。
type ipcConn interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
}

// ipcResp 是 mpv IPC 的一条响应或事件。
//
// 协议见 mpv 手册 "JSON IPC"：顶层必须是 JSON **对象**——
// {"command": [...]} / {"request_id": N, "async": true}。
// 发成数组会让 mpv 当成 input.conf 文本命令（首个字符不是 {），
// 于是永远没有响应。
type ipcResp struct {
	Error  string `json:"error"`
	Data   any    `json:"data"`
	Event  string `json:"event"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// property-change 的 id 字段无用，占位保持字段顺序稳定
	PropID    any   `json:"id"`
	RequestID int64 `json:"request_id"`
	// end-file(reason=error) 里的具体失败原因
	FileError string `json:"file_error"`
}

// ipcReadTimeout 是 IPC socket 单次读的默认超时。
const ipcReadTimeout = 30 * time.Second

// New 构造引擎并在后台启动 mpv。构造本身不碰磁盘/进程/音频设备：
// 首次启动曾被放在 main 的窗口创建之前，mpv 初始化慢（无声卡、驱动
// 初始化、杀软扫描随包 mpv）时窗口根本不出现，用户看到的是一次白屏
// 或「没反应」。播放路径会等这次后台启动完成；启动失败则退化为哑引擎，
// 所有操作返回错误，调用方无需区分「无音频设备」与「无 mpv」。
func New() *Engine {
	e := &Engine{
		volume: 0.8, obsCh: make(chan struct{}, 1), done: make(chan struct{}),
		started: make(chan struct{}), readTimeout: ipcReadTimeout,
	}
	go e.start()
	return e
}

// NewCompatIf 按配置选引擎：want=true 用 MPV 兼容模式（mpv 仅解码、应用侧
// 出声，见 engine_pcm.go），false 原生直出。
func NewCompatIf(want bool) *Engine {
	e := &Engine{
		volume: 0.8, obsCh: make(chan struct{}, 1), done: make(chan struct{}),
		started: make(chan struct{}), readTimeout: ipcReadTimeout,
		pcmCompat: want,
	}
	go e.start()
	return e
}

// start 在后台完成 spawn 与 IPC 握手，并把结果交给 started。
func (e *Engine) start() {
	err := e.spawn()
	e.mu.Lock()
	e.startErr = err
	// 启动失败即哑引擎；Close 先到则保持关闭。
	if err != nil {
		e.closed = true
	}
	e.mu.Unlock()
	if err != nil {
		e.markDone()
		fmt.Fprintf(os.Stderr, "audio: mpv 不可用（%v），播放功能禁用\n", err)
	}
	close(e.started)
}

// awaitStart 等后台启动结束。仅播放路径调用；窗口启动路径不等待。
func (e *Engine) awaitStart() error {
	if e.started != nil {
		<-e.started
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startErr
}

// mpvPath 找可用的 mpv 可执行文件。
//
// 三级定位（见 bins.go）：显式路径 → 随包目录 → PATH。
func mpvPath() (mpvExecutable, error) {
	return resolveMPV()
}

// mpvArgs 构造 mpv 子进程参数。逐条都有出处，别随手改：
//
//	--idle                常驻不退出：换曲只 loadfile，省掉每次拉起进程的开销。
//	                      必须是无值形式——写 --idle=yes 会被当成布尔开关，
//	                      mpv 读完即退（Exiting...(Quit)），socket 刚建好进程就没了。
//	--cache=yes           有界前向缓存；--cache-on-disk 保持 no（不落盘是合规硬线）。
//	--stream-lavf-o=...   上游 CDN 半路断流时由 lavf 自己重连，引擎不必操心。
//	--no-config           不读用户 mpv 配置，避免用户设置污染本应用的播放行为。
func mpvArgs(sock string) []string {
	args := []string{
		"--no-video",
		"--no-terminal",
		"--really-quiet",
		"--audio-display=no",
		"--force-window=no",
		"--no-resume-playback",
		"--ytdl=no",
		"--idle",
		"--keep-open=no",
		"--no-config",
		"--input-ipc-server=" + sock,
		// 网络流：mpv 自己管 readahead 与 Range seek
		"--cache=yes",
		"--cache-on-disk=no",
		"--cache-secs=20",
		"--demuxer-max-bytes=64MiB",
		"--demuxer-readahead-secs=8",
		"--volume-max=100",
		// 上游半路断流时自己重连（最多重试到 5s 一次）
		"--stream-lavf-o=reconnect=1,reconnect_streamed=1,reconnect_delay_max=5",
	}
	if ao := os.Getenv("QAA_MPV_AO"); ao != "" {
		args = append(args, "--ao="+ao)
	}
	if dev := os.Getenv("QAA_MPV_DEVICE"); dev != "" {
		args = append(args, "--audio-device="+dev)
	}
	return args
}

// pcmArgs 追加 MPV 兼容模式的 ao_pcm 参数：48kHz/stereo/float32 是应用侧
// PCM 管线的共同地板（见 engine_pcm.go）。音量在应用侧逐采样相乘，mpv 的
// volume 保持 0dB；audio-samplerate/channels/format 强制重采样到固定形状。
func pcmArgs(fifo string) []string {
	return []string{
		"--ao=pcm",
		"--ao-pcm-file=" + fifo,
		"--ao-pcm-waveheader=no",
		"--ao-pcm-append=yes",
		"--audio-samplerate=" + strconv.Itoa(pcmRate),
		"--audio-channels=stereo",
		"--audio-format=float",
	}
}

// spawn 拉起 mpv 并连上 IPC。
func (e *Engine) spawn() error {
	// 握手命令的读超时收紧：后台启动不会挡窗口，但用户可能在启动完成前
	// 退出；没有响应时也不该让引擎/退出路径陪着等满默认的 30 秒。
	defer e.withReadTimeout(2 * time.Second)()

	bin, err := mpvPath()
	if err != nil {
		return err
	}
	// 兼容模式先备好 FIFO 与 speaker：任何一步失败都让整次 spawn 失败，
	// 上层据此回退原生直出（appui 在 New/NewCompat 之间选择）。
	var pcm *pcmOutput
	if e.pcmCompat {
		p, err := newPCMPipe(nil, nil)
		if err != nil {
			return fmt.Errorf("pcm: 管道不可用: %w", err)
		}
		sp, err := newSpeakerFor(os.Getenv("QAA_PCM_SPEAKER"))
		if err != nil {
			p.close()
			return fmt.Errorf("pcm: 音频输出不可用: %w", err)
		}
		if err := sp.Start(); err != nil {
			p.close()
			sp.Close()
			return fmt.Errorf("pcm: 音频输出不可用: %w", err)
		}
		pcm = &pcmOutput{pipe: p, speaker: sp}
		p.onData = pcm.onData
		p.onErr = pcm.onErr
		pcm.vol.set(e.volume)
	}
	// IPC 端点：Unix/macOS 是临时目录里的 AF_UNIX socket；Windows 是
	// 命名管道（mpv 的 Windows IPC 只支持命名管道，见 ipc_windows.go）。
	serverArg, dialIPC, cleanupIPC, err := ipcSetup()
	if err != nil {
		if pcm != nil {
			pcm.speaker.Close()
			pcm.pipe.close()
		}
		return err
	}

	args := mpvArgs(serverArg)
	if pcm != nil {
		args = append(args, pcmArgs(pcm.pipe.path)...)
	}
	cmd := exec.Command(bin.command[0], bin.args(args...)...)
	cmd.Stderr = mpvStderr()
	// quick-sharun 由包内 loader 隔离；旧式目录布局才追加 lib 路径。
	cmd.Env = childEnv(bin)
	configureMPVProcess(cmd)
	if err := cmd.Start(); err != nil {
		cleanupIPC()
		if pcm != nil {
			pcm.speaker.Close()
			pcm.pipe.close()
		}
		return err
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		_ = cmd.Process.Kill()
		cleanupIPC()
		if pcm != nil {
			pcm.speaker.Close()
			pcm.pipe.close()
		}
		return errors.New("引擎已关闭")
	}
	e.cmd = cmd
	e.pcm = pcm
	e.mu.Unlock()

	// exited 是一个原子标志：mpv 进程退出时置位，用于建连失败时快速诊断。
	// Wait 必须紧跟 Start：连接握手期间 mpv 自己退出也要立刻被观察到，
	// 同时负责回收进程，避免 spawn 失败路径留下僵尸子进程。
	var exited atomic.Bool
	go func() {
		_ = cmd.Wait()
		exited.Store(true)
		cleanupIPC()
		e.markDone()
	}()

	var conn ipcConn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := dialIPC(); derr == nil {
			conn = c
			break
		}
		// mpv 可能已经自己退了（比如音频输出初始化失败），再空等没意义。
		if exited.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		_ = cmd.Process.Kill()
		cleanupIPC()
		return errors.New("mpv IPC 未就绪（socket/命名管道未建立，进程可能已退出）")
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		_ = conn.Close()
		_ = cmd.Process.Kill()
		cleanupIPC()
		return errors.New("引擎已关闭")
	}
	e.conn = conn
	e.rd = bufio.NewReaderSize(conn, 64<<10)
	e.wr = bufio.NewWriterSize(conn, 64<<10)
	e.mu.Unlock()

	// 注册观察属性并同步初始音量。
	//
	// 握手期间刻意**不**启动 observe协程：socket 只有一个读消费位，
	// observe 一起来就会抢上读锁并阻塞在读上（它要等事件，可能等很久），
	// 握手的 command() 就再也拿不到锁，双方互等——启动会直接卡死。
	// 握手这几条命令都由 command() 自己读回响应，此时没有竞争者。
	for _, c := range [][]any{
		{"observe_property", 1, "time-pos"},
		{"observe_property", 2, "duration"},
		{"observe_property", 3, "pause"},
		{"observe_property", 4, "eof-reached"},
		{"observe_property", 5, "idle-active"},
		// 缓冲卡顿：mpv 自己会因欠载暂停并重试，这是它相对自研解码链的
		// 主要价值之一，得能观测到（UI 可显示「缓冲中」而不是假装在播）。
		{"observe_property", 6, "paused-for-cache"},
		{"set_property", "volume", strconv.FormatFloat(e.volume*100, 'f', 0, 64)},
	} {
		if err := e.command(c...); err != nil {
			// 连接已经断（EOF）说明 mpv 进程没了，后面的命令必然同样失败，
			// 直接判死并返回，不再空转。
			if e.isClosed() {
				return fmt.Errorf("mpv 启动后断开: %w", err)
			}
			// 音频设备不可用时（无声卡的服务器/容器）mpv 会挂起初始化、
			// 迟迟不回应。这些命令都是「锦上添花」：观察属性不注册只是
			// 进度条不动，音量不同步只是初始音量；进程与 IPC 通道仍在，
			// 后续播放命令仍可能生效，所以只告警不致命。
			fmt.Fprintf(os.Stderr, "audio: mpv 初始化命令未确认（%v）\n", err)
		}
	}

	// 版本探测必须在观察协程起来之前做：那时 socket 上只有这一个读者。
	// mpv ≥ 0.38 的 loadfile 多了一个插入 index 参数（见 loadfileWantsIndex），
	// 布局猜错的代价是「带进度的起播永远失败」，所以这里认清楚再开录。
	if ver, ok := e.getProperty("mpv-version"); ok {
		e.mpvVersion = ver
		e.loadfileIndex = loadfileWantsIndex(ver)
	} else {
		fmt.Fprintln(os.Stderr, "audio: 读不到 mpv-version，loadfile 参数布局按旧版试探（被拒时会自动改用新布局）")
	}

	// 握手完成，此刻才让观察协程接管 socket。
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		_ = conn.Close()
		_ = cmd.Process.Kill()
		return errors.New("引擎已关闭")
	}
	e.mu.Unlock()
	go e.observe()
	return nil
}

// loadfileWantsIndex 判断这台 mpv 的 loadfile 是否需要「插入 index」占位参数。
//
// mpv 0.38.0 把手册里的签名从 loadfile <url> [<flags> [<options>]] 改成
// loadfile <url> [<flags> [<index> [<options>]]]，并且明说这会打断所有把
// options 放在第三位的旧调用：参数类型对不上时 mpv 直接回 invalid parameter，
// 载入根本没有开始。本项目只有「带进度起播」（恢复上次会话的进度、播放中途
// 换音质）会发 options，症状就是「打开后播放卡在加载，然后自己跳下一曲」。
func loadfileWantsIndex(version string) bool {
	major, minor, ok := ParseMPVVersion(version)
	if !ok {
		// 认不出版本就按 0.38 之前的布局发；真被拒了 applyReply 会换布局重发。
		return false
	}
	return major > 0 || minor >= 38
}

// ParseMPVVersion 从 "mpv v0.41.0" / "mpv 0.38.0-27-gabcdef" 取主次版本。
// 设置页判断「MPV 兼容模式」是否适用也用它。
func ParseMPVVersion(s string) (major, minor int, ok bool) {
	nums := make([]int, 0, 2)
	digits := ""
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits += string(r)
			continue
		}
		if digits != "" {
			n, err := strconv.Atoi(digits)
			if err != nil {
				return 0, 0, false
			}
			nums = append(nums, n)
			digits = ""
			if len(nums) == 2 {
				break
			}
		}
	}
	if digits != "" && len(nums) < 2 {
		if n, err := strconv.Atoi(digits); err == nil {
			nums = append(nums, n)
		}
	}
	if len(nums) < 2 {
		return 0, 0, false
	}
	return nums[0], nums[1], true
}

// sendLoadfile 按当前 mpv 的参数布局下发 loadfile，返回命令 id（0 = 没发出去）。
func (e *Engine) sendLoadfile(c loadCmd) int64 {
	return e.commandQuiet(loadfileArgs(c, e.loadfileIndex)...)
}

// loadfileArgs 拼 loadfile 的参数表。纯函数，参数布局由 wantIndex 决定
// （见 loadfileWantsIndex），单测不需要真 mpv。
func loadfileArgs(c loadCmd, wantIndex bool) []any {
	switch {
	case len(c.opts) == 0:
		// 不需要 options 时干脆不发：少一个参数就少一种被拒的可能。
		return []any{"loadfile", c.url, c.flags}
	case wantIndex:
		// index 位对 replace 没有意义，-1 是手册指定的占位。
		return []any{"loadfile", c.url, c.flags, -1, c.opts}
	default:
		return []any{"loadfile", c.url, c.flags, c.opts}
	}
}

// getProperty 同步读一个字符串属性。只在 spawn 握手阶段调用：那时观察协程
// 还没接管 socket，读这一条不会和任何人抢字节。超时收紧到 2s——mpv 不回应
// 时我们按「认不出版本」处理（loadfile 布局先发旧形态，被拒再自愈），
// 而不是把启动卡在这儿。
func (e *Engine) getProperty(name string) (string, bool) {
	defer e.withReadTimeout(2 * time.Second)()
	id, line, err := e.encodeCmd(nil, []any{"get_property", name})
	if err != nil {
		return "", false
	}
	if err := e.write(line); err != nil {
		return "", false
	}
	for {
		resp, err := e.readResp()
		if err != nil {
			return "", false
		}
		if resp.RequestID == id {
			if resp.Error != "" && resp.Error != "success" {
				return "", false
			}
			s, ok := resp.Data.(string)
			return s, ok
		}
		if resp.Event != "" {
			e.applyEvent(resp)
		} else {
			e.applyReply(resp)
		}
	}
}

// command 发一条 IPC 命令并等它的响应。
func (e *Engine) command(args ...any) error {
	id, line, err := e.encodeCmd(nil, args)
	if err != nil {
		return err
	}
	if err := e.write(line); err != nil {
		return err
	}
	return e.waitReply(id)
}

// commandQuiet 异步下发命令：不等响应。
//
// 用于 seek / 音量这类高频命令——同步等响应会让每帧都阻塞在socket 上，
// 而 mpv 的 seek 可能因网络缓冲耗时。async 让 mpv 在后台执行。
func (e *Engine) commandQuiet(args ...any) int64 {
	id, line, err := e.encodeCmd(nil, args)
	if err != nil {
		return 0
	}
	msg := map[string]any{}
	if err := json.Unmarshal(line, &msg); err != nil {
		return 0
	}
	msg["async"] = true
	b, err := json.Marshal(msg)
	if err != nil {
		return 0
	}
	if err := e.write(append(b, '\n')); err != nil {
		return 0
	}
	return id
}

// encodeCmd 把命令编码成一行 IPC JSON（顶层对象形式）。
func (e *Engine) encodeCmd(extra map[string]any, args []any) (int64, []byte, error) {
	e.mu.Lock()
	if e.closed || e.conn == nil {
		e.mu.Unlock()
		return 0, nil, errors.New("mpv 未运行")
	}
	e.nextID++
	id := e.nextID
	e.mu.Unlock()

	obj := map[string]any{
		"command":    args,
		"request_id": id,
	}
	for k, v := range extra {
		obj[k] = v
	}
	line, err := json.Marshal(obj)
	return id, append(line, '\n'), err
}

// write 写一行到 socket（写操作由 mu 串行化）。
func (e *Engine) write(line []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.wr == nil {
		return errors.New("mpv 未运行")
	}
	if _, err := e.wr.Write(line); err != nil {
		return err
	}
	return e.wr.Flush()
}

// waitReply 读事件流直到拿到目标 request_id 的响应。
//
// 读超时是兜底：mpv 进程若在初始化音频设备时卡死（无声卡的服务器/
// 容器里会），响应永远不会来。没有超时的话调用方会被永久阻塞——
// loadfile 就在起播主流程上，一卡整条链就死。
func (e *Engine) waitReply(id int64) error {
	for {
		resp, err := e.readResp()
		if err != nil {
			return err
		}
		if resp.RequestID == id {
			if resp.Error != "" && resp.Error != "success" {
				return fmt.Errorf("mpv: %s", resp.Error)
			}
			return nil
		}
		if resp.Event != "" {
			e.applyEvent(resp)
		} else {
			// 不是我们要的那条回包——但也可能是 loadfile 被拒的告警，
			// 别让它从指缝里漏掉（载入命令被拒 = 播放永远不会开始）。
			e.applyReply(resp)
		}
	}
}

// readResp 读一条 IPC 响应。
//
// 读超时是兜底：mpv 初始化音频设备时若卡死（无声卡的服务器/容器里会），
// 响应永远不会来。没有超时的话调用方会被永久阻塞——loadfile 就在起播
// 主流程上，一卡整条链就死。
//
// 读操作由readMu 串行化：握手命令要等响应、观察协程也要读同一个 socket，
// 两者并发读会互相抢走对方的字节。
func (e *Engine) readResp() (*ipcResp, error) {
	e.readMu.Lock()
	defer e.readMu.Unlock()

	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()
	if conn == nil {
		return nil, errors.New("mpv 未运行")
	}
	// 默认 30s keepalive 既防死连接，也让这个超时可安全复用
	_ = conn.SetReadDeadline(time.Now().Add(e.readTimeoutOrDefault()))
	line, err := e.rd.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp ipcResp
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("mpv IPC 解析失败: %w", err)
	}
	return &resp, nil
}

// observe 消费 mpv 推来的属性事件（常驻协程）。
func (e *Engine) observe() {
	for {
		resp, err := e.readResp()
		if err != nil {
			e.mu.Lock()
			e.closed = true
			e.mu.Unlock()
			return
		}
		if resp.Event == "" {
			e.applyReply(resp)
			continue
		}
		e.applyEvent(resp)
	}
}

// applyEvent 更新观察到的状态，变化时通知观察者。
func (e *Engine) applyEvent(resp *ipcResp) {
	e.mu.Lock()
	changed := false
	switch resp.Event {
	case "property-change":
		// property-change 的形状是 {"event":"property-change","name":..,"data":..}
		name := resp.Name
		switch name {
		case "time-pos":
			if f, ok := toFloat(resp.Data); ok {
				e.pos, changed = f, true
			}
		case "duration":
			if f, ok := toFloat(resp.Data); ok {
				e.dur, changed = f, true
			}
		case "pause":
			if b, ok := resp.Data.(bool); ok {
				e.obsPause, changed = b, true
			}
		case "eof-reached":
			if b, ok := resp.Data.(bool); ok {
				e.eof, changed = b, true
			}
		case "idle-active":
			if b, ok := resp.Data.(bool); ok {
				e.idle, changed = b, true
			}
		case "paused-for-cache":
			if b, ok := resp.Data.(bool); ok {
				e.stalled, changed = b, true
			}
		}
	case "start-file":
		// mpv 开始打开这个文件（还保证不了成功）。给当前载入代次盖章：
		// 之后的 file-loaded / end-file 才归属于本次载入。
		e.startGen = e.loadGen
		if e.pcm != nil {
			// 兼容模式：新曲新 FIFO 世代。旧会话迟到的 PCM 由代次丢弃。
			e.pcm.beginGen(e.loadGen)
		}
	case "file-loaded":
		// 载入成功的权威信号：demuxer 已经把文件打开并准备播了。
		// 但必须先见到本次载入的 start-file——mpv 总是 start-file 在前、
		// file-loaded 在后，所以「没盖章的这一条」只可能是上一档在我们
		// 超时换档之后才迟到的成功，算到当前档头上就会张冠李戴（播的是
		// 旧流的音频，界面却挂着新档的音质标签）。
		if e.startGen != e.loadGen {
			break
		}
		e.eof, e.loadErr, e.loadErrFatal = false, "", false
		e.loadedGen = e.loadGen
		changed = true
	case "end-file":
		// mpv 在播放结束或载入失败时都推 end-file。只认归属于本次载入的那
		// 一条：换档时我们主动打断的上一档也会补一条（reason=stop/error），
		// 把它算到刚下发的这一档头上，就会把好档判成坏档。
		if e.startGen != e.loadGen {
			break
		}
		e.eof = true
		if e.pcm != nil {
			// ao_pcm 的 EOF 只说明 mpv 写完了 FIFO。位置/结束由 PCM 消费侧
			// 接管（position/Ended 走 pcmOutput），这里只标记 mpv 侧收尾。
			e.pcm.markEOF()
		}
		if resp.Reason == "error" {
			// 带 error 原因的是载入失败（网络 404、格式不支持等），要能让
			// player 知道并降档重试；file_error 里是具体原因，比笼统一句话有用。
			msg := resp.FileError
			if msg == "" {
				msg = "mpv 载入失败"
			}
			e.loadErr, e.loadErrGen = msg, e.loadGen
		}
		changed = true
	}
	cb := e.obs
	e.mu.Unlock()
	if changed && cb != nil {
		e.signal()
	}
}

// applyReply 处理命令回包。异步命令没人等返回值，但「loadfile 被 mpv 拒了」
// 必须接住：命令被拒意味着载入压根没开始，不接住就只能干等到超时，把整条
// 降档链一路拖到超时，最后还表现为「自己跳下一曲」。
func (e *Engine) applyReply(resp *ipcResp) {
	e.mu.Lock()
	if resp.RequestID == 0 || resp.RequestID != e.loadReq {
		e.mu.Unlock()
		return
	}
	if resp.Error == "" || resp.Error == "success" {
		e.mu.Unlock()
		return
	}
	if e.loadedGen == e.loadGen || e.loadErrGen == e.loadGen {
		// 本次载入已经有下文了（成功或已报错），这条回包不再需要处理。
		e.mu.Unlock()
		return
	}
	if resp.Error == "invalid parameter" && e.loadRetried != e.loadGen {
		// 十有八九是 loadfile 的参数布局不合这台 mpv 的口味（0.38 前后差一个
		// index 占位）。换成另一种布局原样重发，并记住哪一种能用：版本探测
		// 失配时这就是自愈路径，用户完全无感。
		e.loadRetried = e.loadGen
		e.loadfileIndex = !e.loadfileIndex
		c := e.lastLoad
		e.mu.Unlock()
		if id := e.sendLoadfile(c); id != 0 {
			e.mu.Lock()
			if id > e.loadReq {
				e.loadReq = id
			}
			e.mu.Unlock()
		}
		e.signal()
		return
	}
	// 参数布局之外被拒（命令语法变了、mpv 已经不收命令了……）：与音质档位
	// 无关，降档重试只是白白把整条队列跳完，报给玩家让人一眼看懂。
	e.loadErr = "播放引擎拒绝了载入命令（" + resp.Error + "）"
	e.loadErrGen, e.loadErrFatal = e.loadGen, true
	e.mu.Unlock()
	e.signal()
}

func (e *Engine) signal() {
	select {
	case e.obsCh <- struct{}{}:
	default:
	}
}

// OnObserve 注册状态变化回调（player 用它驱动 UI 刷新）。
//
// 目前只登记、不外发：mpv 的 time-pos 每十几毫秒就来一条，逐条回调等于
// 逐条重绘。obsCh 是留给「合并后再回调」的口子，接上之前 UI 靠 player 的
// 200ms 心跳刷新，进度与歌词最坏晚一个心跳，换来的是稳定的重绘开销。
func (e *Engine) OnObserve(fn func()) {
	e.mu.Lock()
	e.obs = fn
	e.mu.Unlock()
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// ===== 播放控制 =====

// OpenURL 让 mpv 播放 url，并从 startFrac（0..1）处开始。
// url 通常是本地中继的音频流地址，mpv 自行发起 HTTP Range 与重试。
func (e *Engine) OpenURL(url string, startFrac, dur float64, autoplay bool) error {
	if err := e.awaitStart(); err != nil {
		return fmt.Errorf("mpv 不可用：%w", err)
	}
	cmd := loadCmd{url: url, flags: "replace"}
	if startFrac > 0 {
		// 起点必须走 loadfile 的 options.start，不能载入后再 seek：mpv 会
		// 忽略文件就绪前的 seek，而那还得在带缓冲的网络流上多打一次 Range。
		if at := startFrac * dur; at > 0.5 {
			cmd.opts = map[string]any{"start": strconv.FormatFloat(at, 'f', 3, 64)}
		}
	}

	e.mu.Lock()
	if e.closed || e.conn == nil {
		e.mu.Unlock()
		return errors.New("mpv 未运行，无法播放")
	}
	e.wantPaused = !autoplay
	e.eof, e.pos, e.dur = false, 0, 0
	// 开新的一代：上一首的 start-file/file-loaded/end-file 与回包从此不作数。
	e.loadGen++
	e.startGen, e.loadedGen = 0, 0
	e.loadErr, e.loadErrGen, e.loadErrFatal = "", 0, false
	e.lastLoad = cmd
	if e.pcm != nil {
		// 兼容模式：把续播起点钉进 PCM 时间轴；start-file 到来时代次生效。
		e.pcm.beginPending(startFrac * dur)
	}
	e.mu.Unlock()

	// loadfile 异步下发（async:true）。异步有两个理由：
	//  1. mpv 的命令同步执行期间不服务 socket，loadfile 要等文件打开、
	//     元数据读完才返回——网络流这一等可能好几秒，同步等必然顶到读超时。
	//     异步后命令立刻返回，加载结果由事件（file-loaded/end-file）告知。
	//  2. 参数布局随 mpv 版本变（见 loadfileWantsIndex），所以发完还得盯着
	//     回包：被拒时 applyReply 会换一种布局重发，而不是干等到超时。
	if id := e.sendLoadfile(cmd); id != 0 {
		e.mu.Lock()
		e.loadReq = id
		e.mu.Unlock()
	}

	if autoplay {
		e.commandQuiet("set_property", "pause", false)
	} else {
		e.commandQuiet("set_property", "pause", true)
	}
	return nil
}

// Play 恢复播放。
func (e *Engine) Play() {
	e.mu.Lock()
	e.wantPaused = false
	e.mu.Unlock()
	e.commandQuiet("set_property", "pause", false)
}

// Pause 暂停。
func (e *Engine) Pause() {
	e.mu.Lock()
	e.wantPaused = true
	e.mu.Unlock()
	e.commandQuiet("set_property", "pause", true)
}

// Paused 报告是否暂停（本地意图与 mpv 观测取或）。
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.wantPaused || e.obsPause
}

// IsPlaying 报告是否在出声。
func (e *Engine) IsPlaying() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.wantPaused && !e.obsPause && !e.idle && !e.eof
}

// LoadError 返回本次载入失败的原因；空串表示当前无失败。
//
// player 的降档链靠它判断「这一档位 mpv 打不开」——异步载入拿不到同步
// 错误返回，只能由 end-file(error) 事件与命令回包回填。只认盖着当前
// 代次的那一条，上一首失败留下的错误不会污染本次判断。
func (e *Engine) LoadError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErrGen != e.loadGen {
		return ""
	}
	return e.loadErr
}

// LoadErrorFatal 报告 LoadError 是否与音质档位无关（命令被 mpv 拒了、进程
// 已经没了）。这类错误换档重试只会把整条队列白白跳完，player 据此直接收手。
func (e *Engine) LoadErrorFatal() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadErrFatal && e.loadErrGen == e.loadGen
}

// Loaded 报告本次载入是否已经到达成形（mpv 的 file-loaded）。
//
// 这是「这一档到底能不能播」的权威信号。旧实现拿 duration>0 当成功，但时长
// 要等 demuxer 读出元数据才有，而且有些不报时长的流永远拿不到——能播的流就
// 这样被当成坏档，降档、超时、最后自动跳下一曲。
func (e *Engine) Loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadGen != 0 && e.loadedGen == e.loadGen
}

// Ended 报告当前曲目是否自然播完。
func (e *Engine) Ended() bool {
	e.mu.Lock()
	pcm := e.pcm
	eof := e.eof
	e.mu.Unlock()
	if pcm != nil {
		// ao_pcm 的 EOF 是「写完 FIFO」：真正播完要等设备把尾巴消费掉。
		return pcm.ended()
	}
	return eof
}

// Position 当前播放位置（秒），直接取 mpv 的 time-pos。
func (e *Engine) Position() float64 {
	e.mu.Lock()
	pcm := e.pcm
	mpvPos := e.pos
	e.mu.Unlock()
	if pcm != nil && pcm.active() {
		// ao_pcm 无时钟：mpv 的 time-pos 被预写缓冲带偏，位置以设备侧
		// 消费量为准（mpv time-pos 只留给未开会的间隙兜底）。
		return pcm.position()
	}
	return mpvPos
}

// Duration 当前曲目时长（秒）；mpv 尚未报出时回退到已知值。
func (e *Engine) Duration(fallback float64) float64 {
	e.mu.Lock()
	d := e.dur
	e.mu.Unlock()
	if d > 0 {
		return d
	}
	return fallback
}

// SeekTo 跳到 frac（0..1）× dur 秒。
func (e *Engine) SeekTo(frac, dur float64) {
	e.seekAbs(clamp01(frac) * dur)
}

func (e *Engine) seekAbs(sec float64) {
	if sec < 0 {
		sec = 0
	}
	e.mu.Lock()
	pcm := e.pcm
	url := ""
	if pcm != nil {
		url = e.lastLoad.url
	}
	e.mu.Unlock()
	if pcm == nil || url == "" {
		// 原生直出：mpv 自己有时钟，原地精确 seek 即可。
		e.commandQuiet("seek", strconv.FormatFloat(sec, 'f', 3, 64), "absolute+exact")
		return
	}
	// 兼容模式：ao_pcm 是顺序写文件，seek 没有帧标记、时间轴锚点不会自己
	// 动。对齐上游 MPVCompat 的 restartPcmAt——换代重挂：beginPending 把
	// 在途 PCM 全部作废，loadfile 带 start= 重新打开同一 URL，start-file
	// 到来时 beginGen 开新一代 FIFO 会话（pos 已由 beginPending 钉好）。
	e.pcm.beginPending(sec)
	e.mu.Lock()
	if e.closed || e.conn == nil {
		e.mu.Unlock()
		return
	}
	e.loadGen++
	e.startGen, e.loadedGen = 0, 0
	e.loadErr, e.loadErrGen, e.loadErrFatal = "", 0, false
	e.eof, e.pos = false, sec
	// dur 不清零：重挂间隙里时长沿用旧值，等 mpv 重新报。暂停态也不动，
	// loadfile 会保留 pause 属性。
	cmd := loadCmd{url: url, flags: "replace"}
	if sec > 0.5 {
		cmd.opts = map[string]any{"start": strconv.FormatFloat(sec, 'f', 3, 64)}
	}
	e.lastLoad = cmd
	e.mu.Unlock()
	if id := e.sendLoadfile(cmd); id != 0 {
		e.mu.Lock()
		if id > e.loadReq {
			e.loadReq = id
		}
		e.mu.Unlock()
	}
}

// SetVolume 0..1（mpv 的 volume 单位是百分制）。
func (e *Engine) SetVolume(v float64) {
	v = clamp01(v)
	e.mu.Lock()
	e.volume = v
	pcm := e.pcm
	e.mu.Unlock()
	if pcm != nil {
		// ao_pcm 预写了整段音频，改 mpv volume 慢一个缓冲；音量在
		// 应用侧逐采样相乘，捕获端拿到的也是处理后的声音。
		pcm.setVolume(v)
		return
	}
	e.commandQuiet("set_property", "volume", strconv.FormatFloat(v*100, 'f', 0, 64))
}

// SetStartVolume 设置后台启动握手时要同步给 mpv 的音量。它不发 IPC：
// 启动可能还没完成，command 会被丢弃或阻塞；spawn 握手读到的是最新值。
func (e *Engine) SetStartVolume(v float64) {
	v = clamp01(v)
	e.mu.Lock()
	e.volume = v
	e.mu.Unlock()
}

// Stop 停止当前曲目，回到空闲态。
func (e *Engine) Stop() {
	e.mu.Lock()
	e.wantPaused, e.eof, e.pos, e.dur = false, false, 0, 0
	// 停止同样翻页：之前那次载入的 Loaded/LoadError 不再代表现在。
	e.loadGen++
	e.startGen, e.loadedGen = 0, 0
	e.loadErr, e.loadErrGen, e.loadErrFatal = "", 0, false
	if e.pcm != nil {
		e.pcm.stop() // mpv 侧 stop 后 FIFO 不再有新数据，会话即刻作废
	}
	e.mu.Unlock()
	e.commandQuiet("stop")
}

// markDone 只关闭一次退出信号；spawn 失败与 Wait 回收可能同时收口。
func (e *Engine) markDone() {
	e.doneOnce.Do(func() {
		if e.done != nil {
			close(e.done)
		}
	})
}

// Close 终止 mpv 进程（应用退出时调用）。
func (e *Engine) Close() {
	if e.started != nil {
		<-e.started
	}
	e.mu.Lock()
	already := e.closed
	e.closed = true // 即便 spawn 刚失败，也由这里统一收口
	conn, cmd := e.conn, e.cmd
	e.conn = nil
	pcm := e.pcm
	e.pcm = nil
	e.mu.Unlock()
	if pcm != nil {
		pcm.stop()
		pcm.speaker.Close()
		pcm.pipe.close()
	}
	if already {
		return
	}
	if conn != nil {
		_ = conn.Close()
	}
	killed := cmd != nil && cmd.Process != nil
	if killed {
		terminateMPVProcess(cmd)
	}
	// 等 Wait 回收子进程再返回，应用退出后不留 mpv 孤儿/僵尸。
	// spawn 失败时 done 已关；进程早已退出时同样立即返回。
	if killed && e.done != nil {
		select {
		case <-e.done:
		case <-time.After(2 * time.Second):
			fmt.Fprintln(os.Stderr, "audio: 等待 mpv 退出超时")
		}
	}
}

func (e *Engine) readTimeoutOrDefault() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.readTimeout > 0 {
		return e.readTimeout
	}
	return ipcReadTimeout
}

// withReadTimeout 临时改单次读超时（握手探测用），返回还原函数。
func (e *Engine) withReadTimeout(d time.Duration) func() {
	e.mu.Lock()
	prev := e.readTimeout
	e.readTimeout = d
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		e.readTimeout = prev
		e.mu.Unlock()
	}
}

// isClosed 报告引擎/连接是否已断开。
func (e *Engine) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed || e.conn == nil
}

// Stalled 报告是否因缓冲欠载而暂停（mpv 的 paused-for-cache）。
// 网络流卡顿时为 true——UI 应显示「缓冲中」，而不是假装还在播。
func (e *Engine) Stalled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stalled
}

// Available 报告 mpv 是否可用（设置页据此提示安装）。
func (e *Engine) Available() bool {
	if e.started != nil {
		select {
		case <-e.started:
		default:
			// 后台启动还没结束：不能在这里阻塞 UI。探测只回答「可执行文件
			// 是否存在」，真正能否连上 IPC 由首次播放的 awaitStart 决定。
			_, err := mpvPath()
			return err == nil
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.closed
}

// MPVInfo 是设置页展示用的 mpv 状态。
type MPVInfo struct {
	Path    string // 实际使用的可执行文件路径
	Origin  string // explicit / bundled / PATH
	OK      bool
	Detail  string // 失败原因或补充说明
	Version string // mpv-version（握手属性；未启动过时为空）
}

// MPVInfoFor 探测并返回 mpv 状态（设置页「关于」与播放设置页用）。
func MPVInfoFor() MPVInfo {
	bin, path, err := ProbeMPV()
	info := MPVInfo{Path: path, Origin: bin.Origin}
	if err != nil {
		info.Detail = "未检测到 mpv，播放不可用。请用系统包管理器安装后重启应用。"
		return info
	}
	info.OK = true
	switch bin.Origin {
	case "bundled":
		info.Detail = "随应用附带"
	case "explicit":
		info.Detail = "由 QAA_MPV 指定"
	default:
		info.Detail = "系统安装"
	}
	return info
}

// Version 报告最近一次引擎握手读到的 mpv-version；还没握手成功时为空串。
// 设置页用它提示当前 mpv 是否为老版本（「MPV 兼容模式」的适用场景）。
func (e *Engine) Version() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mpvVersion
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
