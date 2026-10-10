//go:build linux

// MPRIS2 实现：org.mpris.MediaPlayer2 + org.mpris.MediaPlayer2.Player，
// 会话总线上注册 org.mpris.MediaPlayer2.<identity>。
package smedia

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	mprisPath  = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	mprisRoot  = "org.mpris.MediaPlayer2"
	mprisPlyr  = "org.mpris.MediaPlayer2.Player"
	mprisProps = "org.freedesktop.DBus.Properties"
)

// New 连接会话总线并注册 MPRIS 服务；无 D-Bus 会话或名字被占时退化为 no-op。
func New(identity string, cmds *Commands) Controller {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return noop{}
	}
	m := &mpris{
		conn:  conn,
		cmds:  cmds,
		ident: identity,
	}
	for _, iface := range []string{mprisRoot, mprisProps} {
		if err := conn.Export(m, mprisPath, iface); err != nil {
			conn.Close()
			return noop{}
		}
	}
	// Player 接口的 Seek 用映射注册，避免与 io.Seeker 形参撞车触发 vet。
	if err := conn.ExportWithMap(m, map[string]string{"SeekRel": "Seek"}, mprisPath, mprisPlyr); err != nil {
		conn.Close()
		return noop{}
	}
	name := "org.mpris.MediaPlayer2." + busNameSuffix(identity)
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue|dbus.NameFlagReplaceExisting)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return noop{}
	}
	return m
}

// busNameSuffix 把应用名折算成合法的总线名末段（去掉标点等非法字符）。
func busNameSuffix(identity string) string {
	out := make([]rune, 0, len(identity))
	for _, r := range identity {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "app"
	}
	return string(out)
}

type mpris struct {
	conn *dbus.Conn
	cmds *Commands

	busName string

	ident string

	mu   sync.Mutex
	snap Snapshot
	// lastSig 记录上次已广播的状态签名，避免高频通知重复发 PropertiesChanged。
	lastSig string
	closed  bool
}

// D-Bus 标准属性错误（godbus 未内置常量）。
var (
	errUnknownProperty = &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
	errUnknownIface    = &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface"}
)

func (m *mpris) AttachWindow(uintptr) {}

func (m *mpris) Update(s Snapshot) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	old := m.snap
	m.snap = s
	sig := m.stateSig()
	if sig == m.lastSig {
		m.mu.Unlock()
		return
	}
	changed := map[string]dbus.Variant{}
	if old.Playing != s.Playing || old.HasTrack != s.HasTrack {
		changed["PlaybackStatus"] = dbus.MakeVariant(m.playbackStatus())
	}
	if old.Loop != s.Loop {
		changed["LoopStatus"] = dbus.MakeVariant(m.loopStatus())
	}
	if old.Shuffle != s.Shuffle {
		changed["Shuffle"] = dbus.MakeVariant(s.Shuffle)
	}
	if old.Volume != s.Volume {
		changed["Volume"] = dbus.MakeVariant(s.Volume)
	}
	if old.HasTrack != s.HasTrack || old.Duration != s.Duration {
		changed["CanSeek"] = dbus.MakeVariant(s.HasTrack && s.Duration > 0)
		changed["CanPlay"] = dbus.MakeVariant(s.HasTrack)
		changed["CanPause"] = dbus.MakeVariant(s.HasTrack)
	}
	if old.HasTrack != s.HasTrack {
		changed["CanGoNext"] = dbus.MakeVariant(s.HasTrack)
		changed["CanGoPrevious"] = dbus.MakeVariant(s.HasTrack)
	}
	if old.HasTrack != s.HasTrack || old.TrackID != s.TrackID || old.Title != s.Title ||
		old.Artist != s.Artist || old.Album != s.Album || old.ArtURL != s.ArtURL {
		changed["Metadata"] = dbus.MakeVariant(m.metadata())
	}
	m.lastSig = sig
	m.mu.Unlock()

	if len(changed) > 0 {
		m.conn.Emit(mprisPath, mprisProps+".PropertiesChanged",
			mprisPlyr, changed, []string{})
	}
}

func (m *mpris) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.conn.ReleaseName(m.busName)
	m.conn.Close()
}

