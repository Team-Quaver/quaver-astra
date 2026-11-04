package appui

import (
	"fmt"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// seekEngine 是可写播放位置的假引擎：Paused 恒为 false，心跳才会把
// Position 同步进 Player。心跳在别的 goroutine 读位置，测试在 UI 线程改，
// 所以位置带锁。
type seekEngine struct {
	loadEngine
	mu  sync.Mutex
	pos float64
}

func (e *seekEngine) Position() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pos
}

func (e *seekEngine) Paused() bool { return false }

func (e *seekEngine) SeekTo(frac, dur float64) {
	e.mu.Lock()
	e.pos = frac * dur
	e.mu.Unlock()
}

// set 把位置挪到秒数 t，模拟播放推进到另一句。
func (e *seekEngine) set(t float64) {
	e.mu.Lock()
	e.pos = t
	e.mu.Unlock()
}

// TestLyricBlurTargetsNonCurrentLines 守住歌词模糊的取舍：当前句清晰、
// 其余句模糊；用户滚动后的 3s 静默期里整片都不模糊。
func TestLyricBlurTargetsNonCurrentLines(t *testing.T) {
	app := &App{}
	now := time.Now()
	if got := app.lyricLineBlur(true, now); got != 0 {
		t.Errorf("当前句模糊量 = %v，期望 0", got)
	}
	if got := app.lyricLineBlur(false, now); got != npLyricBlur {
		t.Errorf("非当前句模糊量 = %v，期望 %v", got, npLyricBlur)
	}

	app.npFollowPause = now.Add(lyricFollowPause)
	if got := app.lyricLineBlur(false, now); got != 0 {
		t.Errorf("滚动静默期内非当前句模糊量 = %v，期望 0（正在找歌词，不能糊）", got)
	}
	if got := app.lyricLineBlur(false, now.Add(lyricFollowPause)); got != npLyricBlur {
		t.Errorf("静默期结束后模糊量 = %v，期望恢复 %v", got, npLyricBlur)
	}
}

// TestLyricFollowGlides 守住换行跟随是「缓动滚过去」而不是瞬间跳位：
// 当前句变了之后，列表偏移应当从原位逐步走到目标，走完才停下。
func TestLyricFollowGlides(t *testing.T) {
	var qrc strings.Builder
	qrc.WriteString(`<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1"><Lyric_1 LyricType="1" Lyrics="`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&qrc, "&#10;[%d,1000]第%d句", (i+1)*1000, i+1)
	}
	qrc.WriteString(`" /></LyricInfo></QrcInfos>`)

	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, qrc.String(), "", eng)
	// 心跳（200ms）把假引擎位置同步进 Player；等到第 4 句真正成为当前句。
	app.PL.StartTicker()
	deadline := time.Now().Add(3 * time.Second)
	for app.PL.LyricIndex(app.PL.Position()) != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("播放位置未推进（pos=%.2f）", app.PL.Position())
		}
		time.Sleep(5 * time.Millisecond)
	}
	tester.Frame()

	if app.npFollow.pending {
		t.Fatal("当前句已经在视图里，不该退回瞬间跳转")
	}
	if !app.npFollow.active {
		t.Fatal("换行没有开始跟随动画")
	}
	from, to := app.npScroll.Y, app.npFollow.to
	if to <= from {
		t.Fatalf("目标偏移 %.1f 不大于起点 %.1f", to, from)
	}

	// 走到一半：偏移应当落在两端之间，而不是已经在终点。
	// Advance 会按刷新率把动画帧跑完，不必再调 Frame——动画在跑时
	// Frame 会在冻结的时钟上重复渲染最多 20 帧，测试会慢一个数量级。
	tester.Advance(160 * time.Millisecond)
	mid := app.npScroll.Y
	if mid <= from || mid >= app.npFollow.to {
		t.Fatalf("动画中途偏移 %.1f 不在 (%.1f, %.1f) 之间", mid, from, app.npFollow.to)
	}

	// 动画时长 320ms：再推进一段后应当停在终点。
	tester.Advance(300 * time.Millisecond)
	if app.npFollow.active {
		t.Fatal("动画超时仍未停下")
	}
	if got := app.npScroll.Y; got != app.npFollow.to {
		t.Fatalf("停下时偏移 %.1f，不等于目标 %.1f", got, app.npFollow.to)
	}
}

