package player

import (
	"encoding/json"
	"sync"
	"testing"
)

// mapPrefs 是能「落盘」的 Preferences（会话持久化测试用）：Set 记数，
// 测试据此断言去重/节流真的少写了盘。
type mapPrefs struct {
	mu   sync.Mutex
	m    map[string]any
	sets int
}

func newMapPrefs() *mapPrefs { return &mapPrefs{m: map[string]any{}} }

func (s *mapPrefs) String(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key].(string); ok {
		return v
	}
	return def
}

func (s *mapPrefs) Float(key string, def float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key].(float64); ok {
		return v
	}
	return def
}

func (s *mapPrefs) Bool(key string, def bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key].(bool); ok {
		return v
	}
	return def
}

func (s *mapPrefs) Set(key string, val any) {
	s.mu.Lock()
	s.m[key] = val
	s.sets++
	s.mu.Unlock()
}

func (s *mapPrefs) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sets
}

// TestSessionPersistAndRestore 端到端：起播 → 换模式 → 切歌 → seek 到
// 半程 → 退出（Close 强制落盘）→「重启」（新 Player 读同一份 prefs，Boot
// 恢复）。恢复必须只「就位」：队列、下标、模式、随机、进度都在，但不自动
// 起播；用户按播放才带位置走完整起播链。
func TestSessionPersistAndRestore(t *testing.T) {
	prefs := newMapPrefs()
	be := &liveBackend{loggedIn: true}
	eng1 := &fakeEngine{}
	p1 := New(be, eng1, prefs)
	p1.OnNotify(func() {})
	p1.Boot()

	p1.PlayList([]Song{
		{Mid: "a", Title: "A", Artists: "x", SongID: 1, SongType: 1, Interval: 100},
		{Mid: "b", Title: "B", Artists: "y", Album: "al", AlbumPmid: "cover", AlbumMid: "am",
			Singers: []Singer{{Mid: "y", Name: "歌手Y"}}, SongID: 2, SongType: 1, Interval: 200},
	}, 0)
	wait(t, func() bool { return eng1.loadedCount() == 1 && !p1.Loading() })

	p1.SetMode("one")
	p1.SetShuffle(true)
	p1.Jump(1)
	wait(t, func() bool { return eng1.loadedCount() == 2 && !p1.Loading() })
	p1.SeekTo(0.5) // 引擎位置 = 100s

	p1.Close() // 同步 flush 后才关引擎

	// —— 重启 ——
	eng2 := &fakeEngine{}
	p2 := New(be, eng2, prefs)
	p2.OnNotify(func() {})
	p2.Boot()

	q := p2.Queue()
	if len(q) != 2 || q[0].Mid != "a" || q[1].Mid != "b" {
		t.Fatalf("恢复的队列不对: %+v", q)
	}
	if q[1].AlbumPmid != "cover" || q[1].Singers[0].Name != "歌手Y" {
		t.Errorf("歌曲附属字段丢失: %+v", q[1])
	}
	if p2.Index() != 1 || p2.Mode() != "one" || !p2.Shuffle() {
		t.Errorf("index/mode/shuffle 未恢复: %d %s %v", p2.Index(), p2.Mode(), p2.Shuffle())
	}
	if p2.Position() != 100 {
		t.Errorf("恢复的进度 = %v，期望 100", p2.Position())
	}
	if p2.Playing() || eng2.loadedCount() != 0 {
		t.Errorf("恢复只应「就位」：不应自动起播/装载，playing=%v loaded=%d", p2.Playing(), eng2.loadedCount())
	}

	// 按播放 → 从原位续播（走完整起播链）。
	p2.PlayOrPause()
	wait(t, func() bool { return !p2.Loading() && p2.Playing() })
	if n := eng2.loadedCount(); n != 1 {
		t.Fatalf("PlayOrPause 在待命态应触发一次装载，loaded=%d", n)
	}
	if pos := eng2.Position(); pos != 100 {
		t.Errorf("续播位置 = %v，期望 100（startFrac 0.5 × 200s）", pos)
	}
}

// TestSessionProgressMidMismatch 钉住「进度跟错歌就丢弃」：队列保留、进度归零。
func TestSessionProgressMidMismatch(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set(sessionQueueKey, `{"songs":[{"mid":"a","interval":100},{"mid":"b","interval":100}],"index":1}`)
	prefs.Set(sessionPosKey, `{"mid":"zzz","pos":88}`)
	p := New(stubBackend{}, &fakeEngine{}, prefs)
	p.Boot()
	if len(p.Queue()) != 2 || p.Index() != 1 {
		t.Fatalf("队列未恢复: len=%d index=%d", len(p.Queue()), p.Index())
	}
	if p.Position() != 0 {
		t.Errorf("mid 不匹配时进度必须作废，实际 %v", p.Position())
	}
}

