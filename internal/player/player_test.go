package player

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseLrc(t *testing.T) {
	lrc := "[00:01.00]first line\n[00:12.5]second\n[01:02.300]third\n[00:01.00]//comment-ish\n作词：某人\n"
	trans := "[00:01.10]first trans\n[00:12.60]second trans\n[01:03.10]third trans\n"
	lines := ParseLrc(lrc, trans, "")
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

// 主歌词里的制作名单（没有翻译）不能抢走下一句正文的翻译。
// 真实形状来自《起死回生》的 QQ 歌词：Mastering 行在 10.05s，
// 正文在 11.08s，翻译在 11.07s——名单行与翻译只差 1.02s。
func TestParseLrcAuxSkipsUntranslatedCredits(t *testing.T) {
	lrc := "[00:10.05]Mastering：Akihiro Shiba (TEMAS)\n" +
		"[00:11.08]The sun light shines on battered body\n" +
		"[00:13.64]This pain a living evidence\n"
	trans := "[00:11.07]阳光洒在伤痕累累的身躯上\n" +
		"[00:13.64]这般苦楚 正是鲜活生命的象征\n"
	lines := ParseLrc(lrc, trans, "")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Trans != "" {
		t.Errorf("制作名单行不应带翻译，实得 %q", lines[0].Trans)
	}
	if lines[1].Trans != "阳光洒在伤痕累累的身躯上" {
		t.Errorf("正文翻译错位：%q", lines[1].Trans)
	}
	if lines[2].Trans != "这般苦楚 正是鲜活生命的象征" {
		t.Errorf("后续翻译错位：%q", lines[2].Trans)
	}
}

// 制作名单和正文时间戳完全相同（《RUNNING FOR YOUR LIFE》形状）。
// 翻译文件用 "//" 占位保持逐行对应，占位行不能被丢掉，否则后面的
// 翻译会整体往前错一行。
func TestParseLrcAuxKeepsPlaceholderSlots(t *testing.T) {
	lrc := "[00:00.28]词：Nicholas Chiari\n" +
		"[00:00.29]编曲：Grabbitz\n" +
		"[00:00.30]音乐监制：Steven Tang/Grass\n" +
		"[00:00.30]You can run You can try to hide\n" +
		"[00:04.59]Just know I'm gonna have you\n"
	trans := "[00:00.28]//\n" +
		"[00:00.29]//\n" +
		"[00:00.30]//\n" +
		"[00:00.30]逃吧 躲吧\n" +
		"[00:04.59]亡命败将\n"
	lines := ParseLrc(lrc, trans, "")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines, got %d: %+v", len(lines), lines)
	}
	if lines[1].Trans != "" {
		t.Errorf("制作名单行不应带翻译，实得 %q", lines[1].Trans)
	}
	if lines[2].Trans != "逃吧 躲吧" {
		t.Errorf("正文翻译错位：%q", lines[2].Trans)
	}
	if lines[3].Trans != "亡命败将" {
		t.Errorf("后续翻译错位：%q", lines[3].Trans)
	}
}

func TestParseLrcMultiTimestamp(t *testing.T) {
	lines := ParseLrc("[00:05.00][01:05.00]repeat", "", "")
	if len(lines) != 2 || lines[0].Time != 5 || lines[1].Time != 65 {
		t.Fatalf("multi-timestamp expand failed: %+v", lines)
	}
}

func TestLyricIndexAt(t *testing.T) {
	lines := ParseLrc("[00:10.00]a\n[00:20.00]b\n[00:30.00]c\n", "", "")
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
func (stubBackend) GuessSongs(int) ([]Song, error)                  { return nil, nil }
func (stubBackend) Tiers() (*TierTable, error)                      { return nil, nil }
func (stubBackend) Resolve(string, string, int64, string, []string, int64) (*StreamInfo, error) {
	return nil, nil
}
func (stubBackend) FetchLyric(string, bool, bool) (string, string, string, error) {
	return "", "", "", nil
}
func (stubBackend) ReportListenTime(ListenReport) (ListenReportResult, error) {
	return ListenReportResult{OK: true}, nil
}
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
			"http://x/a/atmos2":  "mpv 载入失败",
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

	// 默认关闭全景声回退：回退链按 rank 向下让位，但跳过 7.1 / 5.1
	// （atmos2 不属于「全景声」开关管辖）。master / atmos2 都打不开时
	// 应落到 flac，而不是悄悄换成全景声。
	const want = "flac"
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
	if n := eng.loadedCount(); n < 2 {
		t.Errorf("应至少尝试 2 档（master → flac），实际 %d", n)
	}
}

