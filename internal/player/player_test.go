package player

import (
	"io"
	"testing"
	"time"
)

func TestParseLrc(t *testing.T) {
	lrc := "[00:01.00]first line\n[00:12.5]second\n[01:02.300]third\n[00:01.00]//comment-ish\n作词：某人\n"
	trans := "[00:01.10]first trans\n[00:12.60]second trans\n[01:03.10]third trans\n"
	lines := ParseLrc(lrc, trans)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Time != 1.0 || lines[0].Text != "first line" {
		t.Errorf("line0 wrong: %+v", lines[0])
	}
	if lines[1].Time != 12.5 || lines[2].Time != 62.3 {
		t.Errorf("times wrong: %v %v", lines[1].Time, lines[2].Time)
	}
	// 翻译按就近（<1.5s）对齐
	if lines[0].Trans != "first trans" || lines[2].Trans != "third trans" {
		t.Errorf("trans mismatch: %+v", lines)
	}
}

func TestParseLrcMultiTimestamp(t *testing.T) {
	lines := ParseLrc("[00:05.00][01:05.00]repeat", "")
	if len(lines) != 2 || lines[0].Time != 5 || lines[1].Time != 65 {
		t.Fatalf("multi-timestamp expand failed: %+v", lines)
	}
}

func TestLyricIndexAt(t *testing.T) {
	lines := ParseLrc("[00:10.00]a\n[00:20.00]b\n[00:30.00]c\n", "")
	cases := []struct {
		t    float64
		want int
	}{{0, -1}, {9.7, -1}, {9.9, 0}, {19.0, 0}, {19.9, 1}, {100, 2}}
	for _, c := range cases {
		if got := LyricIndexAt(lines, c.t); got != c.want {
			t.Errorf("LyricIndexAt(%v) = %d, want %d", c.t, got, c.want)
		}
	}
}

func TestShuffleDeterministic(t *testing.T) {
	// 同一天、同一队列 → 同一套顺序（每日一套）
	mkQueue := func(n int) []Song {
		q := make([]Song, n)
		for i := range q {
			q[i] = Song{Mid: string(rune('a' + i))}
		}
		return q
	}
	mk := func() *Player {
		p := New(stubBackend{}, nil, fakePrefs{})
		p.queue = mkQueue(20)
		return p
	}
	a, b := mk(), mk()
	a.mu.Lock()
	a.ensureShufLocked()
	ordA := append([]int(nil), a.shufOrder...)
	a.mu.Unlock()
	b.mu.Lock()
	b.ensureShufLocked()
	ordB := append([]int(nil), b.shufOrder...)
	b.mu.Unlock()
	if len(ordA) != 20 {
		t.Fatalf("order length %d", len(ordA))
	}
	for i := range ordA {
		if ordA[i] != ordB[i] {
			t.Fatalf("orders differ: %v vs %v", ordA, ordB)
		}
	}
	// 是个排列
	seen := map[int]bool{}
	for _, v := range ordA {
		if seen[v] {
			t.Fatalf("duplicate %d", v)
		}
		seen[v] = true
	}
}

func TestNextIndexModes(t *testing.T) {
	mk := func() *Player {
		p := New(stubBackend{}, nil, fakePrefs{})
		p.queue = []Song{{Mid: "a"}, {Mid: "b"}, {Mid: "c"}}
		p.index = 0
		return p
	}
	// 列表循环：末尾回绕
	p := mk()
	p.mode = "all"
	if got := p.nextIndexLocked(true); got != 1 {
		t.Errorf("all: got %d", got)
	}
	p.index = 2
	if got := p.nextIndexLocked(true); got != 0 {
		t.Errorf("all wrap: got %d", got)
	}
	// 顺序播放：末尾即停
	p = mk()
	p.mode = "off"
	p.index = 2
	if got := p.nextIndexLocked(true); got != -1 {
		t.Errorf("off auto at end: got %d", got)
	}
	// 手动下一首总是回绕
	if got := p.nextIndexLocked(false); got != 0 {
		t.Errorf("off manual wrap: got %d", got)
	}
	// 单曲循环
	p = mk()
	p.mode = "one"
	p.index = 1
	if got := p.nextIndexLocked(true); got != 1 {
		t.Errorf("one: got %d", got)
	}
}

type fakePrefs struct{}

