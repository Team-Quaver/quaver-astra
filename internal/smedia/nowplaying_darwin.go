//go:build darwin

// macOS Now Playing：MediaPlayer.framework 的 MPNowPlayingInfoCenter +
// MPRemoteCommandCenter。系统的控制中心 / 菜单栏「正在播放」据此显示
// 标题、歌手、专辑、封面与进度，并把播放、暂停、上一首、下一首、
// 拖动进度等命令回传给播放器。
//
// 与 MPRIS（Linux）/ SMTC（Windows）对齐：AttachWindow 时建会话，
// Update 推送快照（内部 diff），Close 注销。系统只对带
// CFBundleIdentifier 的应用投影 Now Playing，裸 ./quaver-astra 可能
// 在控制中心看不到，打包成 .app 后正常（见 scripts/package-macos.sh）。
package smedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ebitengine/purego/objc"
)

// MPNowPlayingPlaybackState（NSInteger）。
const (
	npStatePlaying = 1
	npStatePaused  = 2
	npStateStopped = 3
)

// MPRemoteCommandHandlerStatus.MPRemoteCommandHandlerStatusSuccess。
const npCommandSuccess = 0

const (
	maxArtBytes    = 4 << 20
	artHTTPTimeout = 20 * time.Second
)

// cgSize 对应 CoreGraphics 的 CGSize（两个 CGFloat = 64 位 double）。
type cgSize struct {
	Width  float64
	Height float64
}

// remoteCommand 记录已注册的远程命令，Close 时逐个移除。
type remoteCommand struct {
	command objc.ID
	token   objc.ID
	block   objc.Block
}

type nowPlaying struct {
	cmds *Commands

	mu       sync.Mutex
	snap     Snapshot
	attached bool
	closed   bool
	lastSig  string

	center objc.ID // MPNowPlayingInfoCenter
	remote objc.ID // MPRemoteCommandCenter

	commands []remoteCommand

	// 封面：Now Playing 无法像 SMTC 那样把 URL 交给系统自行下载，
	// 这里在后台拉字节、建成 owned NSImage 缓存，再回填到 info dict。
	hc       *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	art      map[string]objc.ID // url -> owned NSImage
	artworks map[string]objc.ID // url -> owned MPMediaItemArtwork
	artBlock map[string]objc.Block
	artBusy  map[string]bool
}

// New 建立 Now Playing 会话；真正的系统对象在 AttachWindow（主线程）时创建。
func New(identity string, cmds *Commands) Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &nowPlaying{
		cmds:     cmds,
		hc:       &http.Client{Timeout: artHTTPTimeout},
		ctx:      ctx,
		cancel:   cancel,
		art:      map[string]objc.ID{},
		artworks: map[string]objc.ID{},
		artBlock: map[string]objc.Block{},
		artBusy:  map[string]bool{},
	}
}

func (np *nowPlaying) AttachWindow(uintptr) {
	if err := loadObjC(); err != nil {
		return
	}
	np.mu.Lock()
	defer np.mu.Unlock()
	if np.attached || np.closed {
		return
	}
	// Attach 由 WhenReady 回调触发，处于主线程；ObjC 对象在此创建最稳。
	withPool(func() {
		np.center = class("MPNowPlayingInfoCenter").Send(sel("defaultCenter"))
		np.remote = class("MPRemoteCommandCenter").Send(sel("sharedCommandCenter"))
		if np.center == 0 || np.remote == 0 {
			return
		}
		np.installCommands()
		np.attached = true
		np.applyLocked()
		np.lastSig = np.snap.stateSignature()
	})
}

