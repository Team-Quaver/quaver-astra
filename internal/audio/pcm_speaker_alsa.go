//go:build linux && !alsa_cgo

package audio

// Linux ALSA 实时输出：直接对 /dev/snd/pcmC*D*p 做 ioctl（SNDRV_PCM_*），
// 全程 syscall，不链 libasound、不引 cgo——与 bins.go 的交叉编译前提一致。
//
// 选择/协商策略（本机实测过 UR22C 与 HDA PCH）：
//   - 设备按 card 号升序探测第一个可打开的 playback；
//   - 采样率先报 48000（ao_pcm 固定形状），协商不中再放宽；
//   - 采样格式按 S16_LE → S32_LE → FLOAT_LE 试（USB 声卡普遍拒 S16）；
//   - 声道 2，period 2400 帧（50ms），buffer 0.5s。
//   - ioctl XFER 是阻塞语义：设备缓冲满时写会睡到腾出 period，天然背压。
//     underrun 后内核自动重启流（ exposed as snd_pcm_recover 的行为），
//     实测欠载后继续写即恢复，无需显式 PREPARE。
import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ===== 内核 uapi/sound/asound.h 摘录（只取用到的常量与结构）=====

type alsaStream int32

const (
	alsaStreamPlayback alsaStream = 0
)

type alsaAccess int32

const alsaAccessRWInterleaved alsaAccess = 3

type alsaFormat int32

const (
	alsaFmtS16LE alsaFormat = 2
	alsaFmtS32LE alsaFormat = 10
	alsaFmtF32LE alsaFormat = 14

	alsaSubformatStd = 0
)

// SNDRV_PCM_IOCTL_*（_IOR/_IOW/_IO 宏展开，'A'=0x41；逐值核对自
// linux/uapi/sound/asound.h 667-701 行）。
const (
	snPcmIoctlPVersion  = 0x80044100 // _IOR('A',0x00,int)
	snPcmIoctlSwParams  = 0xc0884113 // _IOWR('A',0x13,sw_params=136B)
	snPcmIoctlHwRefine  = 0xc2604110 // _IOWR('A',0x10,hw_params)
	snPcmIoctlHwParams  = 0xc2604111 // _IOWR('A',0x11,hw_params)
	snPcmIoctlHwFree    = 0x00004112 // _IO('A',0x12)
	snPcmIoctlHwPrepare = 0x00004140 // _IO('A',0x40)
	snPcmIoctlHwStart   = 0x00004142 // _IO('A',0x42)
	snPcmIoctlHwDrop    = 0x00004143 // _IO('A',0x43)
	snPcmIoctlHwXfer    = 0x40184150 // _IOW('A',0x50,snd_xferi=24B)
)

// alsaMask 镜像 snd_mask：只有 bits[8]（SNDRV_MASK_MAX=256 位）。
type alsaMask struct {
	Bits [8]uint32
}

// snd_interval 是位域 {min,max,openmin:1,openmax:1,integer:1,empty:1}；
// 用原始 u32 镜像（小端：低 4 位是 openmin/openmax/integer/empty）。
type alsaInterval struct {
	Min   uint32
	Max   uint32
	Flags uint32 // bit0 openmin, bit1 openmax, bit2 integer, bit3 empty
}

const intervalEmpty = 1 << 3

func alsaIntervalFull() alsaInterval { return alsaInterval{Min: 0, Max: 0xFFFFFFFF} }

// alsaHWParams 镜像内核 struct snd_pcm_hw_params（64 位 608B，逐字段核对过
// offsetof）：masks 是 3 个真实槽 + 5 个保留槽，intervals 是 12 个真实槽
// （SAMPLE_BITS..TICK_TIME）+ 9 个保留槽；尾部 fifo_size 在 64 位上是
// unsigned long，布局错会让内核回写越过 Go 结构体边界。
type alsaHWParams struct {
	Flags     uint32
	Masks     [8]alsaMask      // masks[3] + mres[5]，各 32B
	Intervals [21]alsaInterval // intervals[12] + ires[9]，各 12B
	Rmask     uint32
	Cmask     uint32
	Info      uint32
	Msbits    uint32
	RateNum   uint32
	RateDen   uint32
	FifoSize  uint64
	Rsvd      [64]byte
}

// 参数下标（与 uapi 对齐）：
const (
	hwParamAccess     = 0
	hwParamFormat     = 1
	hwParamSubformat  = 2
	hwParamSampleBits = 8
	hwParamChannels   = 10
	hwParamRate       = 11
	hwParamPeriodSize = 13
	hwParamBufferSize = 17
)

func (p *alsaHWParams) mask(i int) *alsaMask {
	return &p.Masks[i-hwParamAccess]
}