// TestSessionThrottleAndDedup 校验写盘纪律：同内容不重写；非 force（心跳
// 节流）只在切歌或跨 5 秒时写；force 任何变化都写（半秒量化）。
func TestSessionThrottleAndDedup(t *testing.T) {
	prefs := newMapPrefs()
	p := New(stubBackend{}, &fakeEngine{}, prefs)
	p.Boot()
	p.mu.Lock()
	p.queue = []Song{{Mid: "a", Interval: 100}}
	p.index = 0
	p.mu.Unlock()

	p.sessionSave(false)
	first := prefs.count()
	if first != 2 { // 队列 + 进度各写一次
		t.Fatalf("首次落盘应写 2 次，实际 %d", first)
	}
	p.sessionSave(false)
	if prefs.count() != first {
		t.Fatalf("内容未变不该重写: %d", prefs.count()-first)
	}

	p.mu.Lock()
	p.pos = 3
	p.mu.Unlock()
	p.sessionSave(false) // 3s < 5s 且同曲：节流拦下
	if prefs.count() != first {
		t.Fatalf("心跳小步不该写盘: %d", prefs.count()-first)
	}
	p.sessionSave(true) // 事件驱动（暂停/seek/退出）放行
	after := prefs.count()
	if after != first+1 {
		t.Fatalf("force 应写进度一次，delta=%d", after-first)
	}

	p.mu.Lock()
	p.pos = 9.2
	p.mu.Unlock()
	p.sessionSave(false) // 跨 5 秒阈值：放行，且量化到 9.0
	if got := prefs.String(sessionPosKey, ""); got != `{"mid":"a","pos":9}` {
		t.Fatalf("进度落盘值不对: %s", got)
	}
}

// TestSessionClearQueuePersists 校验清空队列后落盘的快照是空队列：否则
// 「清空了但下次启动又满血复活」。
func TestSessionClearQueuePersists(t *testing.T) {
	prefs := newMapPrefs()
	p := New(stubBackend{}, &fakeEngine{}, prefs)
	p.Boot()
	p.mu.Lock()
	p.queue = []Song{{Mid: "a", Interval: 100}}
	p.index = 0
	p.mu.Unlock()
	p.sessionSave(true)

	p.ClearQueue()
	p.sessionSave(true)

	var snap sessionQueue
	if err := json.Unmarshal([]byte(prefs.String(sessionQueueKey, "")), &snap); err != nil {
		t.Fatalf("队列快照解析失败: %v", err)
	}
	if len(snap.Songs) != 0 || snap.Index != -1 {
		t.Fatalf("清空后快照不对: %+v", snap)
	}
	p2 := New(stubBackend{}, &fakeEngine{}, prefs)
	p2.Boot()
	if len(p2.Queue()) != 0 {
		t.Errorf("重启后不该复活已清空的队列: %+v", p2.Queue())
	}
}

// TestSessionSeekWhileStaged 校验「就位态拖进度条」只挪界面进度、不碰引擎，
// 位置还会被下一次落盘带走。
func TestSessionSeekWhileStaged(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set(sessionQueueKey, `{"songs":[{"mid":"a","interval":100}],"index":0}`)
	eng := &fakeEngine{}
	p := New(stubBackend{}, eng, prefs)
	p.Boot()

	p.SeekTo(0.25)
	if p.Position() != 25 {
		t.Fatalf("就位态 seek 应直接挪界面进度，实际 %v", p.Position())
	}
	if n := eng.seekCount(); n != 0 {
		t.Fatalf("引擎没装载东西，不该收到 seek: %d", n)
	}
	p.sessionSave(true)
	if got := prefs.String(sessionPosKey, ""); got != `{"mid":"a","pos":25}` {
		t.Fatalf("seek 后的进度未落盘: %s", got)
	}
}

// TestSessionRestoreRobustness 校验坏配置不炸：非法 JSON、越界下标、非法模式
// 都按保守分支处理。
func TestSessionRestoreRobustness(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set(sessionQueueKey, `not-json`)
	p := New(stubBackend{}, &fakeEngine{}, prefs)
	p.Boot()
	if len(p.Queue()) != 0 {
		t.Fatalf("坏 JSON 不该恢复出队列: %+v", p.Queue())
	}

	prefs2 := newMapPrefs()
	prefs2.Set(sessionQueueKey, `{"songs":[{"mid":"a"}],"index":99,"mode":"bogus"}`)
	p2 := New(stubBackend{}, &fakeEngine{}, prefs2)
	p2.Boot()
	if p2.Index() != 0 {
		t.Errorf("越界下标应收敛到队尾，实际 %d", p2.Index())
	}
	if p2.Mode() != "all" {
		t.Errorf("非法模式应维持默认，实际 %s", p2.Mode())
	}
}

func (e *fakeEngine) seekCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seekTo
}
