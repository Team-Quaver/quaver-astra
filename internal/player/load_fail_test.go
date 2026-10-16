package player

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// 起播失败的处理规则：降档只降真打不开的档；引擎级故障就地收手；带进度起播
// 失败先退回从 0；自动跳歌有上限。这四条都是「卡在加载然后自己跳下一曲」
// 这条用户反馈的组成部分，改动时请连带看 internal/audio 的 loadfile 参数布局测试。

// masterOnlyTiers 是最小档位表：单档，降档链无处可降。
func masterOnlyTiers() *TierTable {
	return &TierTable{Tiers: []Tier{{ID: "master", Rank: 90}, {ID: "flac", Rank: 70}}}
}

func waitSettled(t *testing.T, p *Player) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !p.Loading() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("起播链没有收口：loading=%v err=%q", p.Loading(), p.Error())
}

// TestFatalEngineErrorStopsChain 引擎级故障（mpv 拒绝命令、进程没了）与音质
// 档位无关：既不该把回退链一档一档试完，更不该一首一首往下跳。
func TestFatalEngineErrorStopsChain(t *testing.T) {
	eng := &fakeEngine{fatalURL: map[string]string{
		"http://x/a/master": "播放引擎拒绝了载入命令（invalid parameter）",
	}}
	be := &liveBackend{loggedIn: true}
	be.tiers = masterOnlyTiers()
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.Boot()
	p.tiers = be.tiers

	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 100},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 100},
		{Mid: "c", SongID: 3, SongType: 1, Interval: 100},
	}, 0)
	waitSettled(t, p)

	if n := eng.loadedCount(); n != 1 {
		t.Errorf("引擎级故障只该载入一次，实际 %d 次（降档链没刹住）", n)
	}
	if idx := p.Index(); idx != 0 {
		t.Errorf("不该自动跳歌，index=%d", idx)
	}
	if p.Error() == "" {
		t.Error("应把失败原因报给用户")
	} else if !strings.Contains(p.Error(), "与音质档位无关") {
		t.Errorf("错误应说明与档位无关，实际 %q", p.Error())
	}
	if st := p.Stream(); st != nil {
		t.Errorf("失败后不该留着可播的流：%+v", st)
	}
	if p.Loading() {
		t.Error("失败后 loading 应归位")
	}
}

// TestAutoSkipStopsAfterMaxSkips 连续播不了的队列最多自动跳 maxAutoSkips 首，
// 然后停在当前曲报错。不加这个上限，一次网络故障会把整条队列「自己跳完」——
// 正是用户看到的「卡在加载，然后一首接一首往下跳」。
func TestAutoSkipStopsAfterMaxSkips(t *testing.T) {
	const songs = 10
	// 每一档都打不开（下架/无版权这类），才轮得到「自动跳歌上限」这条刹车
	eng := &fakeEngine{openErr: func(string, float64) (string, bool) {
		return "mpv 载入失败", false
	}}
	queue := make([]Song, 0, songs)
	for i := 0; i < songs; i++ {
		queue = append(queue, Song{Mid: fmt.Sprintf("s%d", i), SongID: int64(i + 1), SongType: 1, Interval: 100})
	}
	be := &liveBackend{loggedIn: true}
	be.tiers = masterOnlyTiers()
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.Boot()
	p.tiers = be.tiers

	p.PlayList(queue, 0)
	waitSettled(t, p)

	if idx := p.Index(); idx != maxAutoSkips {
		t.Errorf("应停在第 %d 首收手，实际 index=%d", maxAutoSkips, idx)
	}
	if !strings.Contains(p.Error(), "停止自动切歌") {
		t.Errorf("错误应说明已停止自动切歌，实际 %q", p.Error())
	}
	// 收手之后再等一会儿：自动跳歌必须真的停了
	before := p.Index()
	time.Sleep(300 * time.Millisecond)
	if p.Index() != before {
		t.Errorf("收手后还在自己跳歌：%d → %d", before, p.Index())
	}
	// 只试到第 maxAutoSkips+1 首（每首一整条回退链），后面的歌没被牵连
	if n, want := eng.loadedCount(), len(fallbackChain("master"))*(maxAutoSkips+1); n > want {
		t.Errorf("载入次数 %d 超过了 4 首歌的回退链总量 %d", n, want)
	}
}

// TestResumeLoadFallsBackToZero 带进度起播失败时，先用同一档位退回从 0 再试，
// 而不是马上降档、更不是直接跳下一曲：上次会话的进度、这一档不支持 Range，
// 都只该让「续播」这一下失败——从 0 播是用户完全能接受的结果。
func TestResumeLoadFallsBackToZero(t *testing.T) {
	eng := &fakeEngine{}
	be := &liveBackend{loggedIn: true}
	be.tiers = &TierTable{Tiers: []Tier{{ID: "master", Rank: 90}}}
	p := New(be, eng, fakePrefs{})
	p.OnNotify(func() {})
	p.Boot()
	p.tiers = be.tiers

	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 60},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 60},
	}, 0)
	waitSettled(t, p)
	if p.Error() != "" {
		t.Fatalf("首档起播应成功：%q", p.Error())
	}

	// 拖到中间，等心跳把位置从引擎同步回来（界面进度就是这么走的），
	// 然后换成「凡是带进度起播都失败」的引擎，再按换音质那条路重起
	p.SeekTo(0.5)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && p.Position() < 20 {
		time.Sleep(10 * time.Millisecond)
	}
	if pos := p.Position(); pos < 20 {
		t.Fatalf("进度没同步回来，测不到带进度起播：pos=%v", pos)
	}
	eng.openErr = func(_ string, startFrac float64) (string, bool) {
		if startFrac > 0 {
			return "mpv 载入失败", false
		}
		return "", false
	}
	p.RestartAtPosition()
	waitSettled(t, p)

	if p.Error() != "" {
		t.Fatalf("退回从 0 起播应成功：%q", p.Error())
	}
	if idx := p.Index(); idx != 0 {
		t.Errorf("不该跳到下一曲，index=%d", idx)
	}
	if st := p.Stream(); st == nil || st.Tier != "master" {
		t.Errorf("档位不该因为进度问题被降掉：%+v", st)
	}
	eng.mu.Lock()
	last := append([]float64{}, eng.starts[max(0, len(eng.starts)-2):]...)
	eng.mu.Unlock()
	if len(last) != 2 || last[0] <= 0 || last[1] != 0 {
		t.Errorf("应先用带进度的起点失败、再从 0 重试，实际 starts=%v", last)
	}
}