// TestLyricFollowTargetIsStable 守住跟随目标不来回摆。
//
// 一帧里视图会建不止一趟，而 LayoutBounds 在布局前一直是同一份。若每趟
// build 都拿「本趟已推进的偏移」去当布局时的偏移，目标会被修正成另一个
// 值，下一趟再修回来：偏移一帧往前一帧往后，就是用户看到的「歌词抽搐」。
// 目标只应在行高变化时修正一次，之后保持不变。
func TestLyricFollowTargetIsStable(t *testing.T) {
	var qrc strings.Builder
	qrc.WriteString(`<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1"><Lyric_1 LyricType="1" Lyrics="`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&qrc, "&#10;[%d,1000]第%d句", (i+1)*1000, i+1)
	}
	qrc.WriteString(`" /></LyricInfo></QrcInfos>`)

	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, qrc.String(), "", eng)
	app.PL.StartTicker()
	deadline := time.Now().Add(3 * time.Second)
	for app.PL.LyricIndex(app.PL.Position()) != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("播放位置未推进（pos=%.2f）", app.PL.Position())
		}
		time.Sleep(5 * time.Millisecond)
	}
	tester.Frame()
	if !app.npFollow.active {
		t.Fatal("换行没有开始跟随动画")
	}
	want := app.npFollow.to
	prev := app.npScroll.Y
	for i := 0; i < 12 && app.npFollow.active; i++ {
		tester.Advance(16 * time.Millisecond)
		if !app.npFollow.active {
			break
		}
		if app.npFollow.to != want {
			t.Fatalf("第 %d 帧目标从 %.2f 变成 %.2f：跟随目标在来回摆", i, want, app.npFollow.to)
		}
		if app.npScroll.Y < prev-0.01 {
			t.Fatalf("第 %d 帧偏移 %.2f 比上一帧 %.2f 还小：动画倒退", i, app.npScroll.Y, prev)
		}
		prev = app.npScroll.Y
	}
}

// lyricFollowQRC 生成 n 句、每句一秒的 QRC，测试滚动/换行用。
func lyricFollowQRC(n int) string {
	var qrc strings.Builder
	qrc.WriteString(`<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1"><Lyric_1 LyricType="1" Lyrics="`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&qrc, "&#10;[%d,1000]第%d句", (i+1)*1000, i+1)
	}
	qrc.WriteString(`" /></LyricInfo></QrcInfos>`)
	return qrc.String()
}