func TestSetModeAndClose(t *testing.T) {
	eng := &fakeEngine{}
	notify := 0
	p := New(stubBackend{}, eng, fakePrefs{})
	p.OnNotify(func() { notify++ })

	p.SetMode("one")
	if p.Mode() != "one" || notify == 0 {
		t.Fatalf("SetMode 未生效/未通知: mode=%s notify=%d", p.Mode(), notify)
	}
	before := notify
	p.SetMode("bogus")
	if p.Mode() != "one" || notify != before {
		t.Fatalf("非法模式不应生效: mode=%s notify=%d", p.Mode(), notify)
	}

	p.playing = true
	p.Close()
	if p.Playing() || !eng.closed {
		t.Fatalf("Close 未停止播放引擎: playing=%v closed=%v", p.Playing(), eng.closed)
	}
}

func TestQueueRowViewAvoidsFullSongSnapshot(t *testing.T) {
	p := New(stubBackend{}, nil, fakePrefs{})
	p.queue = []Song{
		{Mid: "a", Name: "A", Artists: "x", Singers: []Singer{{Mid: "x", Name: "x"}}},
		{Mid: "b", Title: "B", AlbumPmid: "cover"},
	}

	if got := p.QueueLen(); got != 2 {
		t.Fatalf("QueueLen = %d, want 2", got)
	}
	row, ok := p.QueueRow(1)
	if !ok || row.Mid != "b" || row.DisplayName() != "B" || row.AlbumPmid != "cover" {
		t.Fatalf("QueueRow(1) = %+v/%v", row, ok)
	}
	if _, ok := p.QueueRow(2); ok {
		t.Fatal("QueueRow accepted an out-of-range index")
	}
}

func TestMoveInQueuePreservesOrder(t *testing.T) {
	p := New(stubBackend{}, nil, fakePrefs{})
	p.queue = []Song{{Mid: "a"}, {Mid: "b"}, {Mid: "c"}, {Mid: "d"}}
	p.index = 2

	p.MoveInQueue([]int{2, 0}, 3)
	q := p.Queue()
	got := make([]string, 0, len(q))
	for _, s := range q {
		got = append(got, s.Mid)
	}
	want := []string{"b", "c", "a", "d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MoveInQueue order = %v, want %v", got, want)
	}
	if cur, _ := p.Current(); cur.Mid != "c" {
		t.Fatalf("current song moved unexpectedly: %+v", cur)
	}

	p.queue = []Song{{Mid: "a"}, {Mid: "b"}, {Mid: "c"}, {Mid: "d"}}
	p.index = 0
	p.MoveInQueue([]int{1, 1}, 3)
	q = p.Queue()
	got = got[:0]
	for _, s := range q {
		got = append(got, s.Mid)
	}
	if strings.Join(got, ",") != "a,c,b,b,d" {
		t.Fatalf("duplicate MoveInQueue order = %v, want [a c b b d]", got)
	}
}

func TestLyricAuxPriorityAndFallback(t *testing.T) {
	lines := ParseLrc(
		"[00:01.00]君の名\n[00:02.00]二行目",
		"[00:01.10]你的名字",
		"[00:01.00]kimi no na\n[00:02.10]ni gyou me",
	)
	if len(lines) != 2 {
		t.Fatalf("行数错误：%+v", lines)
	}
	if got := AuxText(lines[0], "translation"); got != "你的名字" {
		t.Errorf("翻译优先 = %q", got)
	}
	if got := AuxText(lines[0], "romanization"); got != "kimi no na" {
		t.Errorf("音译优先 = %q", got)
	}
	if got := AuxText(lines[0], "off"); got != "" {
		t.Errorf("关闭档仍有辅助行 %q", got)
	}
	// 第二行没有翻译，音译档与翻译档都必须回落音译。
	if got := AuxText(lines[1], "translation"); got != "ni gyou me" {
		t.Errorf("缺翻译时应回落音译，实得 %q", got)
	}
}

func TestQMAtmosFallbackIncludesSevenOne(t *testing.T) {
	seen := map[string]bool{}
	for _, tier := range qmAtmosFallbackTiers {
		seen[tier] = true
	}
	if !seen["atmos71"] || !seen["atmos51"] {
		t.Fatalf("Fallback Atmos 应同时包含 7.1 与 5.1，实得 %v", qmAtmosFallbackTiers)
	}
}