// likedStub 只关心「我喜欢」的分页拉取，其余后端能力用不到。
//
// LoadLoved 在后台 goroutine 里调它，测试主线程又要在中途改剧本、读次数，
// 所以内部一律加锁（-race 抓到过这里的裸 map 竞争）。
type likedStub struct {
	Backend
	mu    sync.Mutex
	pages map[int][]Song
	fail  map[int]int // page → 前 N 次请求失败（模拟冷启动抖动/持续失败）
	calls map[int]int
}

func (s *likedStub) LikedPage(page, num int) ([]Song, int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	s.calls[page]++
	if s.fail[page] >= s.calls[page] {
		return nil, 0, false, fmt.Errorf("第 %d 页拉取失败", page)
	}
	songs := s.pages[page]
	total := int64(0)
	for _, pg := range s.pages {
		total += int64(len(pg))
	}
	return songs, total, len(s.pages[page+1]) > 0, nil
}

func (s *likedStub) LoginStatus() (bool, error) { return true, nil }

// setScript 换一页的数据并设定「该页前 N 次请求失败」（N=0 表示不再失败）。
func (s *likedStub) setScript(page int, songs []Song, failFirst int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	s.pages[page] = songs
	s.fail[page] = failFirst
}

// ensure 让三张表在任何调用顺序下都可用（测试里用字面量构造，难免漏一张）。
func (s *likedStub) ensure() {
	if s.pages == nil {
		s.pages = map[int][]Song{}
	}
	if s.fail == nil {
		s.fail = map[int]int{}
	}
	if s.calls == nil {
		s.calls = map[int]int{}
	}
}

// callCount 读某一页被请求了几次。
func (s *likedStub) callCount(page int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[page]
}

// TestLoadLovedPartialPage 第 2 页失败时第 1 页照样落地：满屏红心不能因为
// 后半程没拉成就一起消失（看起来就是「红心渲染不出来」）。
func TestLoadLovedPartialPage(t *testing.T) {
	stub := &likedStub{
		pages: map[int][]Song{
			1: {{Mid: "l1", SongID: 11, SongType: 1}, {Mid: "l2", SongID: 12, SongType: 1}},
			2: {{Mid: "l3", SongID: 13, SongType: 1}},
		},
		fail:  map[int]int{2: 99}, // 第 2 页永远失败
		calls: map[int]int{},
	}
	p := New(stub, &fakeEngine{}, fakePrefs{})
	p.OnNotify(func() {})

	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !p.IsLoved("l1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.IsLoved("l1") || !p.IsLoved("l2") {
		t.Fatalf("第 1 页应落地，loved=%v", p.LikedCache())
	}
	if p.IsLoved("l3") {
		t.Error("第 2 页没拉到，不该有 l3")
	}
	if n := p.LikedTotal(); n != 3 {
		t.Errorf("总数应取后端报的 3，实际 %d", n)
	}
}

// TestLoadLovedKeepsOldOnTotalFailure 第 1 页重试后仍失败时保留上一次的结果，
// 成功过一次之后再次全失败也不该把红心清空。
func TestLoadLovedKeepsOldOnTotalFailure(t *testing.T) {
	old := likedRetryDelay
	likedRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { likedRetryDelay = old })

	stub := &likedStub{
		pages: map[int][]Song{1: {{Mid: "l1", SongID: 11, SongType: 1}}},
		calls: map[int]int{},
	}
	p := New(stub, &fakeEngine{}, fakePrefs{})
	p.OnNotify(func() {})

	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !p.IsLoved("l1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.IsLoved("l1") {
		t.Fatal("首轮就该拉到我喜欢")
	}

	// 第 1 页从此全失败：集合必须原样保留
	stub.setScript(1, nil, 99) // 从此第 1 页全失败
	p.LoadLoved()
	time.Sleep(300 * time.Millisecond)
	if !p.IsLoved("l1") {
		t.Error("拉取失败不该把已知的红心抹掉")
	}
	if n := stub.callCount(1); n < 2 {
		t.Errorf("第 1 页失败后应重试，实际只调了 %d 次", n)
	}
}

// TestLoadLovedRetriesTransientFailure 冷启动时后端还没就绪抖一下，重试一次
// 就该把红心拉齐（而不是整轮放弃）。
func TestLoadLovedRetriesTransientFailure(t *testing.T) {
	old := likedRetryDelay
	likedRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { likedRetryDelay = old })

	stub := &likedStub{
		pages: map[int][]Song{1: {{Mid: "l1", SongID: 11, SongType: 1}}},
		fail:  map[int]int{1: 1}, // 只失败第一次
		calls: map[int]int{},
	}
	p := New(stub, &fakeEngine{}, fakePrefs{})
	p.OnNotify(func() {})

	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !p.IsLoved("l1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.IsLoved("l1") {
		t.Fatalf("第 1 页抖动后应重试成功，实际请求 %d 次", stub.callCount(1))
	}
}
