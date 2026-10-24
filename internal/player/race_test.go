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
	loadOK    bool
	loadFatal bool
	failURL   map[string]string // url → 载入错误（模拟 mpv 打不开该档）
	fatalURL  map[string]string // url → 引擎级错误（模拟 mpv 拒绝命令）
	hang      bool              // 载入永不成形（模拟卡到超时）
	starts    []float64         // 依次记录每次 OpenURL 的 startFrac
	// openErr 按 (url, startFrac) 决定这次载入的结果，模拟「只有带进度起播
	// 才失败」这类档位之外的失败。返回空串表示载入成功。
	openErr   func(url string, startFrac float64) (msg string, fatal bool)
	onLoadOK  func()
	onObserve func()
}

func (e *fakeEngine) OpenURL(url string, startFrac, dur float64, autoplay bool) error {
	e.mu.Lock()
	e.loaded++
	e.playing, e.paused, e.eof, e.pos = autoplay, !autoplay, false, startFrac*dur
	e.dur = dur
	e.starts = append(e.starts, startFrac)
	e.loadErr, e.loadOK, e.loadFatal = "", true, false
	if e.openErr != nil {
		if msg, fatal := e.openErr(url, startFrac); msg != "" {
			e.loadErr, e.loadOK, e.loadFatal = msg, false, fatal
		}
	} else if msg, bad := e.failURL[url]; bad {
		e.loadErr, e.loadOK = msg, false
	} else if msg, bad := e.fatalURL[url]; bad {
		// 引擎级故障：与档位无关，player 该收手而不是把降档链走完
		e.loadErr, e.loadOK, e.loadFatal = msg, false, true
	}
	if e.hang {
		e.loadErr, e.loadOK, e.loadFatal = "", false, false
	}
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

// Loaded 模拟 mpv 的 file-loaded：只有真载入成功才算成形的（不靠时长凑数）。
func (e *fakeEngine) Loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadOK
}

// LoadErrorFatal 模拟引擎级错误标记。
func (e *fakeEngine) LoadErrorFatal() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadFatal
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
	// guessRounds 依次作为无限的电台每一轮返回；空时给默认两首。
	guessRounds [][]Song
	guessCalls  int
	// loadFail 指定哪些档位在引擎侧载入失败（模拟 mpv 打不开该流）。
	loadFail map[string]bool
	// listenReports 记录音响力流水；listenErr 模拟网络/HTTP 失败。
	listenReports []ListenReport
	listenErr     error
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
func (b *liveBackend) GuessSongs(int) ([]Song, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.guessCalls++
	if len(b.guessRounds) > 0 {
		at := min(b.guessCalls-1, len(b.guessRounds)-1)
		return append([]Song(nil), b.guessRounds[at]...), nil
	}
	return []Song{{Mid: "radio-1", SongID: 101, SongType: 1}, {Mid: "radio-2", SongID: 102, SongType: 1}}, nil
}
func (b *liveBackend) Tiers() (*TierTable, error) {
	b.mu.Lock()
	t := b.tiers
	b.mu.Unlock()
	if t != nil {
		return t, nil
	}
	return &TierTable{Tiers: []Tier{{ID: "128", Rank: 10}, {ID: "320", Rank: 20}, {ID: "flac", Rank: 30}}}, nil
}
func (b *liveBackend) Resolve(mid, mediaMid string, songType int64, tier string, dep []string, durationMs int64) (*StreamInfo, error) {
	// URL 里带上档位，测试才能按档位模拟「这一档 mpv 打不开」
	return &StreamInfo{URL: "http://x/" + mid + "/" + tier, Tier: tier, TierLabel: tier, Token: "token-" + mid + "-" + tier}, nil
}

func (b *liveBackend) ReportListenTime(report ListenReport) (ListenReportResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listenErr != nil {
		return ListenReportResult{}, b.listenErr
	}
	b.listenReports = append(b.listenReports, report)
	return ListenReportResult{OK: true}, nil
}

// LoadFailFor 报告该档位是否应模拟为载入失败。
func (b *liveBackend) LoadFailFor(tier string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loadFail[tier]
}
func (b *liveBackend) FetchLyric(mid string, trans, roma bool) (string, string, string, error) {
	if b.lyricOK {
		return "[00:01.0]hello\n[00:02.0]world", "[00:01.1]你好\n[00:02.1]世界", "[00:01.0]hello\n[00:02.0]world", nil
	}
	return "", "", "", nil
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