func (p *alsaHWParams) interval(i int) *alsaInterval {
	return &p.Intervals[i-hwParamSampleBits]
}

// reset 把全部槽位放开为「任意」，rmask 置全 1 表示所有参数都可修改。
func (p *alsaHWParams) reset() {
	for i := range p.Intervals {
		p.Intervals[i] = alsaIntervalFull()
	}
	for i := range p.Masks {
		for j := range p.Masks[i].Bits {
			p.Masks[i].Bits[j] = 0xFFFFFFFF
		}
	}
	p.Rmask = 0xFFFFFFFF
}

func (m *alsaMask) setOnly(bit int) {
	for i := range m.Bits {
		m.Bits[i] = 0
	}
	m.Bits[bit/32] = 1 << (bit % 32)
}

func (m *alsaMask) has(bit int) bool {
	return m.Bits[bit/32]&(1<<(bit%32)) != 0
}

func (iv *alsaInterval) setSingle(v uint32) {
	iv.Min, iv.Max, iv.Flags = v, v, 0
}

func (iv *alsaInterval) isEmpty() bool { return iv.Flags&intervalEmpty != 0 }

// snd_xferi：{result(s64), buf, frames(u64)}——result 在前。
type alsaXferI struct {
	Result int64
	Buf    uintptr
	Frames uint64
}

// snd_pcm_sw_params（uapi 顺序）：tstamp_mode, period_step, sleep_min,
// avail_min, xfer_align, start_threshold, stop_threshold, silence_threshold,
// silence_size, boundary(u64), proto, tstamp_type。
// snd_pcm_uframes_t 在 64 位平台是 u64，32 位是 u32——只支持 64 位构建
// （与发行矩阵一致），用固定 u64 保持布局稳定。
type alsaSwParams struct {
	TstampMode    uint32
	PeriodStep    uint32
	SleepMin      uint32
	AvailMin      uint64
	XferAlign     uint64
	StartThresh   uint64
	StopThresh    uint64
	SilenceThresh uint64
	SilenceSize   uint64
	Boundary      uint64
	Proto         uint32
	TstampType    uint32
	Rsvd          [56]byte
}

// alsaSpeaker 是 Linux 实时输出。
type alsaSpeaker struct {
	mu sync.Mutex

	pcm    *os.File // /dev/snd/pcmC*D*p
	ctl    *os.File // /dev/snd/controlC*（PCM info 枚举用，可空）
	period uint32   // 帧数
	buffer uint32   // 总缓冲帧数
	ch     uint32
	fmt    alsaFormat
	closed bool

	// 阻塞写模式不需要自旋；underrun 后内核自行恢复。
	pending []float32
}

func newALSASpeaker() (pcmSpeaker, error) {
	dev, err := openALSAPCM()
	if err != nil {
		return nil, err
	}
	s := &alsaSpeaker{pcm: dev}
	if err := s.negotiate(); err != nil {
		dev.Close()
		return nil, err
	}
	return s, nil
}

// ALSA hw 直通是 Linux 的兜底/显式选择出口。默认分派点在
// pcm_speaker_pulse.go 的 init（Pulse 优先、ALSA 兜底）；用户显式点名
// "alsa" 时经 newALSASpeakerForced 强制走这里，绕过 Pulse。
func init() {
	speakerFactory = func() (pcmSpeaker, error) { return newALSASpeaker() }
	newALSASpeakerForced = newALSASpeaker
}

