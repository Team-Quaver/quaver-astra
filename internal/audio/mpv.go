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
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	conn     net.Conn
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
	loadErr  string // 最近一次载入失败的原因（空 = 正常）

	obs   func()
	obsCh chan struct{}
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
}

// New 启动 mpv 子进程并完成 IPC 握手。失败时返回哑引擎：所有操作退化为
// 返回错误，调用方无需区分「无音频设备」与「无 mpv」。
func New() *Engine {
	e := &Engine{volume: 0.8, obsCh: make(chan struct{}, 1), done: make(chan struct{})}
	if err := e.spawn(); err != nil {
		e.closed = true
		e.markDone()
		fmt.Fprintf(os.Stderr, "audio: mpv 不可用（%v），播放功能禁用\n", err)
		return e
	}
	return e
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

// spawn 拉起 mpv 并连上 IPC。
func (e *Engine) spawn() error {
	bin, err := mpvPath()
	if err != nil {
		return err
	}
	// socket 目录：优先用系统临时目录；某些环境（受限容器）下它不可用，
	// 退回用户缓存目录。
	dir, err := os.MkdirTemp("", "quaver-astra-mpv-")
	if err != nil {
		base, berr := os.UserCacheDir()
		if berr != nil {
			return err
		}
		if mkerr := os.MkdirAll(base, 0o700); mkerr != nil {
			return err
		}
		dir, err = os.MkdirTemp(base, "quaver-astra-mpv-")
		if err != nil {
			return err
		}
	}
	sock := filepath.Join(dir, "ipc.sock")

	cmd := exec.Command(bin.command[0], bin.args(mpvArgs(sock)...)...)
	cmd.Stderr = os.Stderr
	// quick-sharun 由包内 loader 隔离；旧式目录布局才追加 lib 路径。
	cmd.Env = childEnv(bin)
	configureMPVProcess(cmd)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return err
	}
	e.cmd = cmd

	// exited 是一个原子标志：mpv 进程退出时置位，用于建连失败时快速诊断。
	// Wait 必须紧跟 Start：连接握手期间 mpv 自己退出也要立刻被观察到，
	// 同时负责回收进程，避免 spawn 失败路径留下僵尸子进程。
	var exited atomic.Bool
	go func() {
		_ = cmd.Wait()
		exited.Store(true)
		os.RemoveAll(dir)
		e.markDone()
	}()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.Dial("unix", sock); derr == nil {
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
		os.RemoveAll(dir)
		if _, err := os.Stat(sock); err != nil {
			return fmt.Errorf("mpv 未创建 IPC socket（进程可能已退出，检查 mpv 是否可正常启动）")
		}
		return errors.New("mpv IPC 未就绪")
	}
	e.conn = conn
	e.rd = bufio.NewReaderSize(conn, 64<<10)
	e.wr = bufio.NewWriterSize(conn, 64<<10)

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

	// 握手完成，此刻才让观察协程接管 socket。
	go e.observe()
	return nil
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
func (e *Engine) commandQuiet(args ...any) {
	_, line, err := e.encodeCmd(nil, args)
	if err != nil {
		return
	}
	msg := map[string]any{}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	msg["async"] = true
	if b, err := json.Marshal(msg); err == nil {
		_ = e.write(append(b, '\n'))
	}
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
	// 30s keepalive 既防死连接，也让这个超时可安全复用
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
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
	case "file-loaded":
		e.eof, changed = false, true
		e.loadErr = ""
	case "end-file":
		// mpv 在播放结束或载入失败时都推 end-file。带 error 原因的是载入
		// 失败（网络 404、格式不支持等），要能让player 知道并降档重试。
		if resp.Reason == "error" {
			e.eof = true
			e.loadErr = "mpv 载入失败"
		} else {
			e.eof, changed = true, true
		}
	}
	cb := e.obs
	e.mu.Unlock()
	if changed && cb != nil {
		e.signal()
	}
}

func (e *Engine) signal() {
	select {
	case e.obsCh <- struct{}{}:
	default:
	}
}

// OnObserve 注册状态变化回调（player 用它驱动 UI 刷新）。
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
	e.mu.Lock()
	if e.closed || e.conn == nil {
		e.mu.Unlock()
		return errors.New("mpv 未运行，无法播放")
	}
	e.wantPaused = !autoplay
	e.eof, e.pos, e.dur = false, 0, 0
	e.loadErr = ""
	e.mu.Unlock()

	// loadfile 异步下发（async:true）。
	//
	// 两个理由：
	//  1. mpv 的命令同步执行期间不服务 socket，loadfile 要等文件打开、
	//     元数据读完才返回——网络流这一等可能好几秒，同步等必然顶到读超时。
	//     异步后命令立刻返回，加载结果由事件告知。
	//  2. 起点必须走 loadfile 的 options.start，不能载入后再 seek：mpv 会
	//     忽略文件就绪前的 seek，而那还得在带缓冲的网络流上多打一次 Range。
	opts := map[string]any{}
	if startFrac > 0 {
		if at := startFrac * dur; at > 0.5 {
			opts["start"] = strconv.FormatFloat(at, 'f', 3, 64)
		}
	}
	var args []any
	if len(opts) > 0 {
		args = []any{"loadfile", url, "replace", opts}
	} else {
		args = []any{"loadfile", url, "replace"}
	}
	e.commandQuiet(args...)

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

// LoadError 返回最近一次载入失败的原因；空串表示当前无失败。
//
// player 的降档链靠它判断「这一档位 mpv 打不开」——异步载入拿不到同步
// 错误返回，只能由 end-file(error) 事件回填。
func (e *Engine) LoadError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadErr
}

// Ended 报告当前曲目是否自然播完。
func (e *Engine) Ended() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.eof
}

// Position 当前播放位置（秒），直接取 mpv 的 time-pos。
func (e *Engine) Position() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pos
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
	// absolute+exact：精确 seek 到目标时间点。默认的 absolute 会落到
	// 关键帧上，听感上是「跳早了几百毫秒」。
	e.commandQuiet("seek", strconv.FormatFloat(sec, 'f', 3, 64), "absolute+exact")
}

// SetVolume 0..1（mpv 的 volume 单位是百分制）。
func (e *Engine) SetVolume(v float64) {
	v = clamp01(v)
	e.mu.Lock()
	e.volume = v
	e.mu.Unlock()
	e.commandQuiet("set_property", "volume", strconv.FormatFloat(v*100, 'f', 0, 64))
}

// Stop 停止当前曲目，回到空闲态。
func (e *Engine) Stop() {
	e.mu.Lock()
	e.wantPaused, e.eof, e.pos, e.dur = false, false, 0, 0
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
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	conn, cmd := e.conn, e.cmd
	e.conn = nil
	e.mu.Unlock()
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
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.closed
}

// MPVInfo 是设置页展示用的 mpv 状态。
type MPVInfo struct {
	Path   string // 实际使用的可执行文件路径
	Origin string // explicit / bundled / PATH
	OK     bool
	Detail string // 失败原因或补充说明
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

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