// TestFallbackChainRespectsAtmosToggle 锁住开关的真实语义：关闭时全景声
// 完全不进回退链；打开时才按 rank 参与。显式选中的全景声档位不受影响。
func TestFallbackChainRespectsAtmosToggle(t *testing.T) {
	off := fallbackChain("master", false)
	if len(off) == 0 || off[0] != "master" {
		t.Fatalf("回退链应从请求档位开始，实得 %v", off)
	}
	for _, tier := range off {
		if qmAtmosTier(tier) {
			t.Fatalf("关闭全景声回退后链里仍有 %s：%v", tier, off)
		}
	}

	on := fallbackChain("master", true)
	found := false
	for _, tier := range on {
		if tier == "atmos71" {
			found = true
		}
	}
	if !found {
		t.Fatalf("打开全景声回退后链里应有 atmos71：%v", on)
	}

	if explicit := fallbackChain("atmos51", true); len(explicit) == 0 || explicit[0] != "atmos51" {
		t.Fatalf("显式选择全景声时应从该档开始，实得 %v", explicit)
	}
}

// atmosDegradingBackend 模拟后端自行沿 rank 降档：请求 master 却回 atmos71。
type atmosDegradingBackend struct{ liveBackend }

func (b *atmosDegradingBackend) Resolve(mid, mediaMid string, songType int64, tier string, dep []string, durationMs int64) (*StreamInfo, error) {
	resolved := "flac"
	if tier == "master" {
		resolved = "atmos71"
	}
	return &StreamInfo{URL: "http://x/" + mid + "/" + resolved, Tier: resolved, TierLabel: resolved}, nil
}

// TestServerAtmosDegradeRejectedWhenDisabled 覆盖「后端静默降级到全景声」：
// 客户端不能把 atmos71 当成 master 的成功结果直接播放，必须继续让位。
func TestServerAtmosDegradeRejectedWhenDisabled(t *testing.T) {
	eng := &fakeEngine{}
	be := &atmosDegradingBackend{liveBackend{loggedIn: true}}
	be.tiers = &TierTable{Tiers: []Tier{
		{ID: "master", Rank: 90},
		{ID: "atmos71", Rank: 80},
		{ID: "flac", Rank: 70},
	}}
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.Boot()
	p.tiers = be.tiers
	p.PlayList([]Song{{Mid: "a", SongID: 1, SongType: 1, Interval: 100}}, 0)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := p.Stream(); st != nil && !p.Loading() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Error(); err != "" {
		t.Fatalf("降级成功后不应报错，实际: %s", err)
	}
	st := p.Stream()
	if st == nil || st.Tier != "flac" {
		t.Fatalf("应跳过后端返回的 atmos71 落到 flac，实得 %+v", st)
	}
}

func TestRadioMovesForwardOnlyAndExtendsAtRoundEnd(t *testing.T) {
	eng := &fakeEngine{}
	be := &liveBackend{guessRounds: [][]Song{
		{{Mid: "r1", SongID: 1, SongType: 1}, {Mid: "r2", SongID: 2, SongType: 1}},
		{{Mid: "r3", SongID: 3, SongType: 1}, {Mid: "r4", SongID: 4, SongType: 1}},
	}}
	p := New(be, eng, fakePrefs{})
	p.StartRadio(false)
	wait(t, func() bool { return p.RadioActive() && !p.RadioLoading() && p.QueueLen() == 2 })

	// 上一首、循环、随机与插队都不能破坏单向电台。
	if cur, _ := p.Current(); cur.Mid != "r1" {
		t.Fatalf("首轮首曲 = %q", cur.Mid)
	}
	p.Prev()
	if p.Index() != 0 {
		t.Error("电台不应允许上一首")
	}
	before := p.QueueLen()
	p.EnqueueNext(Song{Mid: "insert", SongID: 9, SongType: 1})
	if p.QueueLen() != before {
		t.Error("电台不应允许插队")
	}
	p.SetMode("one")
	p.SetShuffle(true)
	if p.Mode() != "all" || p.Shuffle() {
		t.Error("电台中循环/随机设置应被忽略")
	}

	p.Next(false)
	wait(t, func() bool {
		if cur, ok := p.Current(); ok {
			return cur.Mid == "r2"
		}
		return false
	})
	// 播到轮末：续下一轮、裁掉当前曲之前的历史，并自动落到 r3。
	p.Next(false)
	wait(t, func() bool {
		if cur, ok := p.Current(); ok {
			return cur.Mid == "r3"
		}
		return false
	})
	if p.QueueLen() != 3 {
		t.Errorf("续轮后队列应为 [当前曲+下一轮] 共 3 首，实得 %d", p.QueueLen())
	}
	if p.Index() != 1 {
		t.Errorf("续轮后当前下标 = %d，期望 1", p.Index())
	}

	// 手动换整张歌单退出电台，但当前播放队列保留。
	p.PlayList([]Song{{Mid: "normal", SongID: 99, SongType: 1}}, 0)
	if p.RadioActive() {
		t.Error("手动播放歌单后应退出电台")
	}
}