// openALSAPCM 找第一个可用的 playback 设备（card 升序、device 升序）。
// /dev/snd 里只筛 pcmC*D*p；打不开（被占满/EACCES）就试下一个。
func openALSAPCM() (*os.File, error) {
	entries, err := os.ReadDir("/dev/snd")
	if err != nil {
		return nil, fmt.Errorf("pcm: 读 /dev/snd 失败: %w", err)
	}
	names := make([]string, 0, 8)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "pcmC") && strings.HasSuffix(n, "p") {
			names = append(names, n)
		}
	}
	// pcmC<card>D<dev>p：card 升序、device 升序，与 aplay 的默认选择一致。
	sort.Slice(names, func(i, j int) bool {
		ci, di := pcmDevOrder(names[i])
		cj, dj := pcmDevOrder(names[j])
		if ci != cj {
			return ci < cj
		}
		return di < dj
	})
	// 不能用 os.OpenFile：它会往 runtime netpoller 里注册字符设备并把
	// fd 改回 O_NONBLOCK，ioctl XFER 从此只会 EAGAIN 永不阻塞。用裸
	// syscall.Open 拿到阻塞 fd，再交给 os.NewFile（它对阻塞设备 fd 不挂
	// poller）包装回 *os.File。
	var lastErr error
	for _, n := range names {
		// O_NONBLOCK：被独占的节点 open 直接 EBUSY，不会挂起调用方。
		path := filepath.Join("/dev/snd", n)
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			lastErr = &os.PathError{Op: "open", Path: path, Err: err}
			continue
		}
		// PVERSION 判定节点可用；随后清掉 O_NONBLOCK 回到阻塞写语义
		// （speaker 依赖阻塞 XFER 的设备时钟节拍）。
		var ver int32
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
			uintptr(snPcmIoctlPVersion), uintptr(unsafe.Pointer(&ver))); e != 0 {
			_ = syscall.Close(fd)
			lastErr = fmt.Errorf("%s: PVERSION: %v", n, e)
			continue
		}
		// open 时带 O_NONBLOCK 防止被独占设备挂死 open；确认可用后清掉，
		// 否则 XFER 不会阻塞、永远赶在设备时钟前面写满缓冲。
		if _, _, fe := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd),
			uintptr(syscall.F_GETFL), 0); fe != 0 {
			_ = syscall.Close(fd)
			lastErr = fmt.Errorf("%s: F_GETFL: %v", n, fe)
			continue
		}
		if _, _, fe := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd),
			uintptr(syscall.F_SETFL), 0); fe != 0 {
			_ = syscall.Close(fd)
			lastErr = fmt.Errorf("%s: 清 O_NONBLOCK 失败: %v", n, fe)
			continue
		}
		return os.NewFile(uintptr(fd), path), nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("pcm: 打开 ALSA 设备失败: %w", lastErr)
	}
	return nil, errors.New("pcm: /dev/snd 下没有 playback 设备")
}

func pcmDevOrder(name string) (card, dev int) {
	c, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "pcmC"), ""))
	di := strings.Index(name, "D")
	d, _ := strconv.Atoi(strings.TrimSuffix(name[di+1:], "p"))
	return c, d
}

// negotiate 走 HW_REFINE 把设备参数钉到 ao_pcm 的固定形状。
func (s *alsaSpeaker) negotiate() error {
	var ver uint32
	if err := s.ioctlP(snPcmIoctlPVersion, unsafe.Pointer(&ver)); err != nil {
		return err
	}

	// 先 HW_REFINE 拿设备支持集（内核把不支持的位清掉），再挑形状。
	space := alsaHWParams{}
	space.reset()
	if err := s.ioctlP(snPcmIoctlHwRefine, unsafe.Pointer(&space)); err != nil {
		return fmt.Errorf("pcm: hw_refine 失败: %w", err)
	}
	fmtAlsa, ok := pickFormat(&space)
	if !ok {
		return errors.New("pcm: 声卡不支持 S16/S32/FLOAT 采样格式")
	}
	rate, ok := pickRate(&space, pcmRate)
	if !ok {
		return errors.New("pcm: 声卡不支持 48kHz 附近的采样率")
	}

	apply := func(periodSize uint32) error {
		p := alsaHWParams{}
		p.reset()
		p.mask(hwParamAccess).setOnly(int(alsaAccessRWInterleaved))
		p.mask(hwParamFormat).setOnly(int(fmtAlsa))
		p.mask(hwParamSubformat).setOnly(0) // SNDRV_PCM_SUBFORMAT_STD
		p.interval(hwParamChannels).setSingle(2)
		p.interval(hwParamRate).setSingle(rate)
		if periodSize > 0 {
			p.interval(hwParamPeriodSize).setSingle(periodSize)
		}
		// buffer 钉成 4×period（约 200ms）：太小一有调度抖动就欠载，太大
		// 阻塞写会提前积压、音量/进度跟随变迟钝。
		p.interval(hwParamBufferSize).setSingle(4 * periodSize)
		if err := s.ioctlP(snPcmIoctlHwParams, unsafe.Pointer(&p)); err != nil {
			return err
		}
		// 设备会把最终生效值回填；period/buffer 取回填区间（单值时 min==max）。
		s.period = p.interval(hwParamPeriodSize).Min
		if s.period == 0 || s.period > pcmRate {
			s.period = 2400
		}
		s.buffer = p.interval(hwParamBufferSize).Min
		if s.buffer < 2*s.period {
			s.buffer = 4 * s.period
		}
		return nil
	}
	errP := apply(2400)
	if errP != nil {
		// period 大小不受支持：放开约束让设备自选。
		if err2 := apply(0); err2 != nil {
			return fmt.Errorf("pcm: 参数协商失败: %w", err2)
		}
	}
	s.fmt = fmtAlsa
	s.ch = 2

	// sw_params 的语义：start_threshold 是「缓冲里攒够多少帧才起播」，
	// stop_threshold 是「空闲帧数超过多少就判 XRUN」——后者必须设成整个
	// buffer（内核默认值），设小了第一次写入后空闲空间必然远超阈值，
	// 流会被立刻打成 XRUN，下一次 XFER 即 EPIPE。boundary 取不小于
	// 2×buffer 的 2 的幂（内核会自行校正，对齐 alsa-lib 行为）。
	boundary := uint64(s.buffer) * 2
	for boundary&(boundary-1) != 0 {
		boundary &= boundary - 1
	}
	boundary <<= 1
	if s.ioctlP(snPcmIoctlSwParams, unsafe.Pointer(&alsaSwParams{
		AvailMin:    uint64(s.period),
		XferAlign:   1,
		StartThresh: uint64(s.period),
		StopThresh:  uint64(s.buffer),
		Boundary:    boundary,
	})) != nil {
		fmt.Fprintln(os.Stderr, "audio: pcm sw_params 被拒（用内核默认值继续）")
	}
	if err := s.ioctlP(snPcmIoctlHwPrepare, nil); err != nil {
		return fmt.Errorf("pcm: prepare 失败: %w", err)
	}
	// PREPARE 后状态即 ready；第一次 XFER 会隐式 START。
	return nil
}