// installCommands 注册远程命令。回调可能来自任意线程，只碰并发安全的
// player（经 Commands），绝不持 np.mu 调用，避免与状态回推互锁。
func (np *nowPlaying) installCommands() {
	var (
		play, pause, toggle, stop, next, prev func()
		setPos                                func(float64, string)
		seek                                  func(float64)
	)
	if c := np.cmds; c != nil {
		play, pause, toggle, stop, next, prev = c.Play, c.Pause, c.PlayPause, c.Stop, c.Next, c.Prev
		setPos, seek = c.SetPosition, c.Seek
	}

	simple := func(name string, fn func()) {
		if fn == nil {
			return // 没有处理器就不注册，系统不会显示对应按钮
		}
		cmd := np.remote.Send(sel(name))
		if cmd == 0 {
			return
		}
		cmd.Send(sel("setEnabled:"), true)
		block := objc.NewBlock(func(_ objc.Block, _ objc.ID) int {
			fn()
			return npCommandSuccess
		})
		np.track(cmd, block)
	}

	simple("playCommand", play)
	simple("pauseCommand", pause)
	simple("togglePlayPauseCommand", toggle)
	simple("stopCommand", stop)
	simple("nextTrackCommand", next)
	simple("previousTrackCommand", prev)

	if setPos != nil {
		if cmd := np.remote.Send(sel("changePlaybackPositionCommand")); cmd != 0 {
			cmd.Send(sel("setEnabled:"), true)
			block := objc.NewBlock(func(_ objc.Block, event objc.ID) int {
				pos := objc.Send[float64](event, sel("positionTime"))
				np.mu.Lock()
				track := np.snap.TrackID
				np.mu.Unlock()
				setPos(pos, track)
				return npCommandSuccess
			})
			np.track(cmd, block)
		}
	}

	if seek != nil {
		skip := func(name string, sign float64) {
			cmd := np.remote.Send(sel(name))
			if cmd == 0 {
				return
			}
			cmd.Send(sel("setEnabled:"), true)
			block := objc.NewBlock(func(_ objc.Block, event objc.ID) int {
				delta := objc.Send[float64](event, sel("interval"))
				if delta <= 0 {
					delta = 15
				}
				seek(sign * delta)
				return npCommandSuccess
			})
			np.track(cmd, block)
		}
		skip("skipForwardCommand", 1)
		skip("skipBackwardCommand", -1)
	}
}

// track 在锁内登记命令与处理器块（installCommands 由 AttachWindow 持锁调用）。
func (np *nowPlaying) track(cmd objc.ID, block objc.Block) {
	token := cmd.Send(sel("addTargetWithHandler:"), block)
	np.commands = append(np.commands, remoteCommand{command: cmd, token: token, block: block})
}

func (np *nowPlaying) Update(s Snapshot) {
	np.mu.Lock()
	defer np.mu.Unlock()
	if np.closed {
		return
	}
	np.snap = s
	if !np.attached {
		return
	}
	// 元数据/播放态/进度（整秒量化）任一变化才回写，避免每秒重复建 dict。
	sig := s.stateSignature()
	if sig == np.lastSig {
		return
	}
	np.lastSig = sig
	np.applyAsyncLocked()
}

// applyAsyncLocked 回写状态：非主线程时投递到主队列，避免在播放器的
// 观察协程上直接触碰 MediaPlayer（同步回写是兜底，框架容错但主线程更稳）。
// 调用方持 np.mu；投递的块在主线程重新取锁。
func (np *nowPlaying) applyAsyncLocked() {
	if !onMainThread() && runOnMainAsync(func() {
		np.mu.Lock()
		defer np.mu.Unlock()
		if np.closed || !np.attached {
			return
		}
		withPool(func() { np.applyLocked() })
	}) {
		return
	}
	withPool(func() { np.applyLocked() })
}

// applyLocked 把当前快照投影到 MPNowPlayingInfoCenter。调用方持 np.mu 或
// 处于构建期；需在 withPool 内执行。
func (np *nowPlaying) applyLocked() {
	s := np.snap
	if !s.HasTrack {
		// 无曲目：清空并置 Stopped，系统会收起正在播放卡片。
		np.center.Send(sel("setNowPlayingInfo:"), objc.ID(0))
		np.center.Send(sel("setPlaybackState:"), npStateStopped)
		return
	}
	info := nsMutableDict(8)
	dictSet(info, frameworkString(mediaPlayerHandle, "MPMediaItemPropertyTitle"), nsString(s.Title))
	dictSet(info, frameworkString(mediaPlayerHandle, "MPMediaItemPropertyArtist"), nsString(s.Artist))
	dictSet(info, frameworkString(mediaPlayerHandle, "MPMediaItemPropertyAlbumTitle"), nsString(s.Album))
	if s.Duration > 0 {
		dictSet(info, frameworkString(mediaPlayerHandle, "MPMediaItemPropertyPlaybackDuration"), nsNumberDouble(s.Duration))
	}
	if s.TrackID != "" {
		dictSet(info, frameworkString(mediaPlayerHandle, "MPNowPlayingInfoPropertyExternalContentIdentifier"), nsString(s.TrackID))
	}
	dictSet(info, frameworkString(mediaPlayerHandle, "MPNowPlayingInfoPropertyElapsedPlaybackTime"), nsNumberDouble(s.Position))
	rate := 0.0
	if s.Playing {
		rate = 1
	}
	dictSet(info, frameworkString(mediaPlayerHandle, "MPNowPlayingInfoPropertyPlaybackRate"), nsNumberDouble(rate))
	dictSet(info, frameworkString(mediaPlayerHandle, "MPNowPlayingInfoPropertyDefaultPlaybackRate"), nsNumberDouble(1))
	// MPNowPlayingInfoMediaTypeAudio = 1，声明这是音频流。
	dictSet(info, frameworkString(mediaPlayerHandle, "MPNowPlayingInfoPropertyMediaType"), nsNumberDouble(1))

	np.scheduleArtLocked(s.ArtURL)
	if art := np.artworkLocked(s.ArtURL); art != 0 {
		dictSet(info, frameworkString(mediaPlayerHandle, "MPMediaItemPropertyArtwork"), art)
	}

	np.center.Send(sel("setNowPlayingInfo:"), info)
	np.center.Send(sel("setPlaybackState:"), np.playbackState())
}