// ===== 状态投影 =====

func (m *mpris) playbackStatus() string {
	if m.snap.HasTrack && m.snap.Playing {
		return "Playing"
	}
	if m.snap.HasTrack {
		return "Paused"
	}
	return "Stopped"
}

func (m *mpris) loopStatus() string {
	switch m.snap.Loop {
	case "one":
		return "Track"
	case "all":
		return "Playlist"
	}
	return "None"
}

func (m *mpris) metadata() map[string]dbus.Variant {
	md := map[string]dbus.Variant{}
	if !m.snap.HasTrack {
		return md
	}
	md["mpris:trackid"] = dbus.MakeVariant(m.trackPath())
	md["xesam:title"] = dbus.MakeVariant(m.snap.Title)
	if m.snap.Artist != "" {
		md["xesam:artist"] = dbus.MakeVariant([]string{m.snap.Artist})
	}
	if m.snap.Album != "" {
		md["xesam:album"] = dbus.MakeVariant(m.snap.Album)
	}
	if m.snap.ArtURL != "" {
		md["mpris:artUrl"] = dbus.MakeVariant(m.snap.ArtURL)
	}
	md["mpris:length"] = dbus.MakeVariant(int64(m.snap.Duration * 1e6))
	return md
}

func (m *mpris) trackPath() dbus.ObjectPath {
	id := m.snap.TrackID
	if id == "" {
		id = "no-track"
	}
	return dbus.ObjectPath(fmt.Sprintf("/org/mpris/MediaPlayer2/Track/%s", id))
}

func (m *mpris) stateSig() string {
	return fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%t\x00%.4f\x00%s\x00%s\x00%s",
		m.snap.TrackID, m.snap.HasTrack, m.snap.Playing, m.snap.Loop, m.snap.Shuffle,
		m.snap.Volume, m.snap.Title, m.snap.Artist, m.snap.ArtURL)
}

// ===== org.mpris.MediaPlayer2（根接口）=====

func (m *mpris) Raise() *dbus.Error {
	if m.cmds != nil && m.cmds.Raise != nil {
		m.cmds.Raise()
	}
	return nil
}

func (m *mpris) Quit() *dbus.Error {
	if m.cmds != nil && m.cmds.Quit != nil {
		m.cmds.Quit()
	}
	return nil
}

// ===== org.mpris.MediaPlayer2.Player =====

func (m *mpris) PlayPause() *dbus.Error {
	if m.cmds != nil && m.cmds.PlayPause != nil {
		m.cmds.PlayPause()
	}
	return nil
}

func (m *mpris) Play() *dbus.Error {
	if m.cmds != nil && m.cmds.Play != nil {
		m.cmds.Play()
	}
	return nil
}

func (m *mpris) Pause() *dbus.Error {
	if m.cmds != nil && m.cmds.Pause != nil {
		m.cmds.Pause()
	}
	return nil
}

func (m *mpris) Stop() *dbus.Error {
	if m.cmds != nil && m.cmds.Stop != nil {
		m.cmds.Stop()
	}
	return nil
}

func (m *mpris) Next() *dbus.Error {
	if m.cmds != nil && m.cmds.Next != nil {
		m.cmds.Next()
	}
	return nil
}

func (m *mpris) Previous() *dbus.Error {
	if m.cmds != nil && m.cmds.Prev != nil {
		m.cmds.Prev()
	}
	return nil
}

// SeekRel 相对跳转，offset 单位微秒（经 ExportWithMap 映射为 D-Bus 的 Seek）。
func (m *mpris) SeekRel(offset int64) *dbus.Error {
	m.mu.Lock()
	pos, dur := m.snap.Position, m.snap.Duration
	m.mu.Unlock()
	if m.cmds != nil && m.cmds.Seek != nil {
		m.cmds.Seek(float64(offset) / 1e6)
	}
	target := pos + float64(offset)/1e6
	if dur > 0 && target > dur {
		target = dur
	}
	if target < 0 {
		target = 0
	}
	m.emitSeeked(target)
	return nil
}