// pickFormat 在设备支持集里按偏好挑采样格式（S16 优先：兼容性最好）。
func pickFormat(info *alsaHWParams) (alsaFormat, bool) {
	for _, f := range []alsaFormat{alsaFmtS16LE, alsaFmtS32LE, alsaFmtF32LE} {
		if info.mask(hwParamFormat).has(int(f)) {
			return f, true
		}
	}
	return 0, false
}

// pickRate 挑 48k；设备硬性不支持时退到支持区间里最接近的值。
func pickRate(info *alsaHWParams, want uint32) (uint32, bool) {
	iv := info.interval(hwParamRate)
	if iv.isEmpty() {
		return 0, false
	}
	if want < iv.Min {
		return iv.Min, true
	}
	if want > iv.Max {
		return iv.Max, true
	}
	return want, true
}

func (s *alsaSpeaker) ioctlP(cmd uint32, ptr unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, s.pcm.Fd(), uintptr(cmd), uintptr(ptr))
	if e != 0 {
		return fmt.Errorf("pcm: ioctl 0x%x 失败: %v", cmd, e)
	}
	return nil
}

// Start 满足 pcmSpeaker；协商已在构造时完成。
func (s *alsaSpeaker) Start() error { return nil }

// Play 把 interleaved 帧按设备格式编码后 ioctl XFER。阻塞语义：
// 设备缓冲满时内核睡到有 period 空位，天然与 48kHz 消费对齐。
func (s *alsaSpeaker) Play(samples []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("pcm: 输出已关闭")
	}
	if len(samples) == 0 {
		return nil
	}
	frames := len(samples) / pcmChannels
	buf := encodeSamples(samples, s.fmt)
	x := alsaXferI{
		Buf:    uintptr(unsafe.Pointer(&buf[0])),
		Frames: uint64(frames),
	}
	if err := s.ioctlP(snPcmIoctlHwXfer, unsafe.Pointer(&x)); err != nil {
		// underrun/overrun：显式 PREPARE 后重试一次（内核多数情况已自愈，
		// 这里兜底让异常状态必回到 ready）。
		if perr := s.ioctlP(snPcmIoctlHwPrepare, nil); perr == nil {
			if err2 := s.ioctlP(snPcmIoctlHwXfer, unsafe.Pointer(&x)); err2 == nil {
				return nil
			} else {
				return err2
			}
		}
		return err
	}
	return nil
}

// encodeSamples float32 交错帧 → 设备格式的字节缓冲。
func encodeSamples(samples []float32, f alsaFormat) []byte {
	switch f {
	case alsaFmtS16LE:
		out := make([]byte, len(samples)*2)
		for i, v := range samples {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(clampF(v)*32767)))
		}
		return out
	case alsaFmtS32LE:
		out := make([]byte, len(samples)*4)
		for i, v := range samples {
			binary.LittleEndian.PutUint32(out[i*4:], uint32(int32(clampF(v)*2147483647)))
		}
		return out
	default: // FLOAT_LE
		out := make([]byte, len(samples)*4)
		for i, v := range samples {
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
		}
		return out
	}
}

func clampF(v float32) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return float64(v)
}

func (s *alsaSpeaker) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	// DROP 立即停下并丢弃缓冲；失败不致命（设备可能已自行关闭）。
	_ = s.ioctlP(snPcmIoctlHwDrop, nil)
	_ = s.pcm.Close()
	if s.ctl != nil {
		_ = s.ctl.Close()
	}
}