func (np *nowPlaying) playbackState() int {
	if !np.snap.HasTrack {
		return npStateStopped
	}
	if np.snap.Playing {
		return npStatePlaying
	}
	return npStatePaused
}

// scheduleArtLocked 对已缓存或下载中的封面直接返回，否则后台拉取。
func (np *nowPlaying) scheduleArtLocked(url string) {
	if url == "" || np.art[url] != 0 || np.artBusy[url] {
		return
	}
	np.artBusy[url] = true
	go np.fetchArt(url)
}

// fetchArt 下载封面字节并建成 owned NSImage；成功后若仍是当前曲目就回写。
func (np *nowPlaying) fetchArt(url string) {
	data, err := np.download(url)
	np.mu.Lock()
	defer np.mu.Unlock()
	delete(np.artBusy, url)
	if err != nil || np.closed {
		return
	}
	var img objc.ID
	withPool(func() { img = nsImageFromData(data) })
	if img == 0 {
		return
	}
	np.art[url] = img
	if np.snap.ArtURL == url {
		np.lastSig = "" // 封面就绪，强制回写一次
		np.applyAsyncLocked()
	}
}

func (np *nowPlaying) download(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(np.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := np.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("artwork: " + resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArtBytes {
		return nil, errors.New("artwork too large")
	}
	return data, nil
}

// artworkLocked 返回该 URL 的 MPMediaItemArtwork（惰性构建并缓存）。
func (np *nowPlaying) artworkLocked(url string) objc.ID {
	img := np.art[url]
	if img == 0 {
		return 0
	}
	if art := np.artworks[url]; art != 0 {
		return art
	}
	// 请求处理器忽略系统传入的尺寸，固定回原始封面；AppKit 会自行缩放。
	block := objc.NewBlock(func(_ objc.Block, _ cgSize) objc.ID { return img })
	art := class("MPMediaItemArtwork").Send(sel("alloc"))
	art = art.Send(sel("initWithBoundsSize:requestHandler:"), cgSize{Width: 500, Height: 500}, block)
	if art == 0 {
		block.Release()
		return 0
	}
	np.artworks[url] = art
	np.artBlock[url] = block
	return art
}

func (np *nowPlaying) Close() {
	np.mu.Lock()
	defer np.mu.Unlock()
	if np.closed {
		return
	}
	np.closed = true
	if np.cancel != nil {
		np.cancel()
	}
	if np.remote != 0 {
		withPool(func() {
			for _, rc := range np.commands {
				if rc.token != 0 {
					rc.command.Send(sel("removeTarget:"), rc.token)
				}
			}
		})
	}
	for _, rc := range np.commands {
		if rc.block != 0 {
			rc.block.Release()
		}
	}
	for _, art := range np.artworks {
		objcRelease(art)
	}
	for _, img := range np.art {
		objcRelease(img)
	}
	for _, b := range np.artBlock {
		if b != 0 {
			b.Release()
		}
	}
	if np.center != 0 {
		withPool(func() { np.center.Send(sel("setNowPlayingInfo:"), objc.ID(0)) })
	}
	np.commands = nil
}