// TestEnqueueNextSemantics 钉住「下一首播放」的两分支语义（对齐主项目
//
//  1. 队列非空：插到当前曲之后（index+1），index 与当前曲不动，引擎不再
//     载入——插队不打断当前播放；
//  2. 队列空：没有「下一首」可言，退化成单曲起播。
//
// 旧实现的「插队播放」走 PlayNextNow（立即切歌），右键一次当前曲就被打断，
// 这条测试就是那次语义改动的回归保护。
func TestEnqueueNextSemantics(t *testing.T) {
	eng := &fakeEngine{}
	p := New(stubBackend{}, eng, fakePrefs{})
	p.queue = []Song{{Mid: "a"}, {Mid: "b"}, {Mid: "c"}}
	p.index = 0

	p.EnqueueNext(Song{Mid: "x"})
	q := p.Queue()
	if len(q) != 4 || q[1].Mid != "x" {
		t.Fatalf("插入后队列 = %v，期望 [a x b c]", q)
	}
	if p.Index() != 0 {
		t.Errorf("index = %d，插入不应切换当前曲", p.Index())
	}
	if cur, ok := p.Current(); !ok || cur.Mid != "a" {
		t.Errorf("当前曲 = %v/%v，插入打断了播放", cur, ok)
	}
	if n := eng.loadedCount(); n != 0 {
		t.Errorf("引擎载入了 %d 次——「下一首播放」不该触发起播", n)
	}

	// 空队列：退化为直接起播这一首。
	p2 := New(stubBackend{}, &fakeEngine{}, fakePrefs{})
	p2.EnqueueNext(Song{Mid: "s"})
	if q := p2.Queue(); len(q) != 1 || q[0].Mid != "s" {
		t.Errorf("空队列退化后的队列 = %v，期望 [s]", q)
	}
	if p2.Index() != 0 {
		t.Errorf("空队列退化后 index = %d，期望 0", p2.Index())
	}
	if cur, ok := p2.Current(); !ok || cur.Mid != "s" {
		t.Errorf("空队列退化后当前曲 = %v/%v，期望 s 起播", cur, ok)
	}
}

func (fakePrefs) String(key, def string) string { return def }
func (fakePrefs) Float(key string, def float64) float64 {
	return def
}
func (fakePrefs) Bool(key string, def bool) bool { return def }
func (fakePrefs) Set(key string, val any)        {}

// stubBackend 不触网的空后端（只测队列/模式逻辑）。
type stubBackend struct{}

func (stubBackend) LoginStatus() (bool, error)                      { return false, nil }
func (stubBackend) UserInfo() (UserInfo, error)                     { return UserInfo{}, nil }
func (stubBackend) LikedPage(int, int) ([]Song, int64, bool, error) { return nil, 0, false, nil }
func (stubBackend) Playlists() ([]PlaylistRef, error)               { return nil, nil }
func (stubBackend) Tiers() (*TierTable, error)                      { return nil, nil }
func (stubBackend) Resolve(string, string, int64, string, []string) (*StreamInfo, error) {
	return nil, nil
}
func (stubBackend) FetchLyric(string, bool) (string, string, error) { return "", "", nil }
func (stubBackend) LikeSong(int64, int64, bool) error               { return nil }
func (stubBackend) StreamOpen(string, int64) (io.ReadCloser, error) { return nil, nil }

// TestFallbackOnLoadError 验证降级链：mpv 异步载入失败（LoadError 非空）
// 时自动换下一档重试，而不是把失败暴露给用户。
//
// 这是换用 mpv 后最容易回归的一环——引擎载入是异步的，拿不到同步错误
// 返回，只能靠 awaitLoaded 轮询后回退。
func TestFallbackOnLoadError(t *testing.T) {
	eng := &fakeEngine{
		// URL 形如 http://x/<mid>/<tier>，据此模拟前两档打不开
		failURL: map[string]string{
			"http://x/a/master":  "mpv 载入失败",
			"http://x/a/atmos71": "mpv 载入失败",
		},
	}
	be := &liveBackend{loggedIn: true}
	be.tiers = &TierTable{Tiers: []Tier{
		{ID: "master", Rank: 90},
		{ID: "atmos71", Rank: 80},
		{ID: "atmos51", Rank: 75},
		{ID: "flac", Rank: 70},
	}}
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.Boot()
	p.tiers = be.tiers
	p.PlayList([]Song{{Mid: "a", SongID: 1, SongType: 1, Interval: 100}}, 0)

	// 回退链按固定 rank 顺序往下让位：master → atmos71 → atmos51 → flac…
	// 前两档被标记为打不开，所以应停在第一个可播的 atmos51。
	const want = "atmos51"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := p.Stream(); st != nil && st.Tier == want && !p.Loading() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Error(); err != "" {
		t.Fatalf("降级成功后不应报错，实际: %s", err)
	}
	st := p.Stream()
	if st == nil {
		t.Fatal("未解析出流")
	}
	if st.Tier != want {
		t.Fatalf("应降级到 %s，实际 %s", want, st.Tier)
	}
	if n := eng.loadedCount(); n < 3 {
		t.Errorf("应至少尝试 3 档，实际 %d", n)
	}
}
