package appui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/streaminfo"

	"github.com/egoist/mygo/ui"
)

// ===== 正在播放页浮层（⋮ 菜单 / 音频流信息）测试夹具 =====

// npMenuApp 渲染一张「带两个歌手 + 专辑」的正在播放页。
// qrc 传 testQRCXML 时等逐字歌词就位（菜单的歌词大小行要有歌词才出现）；
// 传 "" 则是无歌词的场景。
func npMenuApp(t *testing.T, qrc string) (*App, *player.Player, *ui.Tester) {
	t.Helper()
	p := player.New(npBackend{qrc: qrc}, loadEngine{}, &nullPrefs{})
	p.PlayList([]player.Song{{
		Mid: "m1", Name: "晴天", Title: "晴天", Artists: "周杰伦 · 方文山",
		Album: "叶惠美", AlbumMid: "al1", AlbumPmid: "004NclXP3Equ1W", Interval: 269,
		Singers: []player.Singer{
			{Mid: "s1", Name: "周杰伦"},
			{Mid: "s2", Name: "方文山"},
		},
	}}, 0)
	if qrc != "" {
		deadline := time.Now().Add(3 * time.Second)
		for {
			if lines, st := p.LyricLines(); st == "ok" && len(lines) > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("假歌词未就绪")
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	// 封面预置纯色块，免得 CoverCache 真去拉图。
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成封面失败：%v", err)
	}
	covers := NewCoverCache()
	for _, size := range []int{300, 500} {
		covers.PutRaw(coverURL("004NclXP3Equ1W", "al1", size), buf.Bytes())
	}

	app := &App{
		PL:            p,
		API:           backend.NewClient("http://127.0.0.1:1"),
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/daily"),
		Covers:        covers,
		npOpen:        true,
		probedMid:     "m1", // 跳过封面取色：否则会异步拉图并改强调色
		daily:         songListState{loaded: true},
		singers:       map[string]*singerState{},
		albums:        map[string]*albumState{},
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	// 开门动画（上滑 + 淡入）落定再交互，否则按钮还在移动中，点不准。
	time.Sleep(450 * time.Millisecond)
	tester.Frame()
	return app, p, tester
}

// reopenNP 重新打开正在播放页并等动画落定（跳转后浮层/页面都被收起）。
func reopenNP(t *testing.T, app *App, tester *ui.Tester) {
	t.Helper()
	app.npOpen = true
	tester.Frame()
	time.Sleep(450 * time.Millisecond)
	tester.Frame()
}

// clickNP 打开 ⋮ 菜单后点一项。
func clickNP(t *testing.T, app *App, tester *ui.Tester, item string) {
	t.Helper()
	if err := tester.Click("更多操作"); err != nil {
		t.Fatalf("打不开 ⋮ 菜单：%v", err)
	}
	tester.Frame()
	if !app.npMoreOpen {
		t.Fatal("⋮ 菜单没开")
	}
	if err := tester.Click(item); err != nil {
		t.Fatalf("点不到菜单项 %q：%v", item, err)
	}
	tester.Frame()
}

// ===== ⋮ 菜单 =====

// TestNowPlayingMoreMenuStructure 钉住菜单结构（对齐主项目 np-menu）：
// 同名搜索 / 跳转歌手（多歌手逐项）/ 跳转专辑 / 翻译 / 歌词大小。
// 旧 ContextMenu 的「显示翻译」不得回来。
func TestNowPlayingMoreMenuStructure(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)

	if err := tester.Click("更多操作"); err != nil {
		t.Fatalf("点不到 ⋮ 按钮：%v", err)
	}
	tester.Frame()
	if !app.npMoreOpen {
		t.Fatal("⋮ 菜单没开")
	}
	for _, want := range []string{
		"同名搜索", "跳转歌手：周杰伦", "跳转歌手：方文山", "跳转专辑", "翻译", "歌词大小", "100%",
	} {
		if !tester.HasText(want) {
			t.Errorf("菜单缺少 %q", want)
		}
	}
	if tester.HasText("显示翻译") {
		t.Error("旧右键菜单的「显示翻译」不该回来")
	}
	if tester.HasText("自动（最高可播）") {
		t.Error("⋮ 菜单里混进了音质切换菜单的内容")
	}
}

// TestNowPlayingMoreMenuNoLyricsHidesSizeRow：没有歌词就没有「歌词大小」行
// （调了也没东西可调）。
func TestNowPlayingMoreMenuNoLyricsHidesSizeRow(t *testing.T) {
	_, _, tester := npMenuApp(t, "")

	if err := tester.Click("更多操作"); err != nil {
		t.Fatalf("点不到 ⋮ 按钮：%v", err)
	}
	tester.Frame()
	if tester.HasText("歌词大小") {
		t.Error("无歌词时不该出现「歌词大小」行")
	}
	if !tester.HasText("翻译") {
		t.Error("翻译开关行应该照旧在")
	}
}

// TestNowPlayingMoreMenuJumps 钉住三组跳转：跳转前收起正在播放页（它盖在
// 路由页上面，不收起换了路由也看不见），浮层状态一并复位。
func TestNowPlayingMoreMenuJumps(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)
	// 目标页预置成已加载：跳转不该触发网络请求（测试后端必然拒连，
	// 回填协程会跟后续帧抢状态）。
	app.search.kw = "晴天"
	seedSinger(app)
	app.singers["s2"] = &singerState{loaded: true, name: "方文山"}
	seedAlbum(app)

	clickNP(t, app, tester, "同名搜索")
	if got := app.Router.Path(); got != "/search" {
		t.Errorf("同名搜索后 path = %q，期望 /search", got)
	}
	if got := app.Router.Query("kw"); got != "晴天" {
		t.Errorf("同名搜索 kw = %q，期望 晴天", got)
	}
	if app.npOpen || app.npMoreOpen {
		t.Error("跳转前应把正在播放页与菜单都收起")
	}

	reopenNP(t, app, tester)
	clickNP(t, app, tester, "跳转歌手：方文山")
	if got := app.Router.Path(); got != "/singer/s2" {
		t.Errorf("跳转歌手后 path = %q，期望 /singer/s2", got)
	}
	if got := app.Router.Query("name"); got != "方文山" {
		t.Errorf("跳转歌手 name = %q，期望 方文山", got)
	}

	reopenNP(t, app, tester)
	clickNP(t, app, tester, "跳转专辑")
	if got := app.Router.Path(); got != "/album/al1" {
		t.Errorf("跳转专辑后 path = %q，期望 /album/al1", got)
	}
	if got := app.Router.Query("name"); got != "叶惠美" {
		t.Errorf("跳转专辑 name = %q，期望 叶惠美", got)
	}
}