// SetPosition 绝对跳转；trackId 不匹配当前曲目时丢弃（MPRIS 规范要求）。
func (m *mpris) SetPosition(trackId dbus.ObjectPath, position int64) *dbus.Error {
	if trackId != m.trackPath() {
		return nil
	}
	if m.cmds != nil && m.cmds.SetPosition != nil {
		m.cmds.SetPosition(float64(position)/1e6, m.snap.TrackID)
	}
	m.emitSeeked(float64(position) / 1e6)
	return nil
}

func (m *mpris) OpenUri(string) *dbus.Error { return nil }

// emitSeeked 规范要求 seek 完成后发 Seeked 信号；这里在命令下发时
// 以目标位置近似（mpv 端回执的位置随后经 Update 刷新到进度条）。
func (m *mpris) emitSeeked(posSec float64) {
	m.conn.Emit(mprisPath, mprisPlyr+".Seeked", int64(posSec*1e6))
}

// ===== org.freedesktop.DBus.Properties =====

func (m *mpris) Get(iface, prop string) (dbus.Variant, *dbus.Error) {
	all, err := m.GetAll(iface)
	if err != nil {
		return dbus.Variant{}, err
	}
	v, ok := all[prop]
	if !ok {
		return dbus.Variant{}, errUnknownProperty
	}
	return v, nil
}

func (m *mpris) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch iface {
	case mprisRoot:
		return map[string]dbus.Variant{
			"CanQuit":             dbus.MakeVariant(true),
			"CanRaise":            dbus.MakeVariant(true),
			"HasTrackList":        dbus.MakeVariant(false),
			"Identity":            dbus.MakeVariant(m.ident),
			"DesktopEntry":        dbus.MakeVariant("quaver-astra"),
			"SupportedUriSchemes": dbus.MakeVariant([]string{"http", "https"}),
			"SupportedMimeTypes":  dbus.MakeVariant([]string{}),
		}, nil
	case mprisPlyr:
		s := m.snap
		return map[string]dbus.Variant{
			"PlaybackStatus": dbus.MakeVariant(m.playbackStatus()),
			"LoopStatus":     dbus.MakeVariant(m.loopStatus()),
			"Rate":           dbus.MakeVariant(1.0),
			"Shuffle":        dbus.MakeVariant(s.Shuffle),
			"Metadata":       dbus.MakeVariant(m.metadata()),
			"Volume":         dbus.MakeVariant(s.Volume),
			"Position":       dbus.MakeVariant(int64(s.Position * 1e6)),
			"MinimumRate":    dbus.MakeVariant(1.0),
			"MaximumRate":    dbus.MakeVariant(1.0),
			"CanGoNext":      dbus.MakeVariant(s.HasTrack),
			"CanGoPrevious":  dbus.MakeVariant(s.HasTrack),
			"CanPlay":        dbus.MakeVariant(s.HasTrack),
			"CanPause":       dbus.MakeVariant(s.HasTrack),
			"CanSeek":        dbus.MakeVariant(s.HasTrack && s.Duration > 0),
			"CanControl":     dbus.MakeVariant(true),
		}, nil
	}
	return nil, errUnknownIface
}

func (m *mpris) Set(iface, prop string, val dbus.Variant) *dbus.Error {
	if iface != mprisPlyr {
		return errUnknownIface
	}
	if m.cmds == nil {
		return nil
	}
	switch prop {
	case "LoopStatus":
		if m.cmds.SetLoop == nil {
			return nil
		}
		switch s, _ := val.Value().(string); s {
		case "Track":
			m.cmds.SetLoop("one")
		case "Playlist":
			m.cmds.SetLoop("all")
		case "None":
			m.cmds.SetLoop("off")
		}
	case "Shuffle":
		if m.cmds.SetShuffle != nil {
			if b, ok := val.Value().(bool); ok {
				m.cmds.SetShuffle(b)
			}
		}
	case "Volume":
		if m.cmds.SetVolume != nil {
			if f, ok := val.Value().(float64); ok {
				if f < 0 {
					f = 0
				}
				if f > 1 {
					f = 1
				}
				m.cmds.SetVolume(f)
			}
		}
	case "Rate":
		// 仅支持 1.0，忽略其他写入。
	case "Position":
		// 规范规定 Position 不可写。
	default:
		return errUnknownProperty
	}
	return nil
}
