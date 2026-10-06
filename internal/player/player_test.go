package player

import (
	"testing"
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
func (stubBackend) StreamBytes(string) ([]byte, error)              { return nil, nil }