// TestNowPlayingMoreMenuTranslation 翻译行整行可点，点一次翻一次。
func TestNowPlayingMoreMenuTranslation(t *testing.T) {
	_, p, tester := npMenuApp(t, testQRCXML)

	if err := tester.Click("更多操作"); err != nil {
		t.Fatalf("点不到 ⋮ 按钮：%v", err)
	}
	tester.Frame()
	if err := tester.Click("翻译"); err != nil {
		t.Fatalf("点不到翻译行：%v", err)
	}
	tester.Frame()
	if !p.ShowTranslation() {
		t.Error("点翻译行后应开启翻译")
	}
	if err := tester.Click("翻译"); err != nil {
		t.Fatalf("点不到翻译行：%v", err)
	}
	tester.Frame()
	if p.ShowTranslation() {
		t.Error("再点翻译行应关闭翻译")
	}
}

// TestNowPlayingMoreMenuLyricSize 歌词大小步进写 Style.LyricScale（整数百分比），
// 读数跟着走，到顶/到底不再动。
func TestNowPlayingMoreMenuLyricSize(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)

	if err := tester.Click("更多操作"); err != nil {
		t.Fatalf("点不到 ⋮ 按钮：%v", err)
	}
	tester.Frame()
	r, ok := tester.Find("歌词大小调节")
	if !ok {
		t.Fatal("找不到歌词大小步进器")
	}
	up := func() {
		tester.ClickAt(r.X+r.W/2, r.Y+r.H*0.25)
		tester.Frame()
	}
	down := func() {
		tester.ClickAt(r.X+r.W/2, r.Y+r.H*0.75)
		tester.Frame()
	}

	up()
	if !tester.HasText("110%") {
		t.Errorf("放大一档后读数应为 110%%，帧内文本：%v", tester.Texts())
	}
	if got := app.Conf.Float("Style.LyricScale", 0); got != 110 {
		t.Errorf("Style.LyricScale = %v，期望 110", got)
	}
	down()
	down()
	if !tester.HasText("90%") {
		t.Errorf("缩小两档后读数应为 90%%，帧内文本：%v", tester.Texts())
	}
	// 一路放大到顶：0.7–1.5 的上界是 150%。
	for range 8 {
		up()
	}
	if !tester.HasText("150%") {
		t.Errorf("读数应停在 150%%，帧内文本：%v", tester.Texts())
	}
	if got := app.Conf.Float("Style.LyricScale", 0); got != 150 {
		t.Errorf("Style.LyricScale = %v，期望封顶 150", got)
	}
}

// ===== 音质胶囊：只读音频流信息浮窗 =====

