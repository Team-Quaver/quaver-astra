package player

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeEngine 可控的假音频引擎（模拟 mpv：位置/时长由引擎持有）。
type fakeEngine struct {
	closed    bool
	mu        sync.Mutex
	playing   bool
	paused    bool
	eof       bool
	pos       float64
	dur       float64
	loaded    int
	seekTo    int
	vol       float64
	loadErr   string
	failURL   map[string]string // url → 载入错误（模拟 mpv 打不开）
	onLoadOK  func()
	onObserve func()
}

func (e *fakeEngine) OpenURL(url string, startFrac, dur float64, autoplay bool) error {
	e.mu.Lock()
	e.loaded++
	e.playing, e.paused, e.eof, e.pos = autoplay, !autoplay, false, startFrac*dur
	e.dur = dur
	e.loadErr = e.failURL[url]
	e.mu.Unlock()
	if e.onLoadOK != nil {
		e.onLoadOK()
	}
	return nil
}
func (e *fakeEngine) Play()  { e.mu.Lock(); e.paused = false; e.mu.Unlock() }
func (e *fakeEngine) Pause() { e.mu.Lock(); e.paused = true; e.mu.Unlock() }
func (e *fakeEngine) Stop()  { e.mu.Lock(); e.playing, e.eof, e.pos = false, false, 0; e.mu.Unlock() }
func (e *fakeEngine) Close() {
	e.mu.Lock()
	e.playing = false
	e.closed = true
	e.mu.Unlock()
}
func (e *fakeEngine) IsPlaying() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.playing && !e.paused
}
func (e *fakeEngine) Ended() bool  { e.mu.Lock(); defer e.mu.Unlock(); return e.eof && !e.paused }
func (e *fakeEngine) Paused() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.paused }
func (e *fakeEngine) Position() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pos
}

// Duration 模拟 mpv：载入失败时不报时长（awaitLoaded 据此判成功）。
func (e *fakeEngine) Duration(fallback float64) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErr != "" {
		return 0
	}
	if e.dur > 0 {
		return e.dur
	}
	return fallback
}
func (e *fakeEngine) SeekTo(frac, dur float64) {
	e.mu.Lock()
	e.seekTo++
	e.pos = frac * dur
	e.eof = false
	e.mu.Unlock()
}
func (e *fakeEngine) SetVolume(v float64) { e.mu.Lock(); e.vol = v; e.mu.Unlock() }
func (e *fakeEngine) OnObserve(fn func()) {
	e.mu.Lock()
	e.onObserve = fn
	e.mu.Unlock()
}

// LoadError 模拟 mpv 的异步载入失败上报。
func (e *fakeEngine) LoadError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadErr
}

// liveBackend 可控假后端。
type liveBackend struct {
	mu       sync.Mutex
	loggedIn bool
	lyricOK  bool
	likeErr  error
	likes    int
	tiers    *TierTable
	// loadFail 指定哪些档位在引擎侧载入失败（模拟 mpv 打不开该流）。
	loadFail map[string]bool
}

func (b *liveBackend) LoginStatus() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loggedIn, nil
}
func (b *liveBackend) UserInfo() (UserInfo, error) { return UserInfo{Name: "tester"}, nil }
func (b *liveBackend) LikedPage(page, num int) ([]Song, int64, bool, error) {
	if page == 1 {
		return []Song{{Mid: "l1", SongID: 11, SongType: 1}}, 1, false, nil
	}
	return nil, 0, false, nil
}
func (b *liveBackend) Playlists() ([]PlaylistRef, error) { return nil, nil }
func (b *liveBackend) Tiers() (*TierTable, error) {
	b.mu.Lock()
	t := b.tiers
	b.mu.Unlock()
	if t != nil {
		return t, nil
	}
	return &TierTable{Tiers: []Tier{{ID: "128", Rank: 10}, {ID: "320", Rank: 20}, {ID: "flac", Rank: 30}}}, nil
}
func (b *liveBackend) Resolve(mid, mediaMid string, songType int64, tier string, dep []string) (*StreamInfo, error) {
	// URL 里带上档位，测试才能按档位模拟「这一档 mpv 打不开」
	return &StreamInfo{URL: "http://x/" + mid + "/" + tier, Tier: tier, TierLabel: tier}, nil
}

// LoadFailFor 报告该档位是否应模拟为载入失败。
func (b *liveBackend) LoadFailFor(tier string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loadFail[tier]
}
func (b *liveBackend) FetchLyric(mid string, trans bool) (string, string, error) {
	if b.lyricOK {
		return "[00:01.0]hello\n[00:02.0]world", "[00:01.1]你好\n[00:02.1]世界", nil
	}
	return "", "", nil
}
func (b *liveBackend) LikeSong(int64, int64, bool) error {
	b.mu.Lock()
	b.likes++
	b.mu.Unlock()
	return b.likeErr
}

func newRacePlayer(t *testing.T) (*Player, *fakeEngine, *liveBackend) {
	t.Helper()
	eng := &fakeEngine{}
	be := &liveBackend{loggedIn: true, lyricOK: true}
	p := New(be, eng, fakePrefs{})
	var notifyCount atomic.Int64
	p.OnNotify(func() { notifyCount.Add(1) })
	p.Boot()
	return p, eng, be
}

func wait(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in 2s")
}

func TestRaceFullFlow(t *testing.T) {
	p, eng, _ := newRacePlayer(t)
	p.RefreshUser()
	wait(t, func() bool { p.mu.RLock(); defer p.mu.RUnlock(); return p.loggedIn })

	p.LoadLoved()
	wait(t, func() bool { return p.IsLoved("l1") })

	p.SetShowTranslation(true)
	p.loadTiers()

	// 播放列表（走 startCurrent 全链）
	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 100},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 100},
	}, 0)
	wait(t, func() bool { return eng.loadedCount() == 1 && !p.Loading() })

	// seek / 音质切换（重启当前曲）
	p.SeekTo(0.5)
	p.SwitchQuality("320")
	wait(t, func() bool { return eng.loadedCount() == 2 && !p.Loading() })

	// 自然播完 → 自动下一首
	eng.setEOF()
	p.Next(true)
	wait(t, func() bool { return p.Index() == 1 && eng.loadedCount() == 3 && !p.Loading() })

	// 收藏切换 + 删除当前曲 + 清空
	p.ToggleLove(Song{Mid: "b", SongID: 2, SongType: 1})
	p.RemoveAt(1)
	p.ClearQueue()

	// 狂点并发操作，制造竞争窗口
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.EnqueueNext(Song{Mid: "x", SongID: int64(i), SongType: 1})
			p.MoveInQueue([]int{0}, 1)
			_ = p.Quality()
			_ = p.TierTable()
			p.Jump(0)
			p.RemoveAt(0)
		}(i)
	}
	wg.Wait()
	p.ClearQueue()
	wait(t, func() bool { return len(p.Queue()) == 0 })
}

func (e *fakeEngine) loadedCount() int { e.mu.Lock(); defer e.mu.Unlock(); return e.loaded }
func (e *fakeEngine) setEOF()          { e.mu.Lock(); e.eof = true; e.mu.Unlock() }
