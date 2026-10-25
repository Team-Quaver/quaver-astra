//go:build windows

package audio

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows WASAPI 共享模式实时输出（纯 syscall，无 cgo）。
//
// 线程模型与 mpv 的 native 输出一致：一个 Render 线程被 WASAPI 的
// SetEvent 唤醒，从 lock-free 队列里取 float32 写进设备，没数据就写静音。
// 队列由 engine 的 PCM 管道泵灌入（devicePeriod 约 10ms）。
type wasapiSpeaker struct {
	mu sync.Mutex
	// COM/COM 侧指针；Start 后有效。
	activate   uintptr // IAudioClient
	render     uintptr // IAudioRenderClient
	clientVtbl *wasapiClientVtbl
	renderVtbl *wasapiRenderVtbl
	event      windows.Handle
	bufFrames  uint32
	frameBytes uint32
	queue      []float32
	queueMu    sync.Mutex
	done       chan struct{}
	doneOnce   sync.Once
	started    bool
	stopped    bool
	coInitDone bool
}

// ===== COM 布局（仅用到的方法槽位）=====

// IUnknown: QueryInterface(0) AddRef(1) Release(2)
// IAudioClient 从 3 起：Initialize(3) GetBufferSize(4) GetStreamLatency(5)
// GetCurrentPadding(6) IsFormatSupported(7) GetMixFormat(8) GetDevicePeriod(9)
// Start(10) Stop(11) Reset(12) SetEventHandle(13) GetService(14)
type wasapiClientVtbl struct {
	queryInterface    uintptr
	addRef            uintptr
	release           uintptr
	initialize        uintptr
	getBufferSize     uintptr
	getStreamLatency  uintptr
	getCurrentPadding uintptr
	isFormatSupported uintptr
	getMixFormat      uintptr
	getDevicePeriod   uintptr
	start             uintptr
	stop              uintptr
	reset             uintptr
	setEventHandle    uintptr
	getService        uintptr
}

// IAudioRenderClient 从 3 起：GetBuffer(3) ReleaseBuffer(4)
type wasapiRenderVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	getBuffer      uintptr
	releaseBuffer  uintptr
}