// waitLyricIndex 等心跳把假引擎的位置同步进 Player，直到 want 成为当前句。
func waitLyricIndex(t *testing.T, app *App, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for app.PL.LyricIndex(app.PL.Position()) != want {
		if time.Now().After(deadline) {
			t.Fatalf("播放位置未推进到第 %d 句（pos=%.2f）", want, app.PL.Position())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settleLyricFollow 把还在路上的跟随动画走完，让测试从静止状态开始。
func settleLyricFollow(t *testing.T, app *App, tester *ui.Tester) {
	t.Helper()
	for i := 0; i < 60 && app.npFollow.active; i++ {
		tester.Advance(16 * time.Millisecond)
	}
	if app.npFollow.active || app.npFollow.pending {
		t.Fatal("跟随动画没有停下")
	}
}

// TestLyricFollowPausesWhileUserScrolls 守住「用户滚动时别抢方向盘」：
// 用户滚过歌词列表后 3 秒内换行不自动跟随（否则正在找/点的歌词会被拽走），
// 停手 3 秒后再缓动回当前句。
func TestLyricFollowPausesWhileUserScrolls(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	settleLyricFollow(t, app, tester)

	before := app.npScroll.Y
	// 在歌词列上滚一格（触控板/滚轮都走 InputScroll）。
	tester.Scroll(700, 350, 0, 140)
	tester.Frame()
	if !app.lyricFollowPaused(tester.Now()) {
		t.Fatal("滚过歌词列表后没有暂停跟随")
	}
	if app.npFollow.active || app.npFollow.pending {
		t.Fatal("滚动后跟随动画还在跑")
	}
	afterScroll := app.npScroll.Y
	if afterScroll == before {
		t.Fatalf("滚动没有改变列表偏移（仍为 %.1f）", before)
	}

	// 暂停期间当前句前进：列表必须停在用户放下的地方。
	eng.set(6.5)
	waitLyricIndex(t, app, 5)
	tester.Frame()
	if app.npFollow.active || app.npFollow.pending {
		t.Fatal("暂停期间换行仍把列表拽走了")
	}
	if got := app.npScroll.Y; got != afterScroll {
		t.Fatalf("暂停期间偏移从 %.1f 变成了 %.1f", afterScroll, got)
	}

	// 停手 3 秒后自动回到当前句。
	tester.Advance(3 * time.Second)
	tester.Frame()
	if app.lyricFollowPaused(tester.Now()) {
		t.Fatal("静默期结束后仍判定为暂停")
	}
	for i := 0; i < 40 && app.npFollow.active; i++ {
		tester.Advance(16 * time.Millisecond)
	}
	if got := app.npScroll.Y; got == afterScroll {
		t.Fatal("静默期结束后没有把列表挪回当前句")
	}
}

// TestLyricFollowStaysResumedAfterScroll 守住复位之后不再被重新判成用户
// 滚动：跟随自己写偏移、列表布局再校正，若把这点差值当成滚动，3s 静默期
// 会被反复续上，歌词模糊就会周期性地闪。
func TestLyricFollowStaysResumedAfterScroll(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	settleLyricFollow(t, app, tester)

	tester.Scroll(700, 350, 0, 140)
	tester.Frame()
	if !app.lyricFollowPaused(tester.Now()) {
		t.Fatal("滚远后没有暂停跟随")
	}

	tester.Advance(3 * time.Second)
	tester.Frame()
	// 让瞬移淡入与跟随动画都走完，再多跑几秒。
	for i := 0; i < 400; i++ {
		// 动画在跑时 Advance 本身就会逐帧渲染；动画停下后没有帧可跑，
		// 也就不会有新的偏移校正，这里静默推进时钟即可。
		tester.Advance(16 * time.Millisecond)
		if app.lyricFollowPaused(tester.Now()) {
			t.Fatalf("复位后第 %d 帧又被判成用户滚动（偏移 %.1f，上帧 %.1f）",
				i, app.npScroll.Y, app.npScrollSeen)
		}
	}
}

func TestDebugBlurFlicker(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	settleLyricFollow(t, app, tester)

	tester.Scroll(700, 350, 0, 140)
	tester.Frame()
	tester.Advance(3 * time.Second)
	tester.Frame()

	latest := map[int]float32{}
	curFlag := map[int]bool{}
	debugBlurHook = func(i int, cur bool, v float32) {
		latest[i] = v
		curFlag[i] = cur
	}
	defer func() { debugBlurHook = nil }()

	prev := map[int]float32{}
	changes := map[int]int{}
	for f := 0; f < 400; f++ {
		tester.Advance(16 * time.Millisecond)
		if f < 120 {
			prev = map[int]float32{}
			for i, v := range latest {
				prev[i] = v
			}
			continue
		}
		for i, v := range latest {
			if p, ok := prev[i]; ok && (v-p > 0.02 || p-v > 0.02) {
				changes[i]++
			}
			prev[i] = v
		}
	}
	for i, n := range changes {
		if n > 2 {
			t.Logf("line %d changed %d times in 280 frames (cur=%v last=%.3f)", i, n, curFlag[i], latest[i])
		}
	}
}

// TestLyricFollowPausesOnScrollbarDrag 守住拖动滚动条也暂停：拖动不经过
// 元素事件，只能靠帧与帧之间的偏移差认出来。
func TestLyricFollowPausesOnScrollbarDrag(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	settleLyricFollow(t, app, tester)

	before := app.npScroll.Y
	// 引擎在下一帧建视图前改掉偏移，正如拖动滚动条拇指。
	app.npScroll.Y += 96
	tester.Frame()
	if !app.lyricFollowPaused(tester.Now()) {
		t.Fatal("拖动滚动条后没有暂停跟随")
	}
	if app.npScroll.Y == before {
		t.Fatal("模拟的拖动没有改变偏移")
	}
}

// TestLyricFollowIgnoresLayoutCorrection 守住跟随动画不会被布局校正误判成
// 用户滚动：列表布局会把动画写进去的偏移再校正一点，若把这点差值当用户
// 滚动，动画刚起步就被取消，当前句只会一路往下走、不回正中。
func TestLyricFollowIgnoresLayoutCorrection(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	if !app.npFollow.active && !app.npFollow.pending {
		t.Fatal("换行后没有开始跟随")
	}
	// 模拟列表布局对动画偏移的校正（真实布局里由 TrackScroll 写回）。
	app.npScroll.Y += 4
	tester.Frame()
	if app.lyricFollowPaused(tester.Now()) {
		t.Fatal("布局校正被误判成用户滚动，跟随被暂停")
	}
	if !app.npFollow.active && !app.npFollow.pending {
		t.Fatal("跟随被布局校正打断了")
	}
}

// TestLyricFollowFadesBackWhenFarAway 守住滚远之后回来不是硬切：当前句
// 不在已建的行里时只能瞬移，但这一跳要淡入，而不是直接把画面换掉。
func TestLyricFollowFadesBackWhenFarAway(t *testing.T) {
	eng := &seekEngine{pos: 4.5}
	app, tester := npTestAppEngine(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, lyricFollowQRC(40), "", eng)
	app.PL.StartTicker()
	waitLyricIndex(t, app, 3)
	tester.Frame()
	settleLyricFollow(t, app, tester)

	// 滚到列表另一头：当前句早已不在构建出来的行里。
	tester.Scroll(700, 350, 0, 2400)
	tester.Frame()
	if !app.lyricFollowPaused(tester.Now()) {
		t.Fatal("滚远后没有暂停跟随")
	}
	far := app.npScroll.Y

	tester.Advance(3 * time.Second)
	tester.Frame()
	if app.npSnapAt.IsZero() {
		t.Fatal("回到当前句的瞬移没有淡入")
	}
	if app.npScroll.Y == far {
		t.Fatal("静默期结束后没有回到当前句")
	}

	// 淡入 220ms 后收干净，不留半透明。
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	if !app.npSnapAt.IsZero() {
		t.Fatal("淡入没有结束")
	}
}

// TestLyricFollowResetsOnSongChange 守住换曲时清空跟随：上一首滚到的位置
// 和还在跑的动画不能带进新歌，否则新歌第一句会把列表从深处拽回顶部。
func TestLyricFollowResetsOnSongChange(t *testing.T) {
	app := &App{lastLyricLine: 9}
	app.npScroll.Y = 320
	app.npFollow = lyricFollow{active: true, line: 9, from: 100, to: 320}

	app.resetLyricFollowOnSongChange("m1")
	if app.npSongKey != "m1" {
		t.Fatalf("曲目键未记下：%q", app.npSongKey)
	}
	if app.npScroll.Y != 0 {
		t.Fatalf("换曲后偏移应为 0，实得 %.1f", app.npScroll.Y)
	}
	if app.npFollow.active || app.npFollow.pending {
		t.Fatal("换曲后不该留着上一首的跟随动画")
	}
	if app.lastLyricLine != -2 {
		t.Fatalf("换曲后当前行号应重置，实得 %d", app.lastLyricLine)
	}

	// 同一首歌里继续播放（例如重开正在播放页）不能被当成换曲。
	app.npScroll.Y = 42
	app.npFollow = lyricFollow{active: true, line: 4}
	app.resetLyricFollowOnSongChange("m1")
	if app.npScroll.Y != 42 || !app.npFollow.active {
		t.Fatal("同一首歌不该重置歌词跟随")
	}
}
