// 系统媒体控制中心（MPRIS / SMTC）与播放器的桥接：
// player 通知 → 快照投影；媒体中心的命令 → player。
package appui

import (
	"strings"

	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/smedia"

	"github.com/egoist/mygo"
)

// initMedia 建立媒体控制中心会话；linux 注册 MPRIS，windows 等
// AttachWindow 提供窗口句柄后再激活 SMTC。
func (a *App) initMedia() {
	a.media = smedia.New("Quaver Astra", a.mediaCommands())
}

// mediaSnapshot 从播放器采集媒体中心需要的最小状态。
func (a *App) mediaSnapshot() smedia.Snapshot {
	cur, has := a.PL.Current()
	s := smedia.Snapshot{
		HasTrack: has,
		Playing:  a.PL.Playing(),
		Position: a.PL.Position(),
		Duration: a.PL.Duration(),
		Loop:     a.PL.Mode(),
		Shuffle:  a.PL.Shuffle(),
		Volume:   a.PL.Volume(),
	}
	if a.PL.Muted() {
		s.Volume = 0
	}
	if has {
		s.TrackID = cur.Mid
		s.Title = oneLine(cur.DisplayName())
		s.Artist = oneLine(artistLine(cur))
		s.Album = oneLine(cur.Album)
		s.ArtURL = coverURL(cur.AlbumPmid, cur.AlbumMid, 500)
	}
	return s
}

// artistLine 歌手串：优先逐个歌手，回退到拼接串按「/」拆分。
func artistLine(song player.Song) string {
	names := make([]string, 0, len(song.Singers))
	for _, singer := range song.Singers {
		if n := oneLine(singer.Name); n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		return strings.Join(names, ", ")
	}
	if song.Artists != "" {
		parts := strings.Split(song.Artists, "/")
		for i, p := range parts {
			parts[i] = oneLine(p)
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

// syncMedia 把最新状态推给媒体控制中心（内部有 diff，重复推送廉价）。
func (a *App) syncMedia() {
	if a.media == nil {
		return
	}
	a.media.Update(a.mediaSnapshot())
}

// mediaCommands 把媒体中心命令映射到 player。这些回调可能从 D-Bus /
// WinRT 事件线程直接进入，player 并发安全；涉及窗口的操作必须回 UI 线程。
func (a *App) mediaCommands() *smedia.Commands {
	return &smedia.Commands{
		PlayPause: a.PL.PlayOrPause,
		Play: func() {
			if !a.PL.Playing() {
				a.PL.PlayOrPause()
			}
		},
		Pause: func() {
			if a.PL.Playing() {
				a.PL.PlayOrPause()
			}
		},
		Stop: a.PL.ClearQueue,
		Next: func() { a.PL.Next(false) },
		Prev: a.PL.Prev,
		Seek: func(delta float64) {
			dur := a.PL.Duration()
			if dur <= 0 {
				return
			}
			target := a.PL.Position() + delta
			if target < 0 {
				target = 0
			}
			if target > dur {
				target = dur
			}
			a.PL.SeekTo(target / dur)
		},
		SetPosition: func(pos float64, trackID string) {
			cur, has := a.PL.Current()
			// 没有（或已换）当前曲的跳转是系统 UI 的陈旧请求，丢弃。
			if !has || (trackID != "" && cur.Mid != trackID) {
				return
			}
			dur := a.PL.Duration()
			if dur <= 0 {
				return
			}
			if pos < 0 {
				pos = 0
			}
			if pos > dur {
				pos = dur
			}
			a.PL.SeekTo(pos / dur)
		},
		SetVolume: func(v float64) {
			if v > 0 && a.PL.Muted() {
				// 系统侧把音量从 0 拉起时顺带解除静音。
				a.PL.SetVolume(v)
				a.PL.ToggleMute()
				return
			}
			a.PL.SetVolume(v)
		},
		SetLoop:    a.PL.SetMode,
		SetShuffle: a.PL.SetShuffle,
		Raise: func() {
			a.update(a.toggleWindow)
		},
		Quit: func() {
			// 退出必须回主线程；mygo.App.Quit 触发 OnQuit → App.Close 收口。
			a.update(func() { mygo.App.Quit() })
		},
	}
}
