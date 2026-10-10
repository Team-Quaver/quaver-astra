//go:build windows

// Windows SMTC（System Media Transport Controls）实现。
//
// 不引 cgo、不引第三方 WinRT 绑定：直接经 combase.dll 的 RoGetActivationFactory
// 拿到 ISystemMediaTransportControlsInterop，按 WinRT 接口的 vtable 布局裸调
// （槽位与 IID 取自 Windows SDK windows.media 元数据）。事件回调以纯 Go 实现
// IInspectable + ITypedEventHandler 的 COM 对象注入。
package smedia

import (
	"fmt"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/windows"
)

// ===== 接口 IID（windows.media / windows.storage / windows.foundation 元数据）=====

var (
	guidIUnknown = windows.GUID{
		Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	guidSMTC = windows.GUID{
		Data1: 0x99fa3ff4, Data2: 0x1742, Data3: 0x42a6,
		Data4: [8]byte{0x90, 0x2e, 0x08, 0x7d, 0x41, 0xf9, 0x65, 0xec},
	}
	guidSMTC2 = windows.GUID{
		Data1: 0xea98d2f6, Data2: 0x7f3c, Data3: 0x4af2,
		Data4: [8]byte{0xa5, 0x86, 0x72, 0x88, 0x98, 0x08, 0xef, 0xb1},
	}
	guidSMTCInterop = windows.GUID{
		Data1: 0x543c033b, Data2: 0x38ae, Data3: 0x42c1,
		Data4: [8]byte{0xa8, 0xba, 0x20, 0xc4, 0xd0, 0xac, 0xd1, 0x85},
	}
	guidMusicProps = windows.GUID{
		Data1: 0x6bbf0c59, Data2: 0xd0a0, Data3: 0x4d26,
		Data4: [8]byte{0x92, 0xa0, 0xf9, 0x78, 0xe1, 0xd1, 0x8e, 0x7b},
	}
	guidMusicProps2 = windows.GUID{
		Data1: 0x00368462, Data2: 0x97d3, Data3: 0x44b9,
		Data4: [8]byte{0xb0, 0x0f, 0x00, 0x8a, 0xfc, 0xef, 0xaf, 0x18},
	}
	guidTimelineProps = windows.GUID{
		Data1: 0x5125316a, Data2: 0xc3a2, Data3: 0x475b,
		Data4: [8]byte{0x85, 0x07, 0x93, 0x53, 0x4d, 0xc8, 0x8f, 0x15},
	}
	guidBtnPressedArgs = windows.GUID{
		Data1: 0xb7f47116, Data2: 0xa56f, Data3: 0x4dc8,
		Data4: [8]byte{0x9e, 0x11, 0x92, 0x03, 0x1f, 0x4a, 0x87, 0xc2},
	}
	guidShuffleArgs = windows.GUID{
		Data1: 0x49b593fe, Data2: 0x4fd0, Data3: 0x4666,
		Data4: [8]byte{0xa3, 0x14, 0xc0, 0xe0, 0x19, 0x40, 0xd3, 0x02},
	}
	guidAutoRepeatArgs = windows.GUID{
		Data1: 0xea137efa, Data2: 0xd852, Data3: 0x438e,
		Data4: [8]byte{0x88, 0x2b, 0xc9, 0x90, 0x10, 0x9a, 0x78, 0xf4},
	}
	guidPlayPosArgs = windows.GUID{
		Data1: 0xb4493f88, Data2: 0xeb28, Data3: 0x4961,
		Data4: [8]byte{0x9c, 0x14, 0x33, 0x5e, 0x44, 0xf3, 0xe1, 0x25},
	}
	guidUriFactory = windows.GUID{
		Data1: 0x44a9796f, Data2: 0x723e, Data3: 0x4fdf,
		Data4: [8]byte{0xa2, 0x18, 0x03, 0x3e, 0x75, 0xb0, 0xc0, 0x84},
	}
	guidStreamRefStatics = windows.GUID{
		Data1: 0x857309dc, Data2: 0x3fbf, Data3: 0x4e7d,
		Data4: [8]byte{0x98, 0x6f, 0xef, 0x3b, 0x1a, 0x07, 0xa9, 0x64},
	}
)

// ===== combase.dll =====

var (
	procRoInitialize       = windows.NewLazySystemDLL("combase.dll").NewProc("RoInitialize")
	procRoGetActivationFac = windows.NewLazySystemDLL("combase.dll").NewProc("RoGetActivationFactory")
	procRoActivateInstance = windows.NewLazySystemDLL("combase.dll").NewProc("RoActivateInstance")
	procCreateStringRef    = windows.NewLazySystemDLL("combase.dll").NewProc("WindowsCreateStringReference")
)

const (
	hresultOK          = 0
	hresultNoInterface = 0x80004004
	roInitMTA          = 1
)

// MediaPlaybackStatus / MediaPlaybackType / AutoRepeatMode 枚举值。
const (
	playbackStopped = 3
	playbackPlaying = 4
	playbackPaused  = 5

	mediaTypeMusic = 1

	repeatNone  = 0
	repeatTrack = 1
	repeatList  = 2
)

// SystemMediaTransportControlsButton 枚举值。
const (
	btnPlay     = 0
	btnPause    = 1
	btnStop     = 2
	btnPrevious = 3
	btnNext     = 4
)

// vtable 槽位（IInspectable 占 0..5，方法自 6 起，顺序即元数据声明顺序）。
const (
	smtcPutPlaybackStatus = 7
	smtcGetDisplayUpdater = 8
	smtcPutIsPlayEnabled  = 13
	smtcPutIsStopEnabled  = 15
	smtcPutIsPauseEnabled = 17
	smtcPutIsNextEnabled  = 27
	smtcPutIsPrevEnabled  = 25
	smtcAddButtonPressed  = 32
	smtcRemoveBtnPressed  = 33

	smtc2PutAutoRepeat  = 7
	smtc2PutShuffle     = 9
	smtc2UpdateTimeline = 12
	smtc2AddPlayPosReq  = 13
	smtc2RmPlayPosReq   = 14
	smtc2AddShuffleReq  = 17
	smtc2RmShuffleReq   = 18
	smtc2AddRepeatReq   = 19
	smtc2RmRepeatReq    = 20

	updPutType  = 7
	updPutThumb = 11
	updGetMusic = 12
	updClearAll = 16
	updUpdate   = 17

	musicPutTitle       = 7
	musicPutAlbumArtist = 9
	musicPutArtist      = 11

	music2PutAlbumTitle = 7

	tlpPutStart = 7
	tlpPutEnd   = 9
	tlpPutMin   = 11
	tlpPutMax   = 13
	tlpPutPos   = 15

	argsGetButton  = 6
	argsGetShuffle = 6
	argsGetRepeat  = 6
	argsGetPlayPos = 6

	uriCreateUri = 6

	srsCreateFromUri = 8
)

// New 初始化 WinRT（MTA）并返回 SMTC 控制器；combase 不可用时退化为 no-op。
func New(identity string, cmds *Commands) Controller {
	if err := procRoInitialize.Find(); err != nil {
		return noop{}
	}
	// MTA 初始化；已初始化为其他模式（RPC_E_CHANGED_MODE）不致命。
	procRoInitialize.Call(roInitMTA)
	return &smtc{identity: identity, cmds: cmds}
}

type smtc struct {
	identity string
	cmds     *Commands

	mu       sync.Mutex
	snap     Snapshot
	attached bool
	closed   bool

	smtc   uintptr // ISystemMediaTransportControls*
	smtc2  uintptr
	disp   uintptr // DisplayUpdater*
	music  uintptr // IMusicDisplayProperties*
	music2 uintptr
	tlp    uintptr // SystemMediaTransportControlsTimelineProperties 实例

	uriFactory uintptr
	srsStatics uintptr
	hwnd       uintptr

	lastSig string
	lastArt string
	lastPos float64
	lastDur float64

	btnToken  int64
	posToken  int64
	shufToken int64
	repToken  int64
	handlers  []*smtcHandler
}

// ===== COM/WinRT 基础设施 =====

func vtblFn(obj uintptr, slot int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
}

// winrtCall 调用 COM 对象 vtable 第 slot 槽，返回 HRESULT。
func winrtCall(obj uintptr, slot int, args ...uintptr) uintptr {
	// Windows 上 SyscallN 返回 (r1, r2, err) 三值。
	r1, _, _ := purego.SyscallN(vtblFn(obj, slot), append([]uintptr{obj}, args...)...)
	return r1
}

// hstrOf 把 Go 字符串包装成零分配的 HSTRING 引用（WindowsCreateStringReference）。
// 返回的 hstr 只在同步 WinRT 调用期间有效；defer keep() 保活底层缓冲。
func hstrOf(s string) (uintptr, func()) {
	u16 := utf16.Encode([]rune(s))
	u16 = append(u16, 0)
	var hdr [3]uint64 // HSTRING_HEADER（x64 24 字节）
	var out uintptr
	hr, _, _ := procCreateStringRef.Call(
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(len(u16)-1),
		uintptr(unsafe.Pointer(&hdr[0])),
		uintptr(unsafe.Pointer(&out)),
	)
	if hr != hresultOK {
		return 0, func() {}
	}
	return out, func() { _ = hdr; _ = u16 } // 闭包引用保活，防 GC 提前回收
}

// getActivationFactory 等价 RoGetActivationFactory(class, riid)。
func getActivationFactory(class string, iid *windows.GUID) (uintptr, error) {
	h, keep := hstrOf(class)
	defer keep()
	if h == 0 {
		return 0, fmt.Errorf("WindowsCreateStringReference failed")
	}
	var out uintptr
	hr, _, _ := procRoGetActivationFac.Call(h, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(h)
	if hr != hresultOK {
		return 0, fmt.Errorf("RoGetActivationFactory(%s): 0x%08x", class, hr)
	}
	return out, nil
}

func roActivateInstance(class string, iid *windows.GUID) (uintptr, error) {
	h, keep := hstrOf(class)
	defer keep()
	if h == 0 {
		return 0, fmt.Errorf("WindowsCreateStringReference failed")
	}
	var unknown uintptr
	hr, _, _ := procRoActivateInstance.Call(h, uintptr(unsafe.Pointer(&unknown)))
	runtime.KeepAlive(h)
	if hr != hresultOK {
		return 0, fmt.Errorf("RoActivateInstance(%s): 0x%08x", class, hr)
	}
	// 拿到 IUnknown 后再 QI 到目标接口。
	defer winrtCall(unknown, 2) // Release
	var out uintptr
	hr = winrtCall(unknown, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if hr != hresultOK {
		return 0, fmt.Errorf("QI(%s): 0x%08x", class, hr)
	}
	return out, nil
}

// qi 通过 QueryInterface 获取对象的其他接口；成功返回引用计数 +1 的指针。
func qi(obj uintptr, iid *windows.GUID) (uintptr, bool) {
	var out uintptr
	hr := winrtCall(obj, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, hr == hresultOK
}

func rel(obj uintptr) {
	if obj != 0 {
		winrtCall(obj, 2)
	}
}

// ===== 事件处理对象 =====

// smtcHandler 是注入 SMTC 的 COM 回调对象：
// IUnknown(0..2) + IInspectable(3..5) + Invoke(6)。
type smtcHandler struct {
	vtbl [7]uintptr
	iid  windows.GUID
	onEv func(args uintptr)
}

func newHandler(iid windows.GUID, onEv func(args uintptr)) *smtcHandler {
	h := &smtcHandler{iid: iid, onEv: onEv}
	h.vtbl[0] = purego.NewCallback(h.queryInterface)
	h.vtbl[1] = purego.NewCallback(h.addRef)
	h.vtbl[2] = purego.NewCallback(h.release)
	h.vtbl[3] = purego.NewCallback(h.getIids)
	h.vtbl[4] = purego.NewCallback(h.getRuntimeClassName)
	h.vtbl[5] = purego.NewCallback(h.getTrustLevel)
	h.vtbl[6] = purego.NewCallback(h.invoke)
	return h
}

func (h *smtcHandler) queryInterface(this, riid, out uintptr) uintptr {
	if riid == 0 || out == 0 {
		return 0x80070057 // E_POINTER
	}
	rid := (*windows.GUID)(unsafe.Pointer(riid))
	if *rid == h.iid || *rid == guidIUnknown {
		*(*uintptr)(unsafe.Pointer(out)) = this
		return hresultOK
	}
	return hresultNoInterface
}

func (h *smtcHandler) addRef(this uintptr) uintptr              { return 1 }
func (h *smtcHandler) release(this uintptr) uintptr             { return 1 }
func (h *smtcHandler) getIids(a, b, c uintptr) uintptr          { return hresultOK }
func (h *smtcHandler) getRuntimeClassName(a, b uintptr) uintptr { return hresultOK }
func (h *smtcHandler) getTrustLevel(a, b uintptr) uintptr       { return hresultOK }

func (h *smtcHandler) invoke(sender, args uintptr) uintptr {
	if h.onEv != nil {
		h.onEv(args)
	}
	return hresultOK
}

// ===== 控制器 =====

func (c *smtc) AttachWindow(hwnd uintptr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attached || hwnd == 0 {
		return
	}
	factory, err := getActivationFactory("Windows.Media.SystemMediaTransportControls", &guidSMTCInterop)
	if err != nil {
		return
	}
	defer rel(factory)
	var smtc uintptr
	hr := winrtCall(factory, 6, hwnd, uintptr(unsafe.Pointer(&guidSMTC)), uintptr(unsafe.Pointer(&smtc)))
	if hr != hresultOK || smtc == 0 {
		return
	}
	c.hwnd = hwnd
	c.smtc = smtc
	c.smtc2, _ = qi(smtc, &guidSMTC2)
	if disp := winrtPropGet(smtc, smtcGetDisplayUpdater); disp != 0 {
		c.disp = disp
		c.music = winrtPropGet(disp, updGetMusic)
		if c.music != 0 {
			c.music2, _ = qi(c.music, &guidMusicProps2)
		}
	}
	c.tlp, _ = roActivateInstance("Windows.Media.SystemMediaTransportControlsTimelineProperties", &guidTimelineProps)
	c.uriFactory, _ = getActivationFactory("Windows.Foundation.Uri", &guidUriFactory)
	c.srsStatics, _ = getActivationFactory("Windows.Storage.Streams.RandomAccessStreamReference", &guidStreamRefStatics)

	// 按钮事件。
	btn := newHandler(guidBtnPressedArgs, c.onButtonPressed)
	if hr := winrtCall(c.smtc, smtcAddButtonPressed, uintptr(unsafe.Pointer(btn.vp())), uintptr(unsafe.Pointer(&c.btnToken))); hr == hresultOK {
		c.handlers = append(c.handlers, btn)
	}
	if c.smtc2 != 0 {
		pos := newHandler(guidPlayPosArgs, c.onPlaybackPositionRequested)
		if hr := winrtCall(c.smtc2, smtc2AddPlayPosReq, uintptr(unsafe.Pointer(pos.vp())), uintptr(unsafe.Pointer(&c.posToken))); hr == hresultOK {
			c.handlers = append(c.handlers, pos)
		}
		shuf := newHandler(guidShuffleArgs, c.onShuffleRequested)
		if hr := winrtCall(c.smtc2, smtc2AddShuffleReq, uintptr(unsafe.Pointer(shuf.vp())), uintptr(unsafe.Pointer(&c.shufToken))); hr == hresultOK {
			c.handlers = append(c.handlers, shuf)
		}
		rep := newHandler(guidAutoRepeatArgs, c.onAutoRepeatRequested)
		if hr := winrtCall(c.smtc2, smtc2AddRepeatReq, uintptr(unsafe.Pointer(rep.vp())), uintptr(unsafe.Pointer(&c.repToken))); hr == hresultOK {
			c.handlers = append(c.handlers, rep)
		}
	}
	c.attached = true
	c.applyAll()
}

// vp 返回指向 vtbl 的对象指针：COM 对象布局 = 指向 vtbl 的指针在首字段。
func (h *smtcHandler) vp() *uintptr { return &h.vtbl[0] }

// winrtPropGet 调用只读属性 getter（out 指针，返回引用计数 +1 的对象）。
func winrtPropGet(obj uintptr, slot int) uintptr {
	if obj == 0 {
		return 0
	}
	var out uintptr
	hr := winrtCall(obj, slot, uintptr(unsafe.Pointer(&out)))
	if hr != hresultOK {
		return 0
	}
	return out
}

func (c *smtc) Update(s Snapshot) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.snap = s
	if !c.attached {
		c.mu.Unlock()
		return
	}
	sig := stateSig(s)
	metaChanged := sig != c.lastSig
	artChanged := s.ArtURL != c.lastArt
	tlNeeded := s.Playing || s.Position != c.lastPos || s.Duration != c.lastDur
	c.lastSig = sig
	c.mu.Unlock()

	// COM 调用一律在锁外做（WinRT 调用可能阻塞），快照按值传入避免竞态。
	if metaChanged {
		c.applyTransport(s)
		c.applyMetadata(s)
	}
	if artChanged {
		c.applyThumbnail(s.ArtURL)
	}
	if tlNeeded {
		c.applyTimeline(s)
	}
}

// stateSig 提取影响 SMTC 元数据/传输状态的字段做 diff。
func stateSig(s Snapshot) string {
	return fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%t\x00%s\x00%s\x00%s\x00%t",
		s.TrackID, s.HasTrack, s.Playing, s.Loop, s.Shuffle,
		s.Title, s.Artist, s.Album, s.Duration > 0)
}

// smtcObjs 是 COM 对象指针的一致性快照（Close 可并发清零，避免用到悬空句柄）。
type smtcObjs struct {
	smtc, smtc2, disp, music, music2, tlp, uriFactory, srsStatics uintptr
}

func (c *smtc) objs() smtcObjs {
	c.mu.Lock()
	defer c.mu.Unlock()
	return smtcObjs{
		c.smtc, c.smtc2, c.disp, c.music, c.music2, c.tlp, c.uriFactory, c.srsStatics,
	}
}

// setTlState 在锁内更新时间轴去重状态。
func (c *smtc) setTlState(pos, dur float64) {
	c.mu.Lock()
	c.lastPos, c.lastDur = pos, dur
	c.mu.Unlock()
}

// setArt 在锁内记录已应用的封面地址。
func (c *smtc) setArt(artURL string) {
	c.mu.Lock()
	c.lastArt = artURL
	c.mu.Unlock()
}

// applyAll 在绑定时把缓冲的快照全部铺到 SMTC 上。
func (c *smtc) applyAll() {
	c.mu.Lock()
	s := c.snap
	c.mu.Unlock()
	c.applyTransport(s)
	c.applyMetadata(s)
	c.applyThumbnail(s.ArtURL)
	c.applyTimeline(s)
}

func (c *smtc) applyTransport(s Snapshot) {
	o := c.objs()
	if o.smtc == 0 {
		return
	}
	status := uintptr(playbackStopped)
	if s.HasTrack {
		if s.Playing {
			status = playbackPlaying
		} else {
			status = playbackPaused
		}
	}
	winrtCall(o.smtc, smtcPutPlaybackStatus, status)
	winrtCall(o.smtc, smtcPutIsPlayEnabled, boolW(s.HasTrack))
	winrtCall(o.smtc, smtcPutIsPauseEnabled, boolW(s.HasTrack))
	winrtCall(o.smtc, smtcPutIsStopEnabled, boolW(s.HasTrack))
	winrtCall(o.smtc, smtcPutIsNextEnabled, boolW(s.HasTrack))
	winrtCall(o.smtc, smtcPutIsPrevEnabled, boolW(s.HasTrack))
	if o.smtc2 != 0 {
		rep := uintptr(repeatNone)
		switch s.Loop {
		case "one":
			rep = repeatTrack
		case "all":
			rep = repeatList
		}
		winrtCall(o.smtc2, smtc2PutAutoRepeat, rep)
		winrtCall(o.smtc2, smtc2PutShuffle, boolW(s.Shuffle))
	}
}

func boolW(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func (c *smtc) applyMetadata(s Snapshot) {
	o := c.objs()
	if o.disp == 0 || o.music == 0 {
		return
	}
	winrtCall(o.disp, updPutType, mediaTypeMusic)
	if h, keep := hstrOf(s.Title); h != 0 {
		winrtCall(o.music, musicPutTitle, h)
		keep()
	}
	if h, keep := hstrOf(s.Artist); h != 0 {
		winrtCall(o.music, musicPutArtist, h)
		keep()
	}
	if o.music2 != 0 {
		if h, keep := hstrOf(s.Album); h != 0 {
			winrtCall(o.music2, music2PutAlbumTitle, h)
			keep()
		}
	} else {
		if h, keep := hstrOf(s.Album); h != 0 {
			winrtCall(o.music, musicPutAlbumArtist, h)
			keep()
		}
	}
	// DisplayUpdater.Update() 把以上属性提交给系统 UI。
	winrtCall(o.disp, updUpdate)
}

func (c *smtc) applyThumbnail(artURL string) {
	c.setArt(artURL)
	if artURL == "" {
		return
	}
	o := c.objs()
	if o.disp == 0 || o.uriFactory == 0 || o.srsStatics == 0 {
		return
	}
	uh, keep := hstrOf(artURL)
	if uh == 0 {
		return
	}
	var uri uintptr
	hr := winrtCall(o.uriFactory, uriCreateUri, uh, uintptr(unsafe.Pointer(&uri)))
	keep()
	if hr != hresultOK || uri == 0 {
		return
	}
	defer rel(uri)
	var thumb uintptr
	hr = winrtCall(o.srsStatics, srsCreateFromUri, uri, uintptr(unsafe.Pointer(&thumb)))
	if hr != hresultOK || thumb == 0 {
		return
	}
	winrtCall(o.disp, updPutThumb, thumb)
	rel(thumb) // put_Thumbnail 已 AddRef，释放本地引用
	winrtCall(o.disp, updUpdate)
}

func (c *smtc) applyTimeline(s Snapshot) {
	o := c.objs()
	if o.tlp == 0 || o.smtc2 == 0 {
		return
	}
	if !s.HasTrack || s.Duration <= 0 {
		return
	}
	pos := timeSpanTicks(s.Position)
	dur := timeSpanTicks(s.Duration)
	zero := int64(0)
	winrtCall(o.tlp, tlpPutStart, uintptr(unsafe.Pointer(&zero)))
	winrtCall(o.tlp, tlpPutEnd, uintptr(unsafe.Pointer(&dur)))
	winrtCall(o.tlp, tlpPutMin, uintptr(unsafe.Pointer(&zero)))
	winrtCall(o.tlp, tlpPutMax, uintptr(unsafe.Pointer(&dur)))
	winrtCall(o.tlp, tlpPutPos, uintptr(unsafe.Pointer(&pos)))
	winrtCall(o.smtc2, smtc2UpdateTimeline, o.tlp)
	c.setTlState(s.Position, s.Duration)
}

// timeSpanTicks 秒 → WinRT TimeSpan（100ns tick）。
func timeSpanTicks(sec float64) int64 {
	return int64(sec * 1e7)
}

// ===== 事件回调 =====

func (c *smtc) call(f func()) {
	if f != nil {
		f()
	}
}

func (c *smtc) onButtonPressed(args uintptr) {
	if args == 0 {
		return
	}
	var btn int32
	winrtCall(args, argsGetButton, uintptr(unsafe.Pointer(&btn)))
	c.mu.Lock()
	cmds := c.cmds
	c.mu.Unlock()
	switch btn {
	case btnPlay:
		c.call(cmds.Play)
	case btnPause:
		c.call(cmds.Pause)
	case btnStop:
		c.call(cmds.Stop)
	case btnPrevious:
		c.call(cmds.Prev)
	case btnNext:
		c.call(cmds.Next)
	}
}

func (c *smtc) onPlaybackPositionRequested(args uintptr) {
	if args == 0 {
		return
	}
	var ticks int64
	winrtCall(args, argsGetPlayPos, uintptr(unsafe.Pointer(&ticks)))
	c.mu.Lock()
	cmds, trackID := c.cmds, c.snap.TrackID
	c.mu.Unlock()
	if cmds != nil && cmds.SetPosition != nil {
		cmds.SetPosition(float64(ticks)/1e7, trackID)
	}
}

func (c *smtc) onShuffleRequested(args uintptr) {
	if args == 0 {
		return
	}
	var on uint8
	winrtCall(args, argsGetShuffle, uintptr(unsafe.Pointer(&on)))
	c.mu.Lock()
	cmds := c.cmds
	c.mu.Unlock()
	if cmds != nil && cmds.SetShuffle != nil {
		cmds.SetShuffle(on != 0)
	}
}

func (c *smtc) onAutoRepeatRequested(args uintptr) {
	if args == 0 {
		return
	}
	var mode int32
	winrtCall(args, argsGetRepeat, uintptr(unsafe.Pointer(&mode)))
	c.mu.Lock()
	cmds := c.cmds
	c.mu.Unlock()
	if cmds == nil || cmds.SetLoop == nil {
		return
	}
	switch mode {
	case repeatTrack:
		cmds.SetLoop("one")
	case repeatList:
		cmds.SetLoop("all")
	default:
		cmds.SetLoop("off")
	}
}

func (c *smtc) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.attached = false
	smtc, smtc2 := c.smtc, c.smtc2
	tokens := []struct {
		slot  int
		token int64
		obj   uintptr
	}{}
	if smtc != 0 && c.btnToken != 0 {
		tokens = append(tokens, struct {
			slot  int
			token int64
			obj   uintptr
		}{smtcRemoveBtnPressed, c.btnToken, smtc})
	}
	if smtc2 != 0 {
		if c.posToken != 0 {
			tokens = append(tokens, struct {
				slot  int
				token int64
				obj   uintptr
			}{smtc2RmPlayPosReq, c.posToken, smtc2})
		}
		if c.shufToken != 0 {
			tokens = append(tokens, struct {
				slot  int
				token int64
				obj   uintptr
			}{smtc2RmShuffleReq, c.shufToken, smtc2})
		}
		if c.repToken != 0 {
			tokens = append(tokens, struct {
				slot  int
				token int64
				obj   uintptr
			}{smtc2RmRepeatReq, c.repToken, smtc2})
		}
	}
	disp, music, music2, tlp, uriF, srs := c.disp, c.music, c.music2, c.tlp, c.uriFactory, c.srsStatics
	c.disp, c.music, c.music2, c.tlp, c.uriFactory, c.srsStatics = 0, 0, 0, 0, 0, 0
	c.smtc, c.smtc2 = 0, 0
	c.handlers = nil
	c.mu.Unlock()

	for _, t := range tokens {
		winrtCall(t.obj, t.slot, uintptr(t.token))
	}
	rel(disp)
	rel(music)
	rel(music2)
	rel(tlp)
	rel(uriF)
	rel(srs)
	rel(smtc)
	rel(smtc2)
}
