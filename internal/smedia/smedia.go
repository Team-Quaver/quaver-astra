// Package smedia 把播放器状态投影到系统媒体控制中心：
// Linux 走 MPRIS（D-Bus），Windows 走 SMTC（WinRT SystemMediaTransportControls）。
// 平台实现经构建标签拆分，其余平台退化为 no-op。
package smedia

// Snapshot 是播放器当前状态的一次投影，由宿主（appui）在状态变化时推送。
type Snapshot struct {
	HasTrack bool
	// TrackID 是当前曲目在应用内的稳定标识（QQ 音乐 media mid），
	// MPRIS 用它构造 mpris:trackid，SMTC 用它过滤过期的 SetPosition。
	TrackID  string
	Title    string
	Artist   string
	Album    string
	ArtURL   string // 封面 http(s) 地址，可为空
	Playing  bool
	Position float64 // 秒
	Duration float64 // 秒
	Volume   float64 // 0..1，静音时为 0
	Loop     string  // off | all | one
	Shuffle  bool
}

// Commands 是系统媒体控制中心回传的播放命令。全部由实现方在任意
// goroutine 调用（player 本身并发安全），可为 nil 表示不支持。
type Commands struct {
	PlayPause func()
	Play      func()
	Pause     func()
	Stop      func()
	Next      func()
	Prev      func()
	// Seek 相对跳转，deltaSec 可正可负。
	Seek func(deltaSec float64)
	// SetPosition 绝对跳转；trackID 非空时实现方应校验是否仍是当前曲目，
	// 过期请求（系统 UI 的陈旧进度条）直接丢弃。
	SetPosition func(posSec float64, trackID string)
	SetVolume   func(v float64) // 0..1
	SetLoop     func(mode string)
	SetShuffle  func(on bool)
	Raise       func()
	Quit        func()
}

// Controller 是单个系统媒体控制中心的会话。
type Controller interface {
	// AttachWindow 绑定宿主窗口（Windows SMTC 需要 HWND；MPRIS 忽略）。
	// 必须在窗口创建后调用；之前推送的 Update 会在绑定时补齐。
	AttachWindow(hwnd uintptr)
	// Update 推送最新状态。实现内部做 diff，未变化时不打扰系统。
	Update(s Snapshot)
	// Close 注销服务并释放资源。幂等。
	Close()
}

// noop 是平台不支持时的空实现。
type noop struct{}

func (noop) AttachWindow(uintptr) {}
func (noop) Update(Snapshot)      {}
func (noop) Close()               {}