var (
	modOle32 = windows.NewLazySystemDLL("ole32.dll")
	modMmapi = windows.NewLazySystemDLL("mmdevapi.dll")

	procCoInitializeEx      = modOle32.NewProc("CoInitializeEx")
	procCoCreateInstance    = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree       = modOle32.NewProc("CoTaskMemFree")
	procCreateEventW        = modKernel32.NewProc("CreateEventW")
	procWaitForSingleObject = modKernel32.NewProc("WaitForSingleObject")
	procCloseHandle         = modKernel32.NewProc("CloseHandle")

	// CLSID_MMDeviceEnumerator / IID_IMMDeviceEnumerator / IID_IAudioClient
	clsidEnumerator = windows.GUID{Data1: 0xBCDE0394, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidEnumerator   = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidAudioClient  = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4c32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidAudioRender  = windows.GUID{Data1: 0xF294ACFC, Data2: 0x3146, Data3: 0x4483, Data4: [8]byte{0xA7, 0xBF, 0xAD, 0xDC, 0xA7, 0xC2, 0x60, 0xE2}}
)

var modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

const (
	coInitMultithreaded = 0x0
	clsCtxAll           = 23 // CLSCTX_ALL
	// AUDCLNT_STREAMFLAGS_EVENTCALLBACK | AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM |
	// AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY
	streamFlags        = 0x00040000 | 0x80000000 | 0x08000000
	shareModeShared    = 0
	hnsBufferDuration  = 0 // 让 WASAPI 自己挑（共享模式默认约 10ms×2）
	hnsPeriodicity     = 0
	waitInfinite       = 0xFFFFFFFF
	waitObject0        = 0
	waitFailed         = 0xFFFFFFFF
	audclntBufferEmpty = 0
)

// waveFormatExtensible 的最小够用版：48k/stereo/float32。
type waveFormat struct {
	tag        uint16
	channels   uint16
	rate       uint32
	avgBytes   uint32
	blockAlign uint16
	bits       uint16
	extSize    uint16
	extValid   uint16 // wValidBitsPerSample
	extChannel uint32 // dwChannelMask（stereo=3）
	extSub     windows.GUID
}

var guidIEEEFloat = windows.GUID{Data1: 3, Data4: [8]byte{0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA}}

func init() {
	speakerFactory = func() (pcmSpeaker, error) {
		return newWASAPISpeaker()
	}
}

func newWASAPISpeaker() (pcmSpeaker, error) {
	w := &wasapiSpeaker{done: make(chan struct{})}
	if err := w.open(); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

func (w *wasapiSpeaker) open() error {
	if hr, _, _ := procCoInitializeEx.Call(0, coInitMultithreaded); hr != 0 && hr != 1 && hr != 0x80010106 {
		return fmt.Errorf("pcm: CoInitializeEx 失败 (0x%x)", hr)
	}
	w.coInitDone = true

	var enum uintptr
	if hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidEnumerator)), 0, clsCtxAll,
		uintptr(unsafe.Pointer(&iidEnumerator)), uintptr(unsafe.Pointer(&enum))); hr != 0 || enum == 0 {
		return fmt.Errorf("pcm: 创建设备枚举器失败 (0x%x)", hr)
	}
	// IMMDeviceEnumeratorVtbl: QI,AddRef,Release,EnumAudioEndpoints,GetDefaultAudioEndpoint
	enumVtbl := (**struct{ _ [5]uintptr })(unsafe.Pointer(*(**uintptr)(unsafe.Pointer(enum))))
	releaseFn := uintptrAt(unsafe.Pointer(enumVtbl), 2)
	defer syscall.SyscallN(releaseFn, enum)

	var dev uintptr
	// GetDefaultAudioEndpoint(eRender=0, eMultimedia=1, &dev)
	syscall.SyscallN(uintptrAt(unsafe.Pointer(enumVtbl), 4), enum, 0, 1, uintptr(unsafe.Pointer(&dev)))
	if dev == 0 {
		return errors.New("pcm: 找不到默认音频设备")
	}
	devVtbl := (**struct{ _ [6]uintptr })(unsafe.Pointer(*(**uintptr)(unsafe.Pointer(dev))))
	defer syscall.SyscallN(uintptrAt(unsafe.Pointer(devVtbl), 2), dev)

	var endpoint uintptr
	// IMMDevice::Activate(iidAudioClient, CLSCTX_ALL, nil, &endpoint)
	if hr, _, _ := syscall.SyscallN(uintptrAt(unsafe.Pointer(devVtbl), 3),
		dev, uintptr(unsafe.Pointer(&iidAudioClient)), clsCtxAll, 0, uintptr(unsafe.Pointer(&endpoint))); hr != 0 || endpoint == 0 {
		return fmt.Errorf("pcm: 激活音频客户端失败 (0x%x)", hr)
	}
	w.activate = endpoint
	w.clientVtbl = (*wasapiClientVtbl)(unsafe.Pointer(*(**uintptr)(unsafe.Pointer(endpoint))))

	format := waveFormat{
		tag: 0xFFFE, channels: pcmChannels, rate: pcmRate,
		avgBytes: pcmRate * pcmChannels * 4, blockAlign: pcmChannels * 4,
		bits: 32, extSize: 22, extValid: 32, extChannel: 3, extSub: guidIEEEFloat,
	}
	// Initialize(AUDCLNT_SHAREMODE_SHARED=0, flags, 0, 0, format, nil)
	if hr, _, _ := syscall.SyscallN(w.clientVtbl.initialize, w.activate,
		shareModeShared, streamFlags, hnsBufferDuration, hnsPeriodicity,
		uintptr(unsafe.Pointer(&format)), 0); hr != 0 {
		return fmt.Errorf("pcm: 初始化音频流失败 (0x%x)", hr)
	}
	if hr, _, _ := syscall.SyscallN(w.clientVtbl.getBufferSize, w.activate, uintptr(unsafe.Pointer(&w.bufFrames))); hr != 0 {
		return fmt.Errorf("pcm: 读取缓冲区大小失败 (0x%x)", hr)
	}
	w.frameBytes = w.bufFrames * uint32(pcmChannels*4)

	ev, _, err := procCreateEventW.Call(0, 0, 0, 0)
	if ev == 0 {
		return fmt.Errorf("pcm: 创建事件失败: %v", err)
	}
	w.event = windows.Handle(ev)
	if hr, _, _ := syscall.SyscallN(w.clientVtbl.setEventHandle, w.activate, ev); hr != 0 {
		return fmt.Errorf("pcm: 绑定事件句柄失败 (0x%x)", hr)
	}

	var render uintptr
	// IAudioClient::GetService(iidAudioRender, &render)
	if hr, _, _ := syscall.SyscallN(w.clientVtbl.getService, w.activate,
		uintptr(unsafe.Pointer(&iidAudioRender)), uintptr(unsafe.Pointer(&render))); hr != 0 || render == 0 {
		return fmt.Errorf("pcm: 获取渲染客户端失败 (0x%x)", hr)
	}
	w.render = render
	w.renderVtbl = (*wasapiRenderVtbl)(unsafe.Pointer(*(**uintptr)(unsafe.Pointer(render))))

	if hr, _, _ := syscall.SyscallN(w.clientVtbl.start, w.activate); hr != 0 {
		return fmt.Errorf("pcm: 启动音频流失败 (0x%x)", hr)
	}
	w.started = true
	go w.renderLoop()
	return nil
}