// TestNowPlayingQualityInfoRows 缓存命中：浮窗直接给参数行 + 档位徽标，
// 并且是只读的——切档菜单（自动（最高可播）…）不得出现。
func TestNowPlayingQualityInfoRows(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)
	app.npQInfo.seed("test://stream", &streaminfo.Info{
		Codec: "FLAC", SampleRate: 48000, BitDepth: 24, Bitrate: 1411, Channels: 2,
	})

	if err := tester.Click("音质"); err != nil {
		t.Fatalf("点不到音质胶囊：%v", err)
	}
	tester.Frame()
	if !app.npQInfoOpen {
		t.Fatal("音质胶囊浮窗没开")
	}
	if app.qualityOpen {
		t.Error("音质胶囊浮窗不该动播放条的切档状态")
	}
	if tester.HasText("自动（最高可播）") {
		t.Error("音质胶囊浮窗里不该出现音质切换项")
	}
	for _, want := range []string{
		"音频流信息", "FLAC", "编码格式", "48 kHz", "24 bit", "1411 kbps", "2（立体声）",
	} {
		if !tester.HasText(want) {
			t.Errorf("流信息浮窗缺少 %q", want)
		}
	}
}

// TestNowPlayingQualityInfoPending 没缓存：先「探测中…」，探测回来落参数行。
func TestNowPlayingQualityInfoPending(t *testing.T) {
	_, _, tester := npMenuApp(t, testQRCXML)

	release := make(chan struct{})
	called := make(chan struct{})
	var once sync.Once
	old := streamFetch
	streamFetch = func(string, int64, float64) *streaminfo.Info {
		once.Do(func() { close(called) })
		<-release
		return &streaminfo.Info{Codec: "MP3", SampleRate: 44100, Bitrate: 320, Channels: 2}
	}
	t.Cleanup(func() {
		streamFetch = old
		select {
		case <-release:
		default:
			close(release)
		}
	})

	if err := tester.Click("音质"); err != nil {
		t.Fatalf("点不到音质胶囊：%v", err)
	}
	tester.Frame()
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("流参数探测没跑起来")
	}
	if !tester.HasText("探测中…") {
		t.Error("探测未完成时应显示「探测中…」")
	}
	if tester.HasText("编码格式") {
		t.Error("探测中不该已有参数行")
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for !tester.HasText("编码格式") {
		if time.Now().After(deadline) {
			t.Fatalf("探测完成后参数行没出来，帧内文本：%v", tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
		tester.Frame()
	}
	if !tester.HasText("44.1 kHz") || !tester.HasText("320 kbps") {
		t.Errorf("参数行不对，帧内文本：%v", tester.Texts())
	}
}

// TestNowPlayingQualityInfoUnavailable 探测不出东西（未知容器/取不到头）：
// 流信息不可用。
func TestNowPlayingQualityInfoUnavailable(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)
	app.npQInfo.seed("test://stream", nil)

	if err := tester.Click("音质"); err != nil {
		t.Fatalf("点不到音质胶囊：%v", err)
	}
	tester.Frame()
	if !tester.HasText("流信息不可用") {
		t.Errorf("探测失败应显示「流信息不可用」，帧内文本：%v", tester.Texts())
	}
}

// TestNowPlayingQualityPillIndependentFromPlayerBar 守住「正在播放页的音质胶囊
// 与播放控制条互相独立」：正在播放页只展示流参数，切档仍只在播放条上。
func TestNowPlayingQualityPillIndependentFromPlayerBar(t *testing.T) {
	app, _, tester := npMenuApp(t, testQRCXML)
	app.npQInfo.seed("test://stream", nil)

	// 正在播放页：开的是流信息浮窗，播放条的切档状态不该动。
	if err := tester.Click("音质"); err != nil {
		t.Fatalf("点不到正在播放页的音质胶囊：%v", err)
	}
	tester.Frame()
	if !app.npQInfoOpen || app.qualityOpen {
		t.Fatalf("npQInfoOpen=%v qualityOpen=%v，两套状态应互不联动", app.npQInfoOpen, app.qualityOpen)
	}
	if !tester.HasText("音频流信息") {
		t.Error("正在播放页的音质胶囊应弹出音频流信息浮窗")
	}
	if tester.HasText("自动（最高可播）") {
		t.Error("切档菜单不该被带到正在播放页")
	}

	// 收起正在播放页后，播放条胶囊照旧弹切档菜单，且不带出流信息浮窗。
	app.npOpen = false
	app.npQInfoOpen = false
	tester.Frame()
	time.Sleep(450 * time.Millisecond) // 等退场动画把浮层摘掉
	tester.Frame()
	if err := tester.Click("FLAC"); err != nil {
		t.Fatalf("点不到播放条的音质胶囊：%v", err)
	}
	tester.Frame()
	if !app.qualityOpen {
		t.Error("播放条胶囊应打开切档菜单")
	}
	if app.npQInfoOpen {
		t.Error("播放条胶囊不该动正在播放页浮窗的状态")
	}
	if !tester.HasText("自动（最高可播）") {
		t.Error("播放条胶囊应弹出切档菜单")
	}
	if tester.HasText("音频流信息") {
		t.Error("播放条的切档菜单里不该出现流信息浮窗")
	}
}