// TestSeekWhilePausedMovesPosition 钉住「暂停时拖进度条」的回归：
// 引擎已装载但暂停时心跳不再同步位置，SeekTo 必须自己把界面位置落定，
// 否则拖动松手后进度条弹回原处，要等再次播放才「跳过去」。
func TestSeekWhilePausedMovesPosition(t *testing.T) {
	eng := &fakeEngine{}
	p := New(stubBackend{}, eng, fakePrefs{})
	p.queue = []Song{{Mid: "a", SongID: 1, SongType: 1, Interval: 100}}
	p.index = 0
	p.dur = 100
	p.pos = 12
	p.stream = &StreamInfo{URL: "x"}
	p.playing = false
	eng.paused = true

	p.SeekTo(0.5)
	if got := p.Position(); got != 50 {
		t.Fatalf("暂停 seek 后界面位置 = %v，期望 50", got)
	}
	if eng.seekTo != 1 {
		t.Fatalf("引擎 SeekTo 调用次数 = %d，期望 1", eng.seekTo)
	}

	// 暂停心跳不能把位置拉回旧值（引擎可能还没来得及回报新位置）。
	p.tick()
	if got := p.Position(); got != 50 {
		t.Fatalf("暂停心跳后界面位置 = %v，期望 50", got)
	}
}

// cloudBackend 给 stubBackend 补上微云取流/歌词能力（可选接口）。
type cloudBackend struct {
	stubBackend
	gotFileID string
	gotName   string
	lyricKey  string
}

func (b *cloudBackend) ResolveCloud(mid, fileID, name string, durationMs int64) (*StreamInfo, error) {
	b.gotFileID, b.gotName = fileID, name
	return &StreamInfo{URL: "http://x/" + mid + "/weiyun", Tier: "weiyun", Token: "tok"}, nil
}

func (b *cloudBackend) FetchCloudLyric(key string) (string, string, string, error) {
	b.lyricKey = key
	return "[00:01.00]hello", "", "", nil
}

// TestCloudSongUsesCloudResolver 钉住网盘曲目的取流路径：有 CloudFileID 时
// 走 ResolveCloud（而非 QQ 音质回退链），歌词走网盘接口，音响力不上报。
func TestCloudSongUsesCloudResolver(t *testing.T) {
	eng := &fakeEngine{}
	be := &cloudBackend{}
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.tiers = &TierTable{Tiers: []Tier{{ID: "flac", Rank: 70}}}
	p.PlayList([]Song{{
		Mid: "cloud:k", SongID: 42, SongType: 1, Interval: 180,
		CloudFileID: "file123", CloudKey: "k", CloudName: "a.flac",
	}}, 0)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && p.Loading() {
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Error(); err != "" {
		t.Fatalf("网盘播放报错: %s", err)
	}
	if be.gotFileID != "file123" || be.gotName != "a.flac" {
		t.Fatalf("ResolveCloud 参数 = %q / %q", be.gotFileID, be.gotName)
	}
	if st := p.Stream(); st == nil || st.Tier != "weiyun" {
		t.Fatalf("stream = %+v", st)
	}
	if be.lyricKey != "k" {
		t.Fatalf("网盘歌词 key = %q", be.lyricKey)
	}
	p.mu.RLock()
	if p.listenTarget.SongID != 0 || p.listenTarget.StreamToken != "" {
		t.Errorf("网盘曲目不应建立音响力上报目标: %+v", p.listenTarget)
	}
	p.mu.RUnlock()
}

// TestSetEngineSwitchesBackend 验证运行时切换后端：旧引擎关闭、新引擎接手
// 音量；暂停态只保留进度，不自动起播。
func TestSetEngineSwitchesBackend(t *testing.T) {
	oldEng := &fakeEngine{}
	be := &cloudBackend{}
	p := New(be, oldEng, fakePrefs{})
	p.OnNotify(func() {})
	p.queue = []Song{{Mid: "cloud:k", CloudFileID: "f", CloudKey: "k", Interval: 100}}
	p.index = 0
	p.dur = 100
	p.pos = 42
	p.vol = 0.8
	p.playing = false

	newEng := &fakeEngine{}
	p.SetEngine(newEng)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !oldEng.closed {
		time.Sleep(5 * time.Millisecond)
	}
	if !oldEng.closed {
		t.Error("旧引擎应被关闭")
	}
	if got := p.Position(); got != 42 {
		t.Fatalf("切换后进度 = %v，期望 42", got)
	}
	if newEng.loaded != 0 {
		t.Fatalf("暂停态切换不应自动起播，OpenURL 次数 = %d", newEng.loaded)
	}
	if newEng.vol != 0.8 {
		t.Fatalf("新引擎音量 = %v，期望继承 0.8", newEng.vol)
	}
}