// renderLoop 被 WASAPI 事件驱动（共享模式约每 10ms 一次）：每次填满整个
// 设备缓冲。没数据时写静音——与 mpv 自己的输出行为一致。
func (w *wasapiSpeaker) renderLoop() {
	frameBytes := uintptr(pcmChannels * 4)
	buf := make([]float32, w.bufFrames*pcmChannels)
	for {
		select {
		case <-w.done:
			return
		default:
		}
		if hr, _, _ := procWaitForSingleObject.Call(uintptr(w.event), waitInfinite); hr == waitFailed {
			return
		}
		w.queueMu.Lock()
		n := copy(buf, w.queue)
		w.queue = w.queue[n:]
		w.queueMu.Unlock()
		if n < len(buf) {
			clear(buf[n:])
		}
		var data unsafe.Pointer
		// GetBuffer(frames=AUDCLNT_BUFFERFLAGS_SILENT? no—我们写自己的静音,
		// 直接拿全缓冲指针)
		if hr, _, _ := syscall.SyscallN(w.renderVtbl.getBuffer, w.render,
			uintptr(w.bufFrames), uintptr(unsafe.Pointer(&data))); hr != 0 {
			return
		}
		copy(unsafe.Slice((*float32)(data), w.bufFrames*pcmChannels), buf)
		syscall.SyscallN(w.renderVtbl.releaseBuffer, w.render, uintptr(w.bufFrames), 0)
		_ = frameBytes
	}
}

func (w *wasapiSpeaker) Start() error { return nil }

// Play 进队。WASAPI 消费速度固定 48kHz；队满时丢弃最旧数据以免无限增长
// （正常情况下泵侧被管道背压约束，永远不会队满）。
func (w *wasapiSpeaker) Play(samples []float32) error {
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return errors.New("pcm: 输出已关闭")
	}
	w.queueMu.Lock()
	w.queue = append(w.queue, samples...)
	const capFrames = pcmRate // 1s
	if max := capFrames * pcmChannels; len(w.queue) > max {
		w.queue = append([]float32(nil), w.queue[len(w.queue)-max:]...)
	}
	w.queueMu.Unlock()
	return nil
}

func (w *wasapiSpeaker) Close() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()
	w.doneOnce.Do(func() { close(w.done) })
	if w.started && w.activate != 0 {
		syscall.SyscallN(w.clientVtbl.stop, w.activate)
	}
	if w.render != 0 {
		syscall.SyscallN(w.renderVtbl.release, w.render)
		w.render = 0
	}
	if w.event != 0 {
		procCloseHandle.Call(uintptr(w.event))
		w.event = 0
	}
	if w.activate != 0 {
		syscall.SyscallN(w.clientVtbl.release, w.activate)
		w.activate = 0
	}
	if w.coInitDone {
		modOle32.NewProc("CoUninitialize").Call()
		w.coInitDone = false
	}
}

func uintptrAt(p unsafe.Pointer, idx int) uintptr {
	return *(*uintptr)(unsafe.Add(p, uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}
