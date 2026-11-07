package appui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

const (
	testWinW = 1000
	testWinH = 700
)

// testApp 造一个只用于跑视图的 App。
//
// 不走 New()：那会真连后端、真启 mpv 子进程，测试既慢又不确定。
// 视图需要的几样东西在这里补齐：
//   - Conf 必须是真 Store（视图到处读配置，nil 会 panic）
//   - Router 停在 /settings（首页会 ensureHome 去连后端，测试里没有）
//   - Covers 不能为 nil（头像按钮要读头像）
func testApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	p := player.New(nil, nilEngine{}, &nullPrefs{})
	app := &App{
		PL:            p,
		API:           nil,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/settings"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	return app, ui.NewTester(app.View, testWinW, testWinH)
}

// nilEngine 是空实现，仅为让 player 构造出来（顶栏渲染不碰播放）。
type nilEngine struct{}

func (nilEngine) OpenURL(string, float64, float64, bool) error { return nil }
func (nilEngine) Play()                                        {}
func (nilEngine) Pause()                                       {}
func (nilEngine) Stop()                                        {}
func (nilEngine) Close()                                       {}
func (nilEngine) IsPlaying() bool                              { return false }
func (nilEngine) Ended() bool                                  { return false }
func (nilEngine) Paused() bool                                 { return true }
func (nilEngine) Position() float64                            { return 0 }
func (nilEngine) Duration(fallback float64) float64            { return fallback }
func (nilEngine) SeekTo(float64, float64)                      {}
func (nilEngine) SetVolume(float64)                            {}
func (nilEngine) LoadError() string                            { return "" }
func (nilEngine) Loaded() bool                                 { return true }
func (nilEngine) LoadErrorFatal() bool                         { return false }
func (nilEngine) OnObserve(func())                             {}

type nullPrefs struct{}

func (*nullPrefs) String(string, string) string  { return "" }
func (*nullPrefs) Float(string, float64) float64 { return 0 }
func (*nullPrefs) Bool(string, bool) bool        { return false }
func (*nullPrefs) Set(string, any)               {}

// mustFind 取一个必然存在的文本块；失败时列出当前帧所有文本，
// 让断言报错能直接看出「顶栏到底渲染了什么」。
func mustFind(t *testing.T, tester *ui.Tester, s string) ui.Rect {
	t.Helper()
	r, ok := tester.Find(s)
	if !ok {
		t.Fatalf("未找到 %q；当前帧文本：%v", s, tester.Texts())
	}
	return r
}

// loggedBackend 是已登录的假后端：让头像与歌单渲染出来。
// 布局断言量的是按钮的实际落点，所以测试里造一个不触网的登录态。
type loggedBackend struct{}

func (loggedBackend) LoginStatus() (bool, error) { return true, nil }
func (loggedBackend) UserInfo() (player.UserInfo, error) {
	return player.UserInfo{Name: "Ne0W0r1d新界", VipLabel: "超级会员"}, nil
}
func (loggedBackend) LikedPage(int, int) ([]player.Song, int64, bool, error) {
	return nil, 0, false, nil
}
func (loggedBackend) Playlists() ([]player.PlaylistRef, error) {
	return []player.PlaylistRef{{ID: 1, Title: "博客"}, {ID: 2, Title: "古典"}}, nil
}
func (loggedBackend) GuessSongs(int) ([]player.Song, error) { return nil, nil }
func (loggedBackend) Tiers() (*player.TierTable, error)     { return nil, nil }
func (loggedBackend) Resolve(string, string, int64, string, []string, int64) (*player.StreamInfo, error) {
	return nil, nil
}
func (loggedBackend) ReportListenTime(player.ListenReport) (player.ListenReportResult, error) {
	return player.ListenReportResult{OK: true}, nil
}
func (loggedBackend) FetchLyric(string, bool, bool) (string, string, string, error) {
	return "", "", "", nil
}
func (loggedBackend) LikeSong(int64, int64, bool) error { return nil }

// loggedApp 渲染已登录形态的 App，等异步用户信息就绪后返回。
// 路由停在 /settings：导航区没有激活项，像素扫描不会被激活底色污染。
func loggedApp(t *testing.T) *App {
	t.Helper()
	p := player.New(loggedBackend{}, nilEngine{}, &nullPrefs{})
	p.RefreshUser()
	for i := 0; i < 200; i++ {
		if p.LoggedIn() && p.Me().Name != "" && len(p.Playlists()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !p.LoggedIn() {
		t.Fatal("假后端登录态未就绪")
	}
	return &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/settings"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
}

// TestHomeHeroNotCrushed 窄窗口（1000 逻辑宽）下，一行放不下「hero 最小
// 宽 + 新歌速递 430」时新歌速递必须折行，hero 不得被挤到标题折行。
//
// 历史根因：新歌速递列写死 Width(430)，hero Grow(1) 无最小宽——1000 宽
// 下 hero 只剩 290，扣掉 160 头图、内边距与间距后文字列仅 ~76px，
// 「PLAYLIST · 今日精选」折成两行，标题断得不成样子。
func TestHomeQuickStartNotCrushed(t *testing.T) {
	app := loggedApp(t)
	app.Router = ui.NewRouter("/")
	app.home = homeState{
		loaded: true,
		recs: []backend.SonglistSummary{
			{ID: 1, Title: "2026全网最火超好听循环神曲", Desc: "全网最火超好听循环神曲合集，听到停不下来", Songnum: 103, Nickname: "精选君"},
			{ID: 2, Title: "90后上班族必备：随身听舒缓烦恼", Songnum: 88},
			{ID: 3, Title: "点击播放｜全修金曲正在循环", Songnum: 66},
			{ID: 4, Title: "500首抖音热歌：包你一次听个够", Songnum: 500},
		},
		newsong: []player.Song{
			{Mid: "m1", Title: "自由的你", Artists: "G.E.M.邓紫棋"},
			{Mid: "m2", Title: "以我之见", Artists: "谭维维"},
			{Mid: "m3", Title: "醒时歌", Artists: "周传雄/希林娜依高"},
			{Mid: "m4", Title: "我们在场", Artists: "周深/王者荣耀"},
			{Mid: "m5", Title: "Yellow Brick Road", Artists: "ONE OR EIGHT"},
			{Mid: "m6", Title: "亲爱的赶路人", Artists: "小沈阳"},
		},
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)

	radio := mustFind(t, tester, "无限电台")
	if radio.W < 40 || radio.H > 24 {
		t.Errorf("无限电台标签被压成 %.0fx%.0f，应保持单行可读", radio.W, radio.H)
	}
	news := mustFind(t, tester, "新歌速递")
	if news.W < 60 || news.H > 24 {
		t.Errorf("新歌速递标题被压成 %.0fx%.0f", news.W, news.H)
	}
	if first := mustFind(t, tester, "自由的你"); first.W < 80 {
		t.Errorf("新歌行标题宽度只有 %.0f，速递列仍被挤压", first.W)
	}
}

// TestTopBarNavIsCenteredAndEvenlySpaced 菜单栏在顶栏里居中，所有按钮
// 共享一条水平中线。菜单只画图标，功能名通过 tooltip 与无障碍名暴露；
// Find 读到的是按钮盒，所以这里的几何就是用户点得到的落点。
func TestTopBarNavIsCenteredAndEvenlySpaced(t *testing.T) {
	_, tester := testApp(t)

	labels := []string{"首页", "猜你喜欢", "每日 30 首", "我喜欢", "音乐网盘", "歌单"}
	boxes := make([]ui.Rect, 0, len(labels))
	for _, l := range labels {
		boxes = append(boxes, mustFind(t, tester, l))
	}

	first, last := boxes[0], boxes[len(boxes)-1]
	for i, b := range boxes {
		if d := (b.Y + b.H/2) - (first.Y + first.H/2); d > 1 || d < -1 {
			t.Errorf("「%s」中线 y=%.1f，与首项相差 %.1f，未落在同一条水平线上",
				labels[i], b.Y+b.H/2, d)
		}
		if b.Y+b.H > topBarH+1 {
			t.Errorf("「%s」Y=%.1f H=%.1f，越出顶栏高度 %.0f", labels[i], b.Y, b.H, topBarH)
		}
	}
	step := boxes[1].X - boxes[0].X
	if step <= 0 {
		t.Fatalf("菜单按钮水平间距异常: %v", step)
	}
	for i := 1; i < len(boxes); i++ {
		if d := boxes[i].X - boxes[i-1].X; d != step {
			t.Errorf("「%s」与上一项间距 %.1f，与首项间距 %.1f 不一致",
				labels[i], d, step)
		}
	}
	mid := (first.X + last.X + last.W) / 2
	if d := mid - testWinW/2; d < -2 || d > 2 {
		t.Errorf("菜单组中心 x=%.1f，窗口中线 %d，偏差 %.1f", mid, testWinW/2, d)
	}
}

// TestTopBarSearchIsButtonWithPopover 搜索框不常驻：顶栏只放一枚按钮，
// 点开后才出现输入浮窗；提交后进入搜索页。
func TestTopBarSearchIsButtonWithPopover(t *testing.T) {
	app, tester := testApp(t)
	// 提交后会进入搜索页，页面立刻向后端取数；给一个空响应的桩，
	// 测试只关心「按钮 → 浮窗 → 路由」这条链。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer srv.Close()
	app.API = backend.NewClient(srv.URL)
	tester.Frame()
	if _, ok := tester.Find("搜索音乐"); ok {
		t.Fatal("搜索输入框不该常驻顶栏")
	}
	if err := tester.Click("搜索"); err != nil {
		t.Fatalf("顶栏没有搜索按钮：%v", err)
	}
	tester.Frame()
	if _, ok := tester.Find("搜索音乐"); !ok {
		t.Fatalf("点搜索按钮后浮窗里没有输入框；当前文本：%v", tester.Texts())
	}
	tester.Type("周杰伦")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := app.Router.Path(); got != "/search" {
		t.Fatalf("提交后路由是 %q，期望 /search", got)
	}
	if got := app.Router.Query("kw"); got != "周杰伦" {
		t.Errorf("搜索关键词是 %q，期望「周杰伦」", got)
	}
}

// TestTopBarSettingsAndAvatarOnTheRight 设置与头像在右组：都在菜单右侧，
// 头像在设置右边，且两者共享同一条中线。
func TestTopBarSettingsAndAvatarOnTheRight(t *testing.T) {
	app := loggedApp(t)
	tester := ui.NewTester(app.View, testWinW, testWinH)

	lastNav := mustFind(t, tester, "歌单")
	settings := mustFind(t, tester, "设置")
	avatar := mustFind(t, tester, "个人中心")
	if settings.X <= lastNav.X+lastNav.W {
		t.Errorf("设置按钮 x=%.1f 不在菜单右侧（菜单右缘 %.1f）", settings.X, lastNav.X+lastNav.W)
	}
	if avatar.X <= settings.X {
		t.Errorf("头像 x=%.1f 不在设置（x=%.1f）右边", avatar.X, settings.X)
	}
	if d := (avatar.Y + avatar.H/2) - (settings.Y + settings.H/2); d > 1 || d < -1 {
		t.Errorf("头像与设置中线相差 %.1f，未对齐", d)
	}
}

// TestMacWindowControlsFollowHIG macOS 的自绘 CSD 必须放在左边；
// 其他平台维持右置。平台判断抽成纯函数，便于在 Linux CI 上验证。
func TestMacWindowControlsFollowHIG(t *testing.T) {
	if !macTrafficOnLeft("darwin") {
		t.Error("macOS 的窗口按钮应在左边（HIG）")
	}
	for _, goos := range []string{"windows", "linux"} {
		if macTrafficOnLeft(goos) {
			t.Errorf("%s 的窗口按钮应维持右置", goos)
		}
	}
}

// TestNoSidebarCollapseSetting 菜单移到顶栏后，侧栏的收起状态一并取消。
func TestNoSidebarCollapseSetting(t *testing.T) {
	_, tester := testApp(t)
	tester.Frame()
	if tester.HasText("收起侧栏") {
		t.Error("设置页仍保留「收起侧栏」——顶栏菜单没有展开状态")
	}
}

// ===== 歌单页夹具 =====

// coverBackend 在 loggedBackend 之上给歌单配上封面地址。
//
// 地址用 cover:// 这种不可能被真拉取的 scheme：命中 Covers.PutRaw 预置的
// 位图时走缓存；没预置的那条（「私藏」）会立刻以「unsupported protocol
// scheme」失败，测试因此既不触网，又能稳定复现「封面取不到」的形态。
type coverBackend struct{ loggedBackend }

func (coverBackend) Playlists() ([]player.PlaylistRef, error) {
	return []player.PlaylistRef{
		{ID: 1, Title: "博客", Cover: "cover://blog"},
		{ID: 2, Title: "古典", Cover: "cover://classic"},
		{ID: 3, Title: "私藏"}, // 没有封面：应退回♪
	}, nil
}

// solidCoverPNG 造一张纯色封面（PNG 字节），供 Covers.PutRaw 预置。
func solidCoverPNG(t *testing.T, col color.RGBA) []byte {
	t.Helper()
	const n = 64
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetRGBA(x, y, col)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码测试封面失败：%v", err)
	}
	return buf.Bytes()
}

var (
	coverBlog    = color.RGBA{0xE8, 0x3A, 0x3A, 0xFF}
	coverClassic = color.RGBA{0x2E, 0x6E, 0xFF, 0xFF}
)

// coverApp 渲染「登录 + 三首歌单（两首有封面）」的 App。
func coverApp(t *testing.T) *App {
	t.Helper()
	p := player.New(coverBackend{}, nilEngine{}, &nullPrefs{})
	p.RefreshUser()
	for i := 0; i < 200; i++ {
		if len(p.Playlists()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(p.Playlists()) == 0 {
		t.Fatal("假后端歌单未就绪")
	}
	covers := NewCoverCache()
	covers.PutRaw("cover://blog", solidCoverPNG(t, coverBlog))
	covers.PutRaw("cover://classic", solidCoverPNG(t, coverClassic))
	return &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/settings"),
		Covers:        covers,
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
}

// colorBox 返回扫描区内与 col 足够接近的像素范围（含抗锯齿边缘）。
func colorBox(img image.Image, x0, x1, y0, y1 int, col color.RGBA) (image.Rectangle, bool) {
	const tol = 28
	found := false
	box := image.Rectangle{Min: image.Pt(1<<20, 1<<20), Max: image.Pt(-1<<20, -1<<20)}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			c := at(img, x, y)
			if absDiff(int(c.R), int(col.R)) > tol ||
				absDiff(int(c.G), int(col.G)) > tol ||
				absDiff(int(c.B), int(col.B)) > tol {
				continue
			}
			found = true
			if x < box.Min.X {
				box.Min.X = x
			}
			if y < box.Min.Y {
				box.Min.Y = y
			}
			if x > box.Max.X {
				box.Max.X = x
			}
			if y > box.Max.Y {
				box.Max.Y = y
			}
		}
	}
	return box, found
}

// TestTopMenuHasNoPlaylistRows 歌单已全部收进歌单页，顶部菜单不再重复列歌单。
func TestTopMenuHasNoPlaylistRows(t *testing.T) {
	app := coverApp(t)
	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()
	for _, title := range []string{"博客", "古典", "私藏"} {
		if tester.HasText(title) {
			t.Errorf("顶栏仍列出了歌单 %q；应移入 /playlists", title)
		}
	}
	if !tester.HasText("歌单") {
		t.Error("顶栏应有歌单页入口")
	}
}

// TestPlaylistsPageListsAll 歌单页同时承载我创建的歌单与我收藏的歌单。
func TestPlaylistsPageListsAll(t *testing.T) {
	app := coverApp(t)
	app.favLists = favListsState{loaded: true}
	tester := ui.NewTester(app.playlistsView, testWinW, testWinH)
	tester.Frame()
	for _, s := range []string{"我创建的歌单", "博客", "古典", "私藏", "我收藏的歌单"} {
		if !tester.HasText(s) {
			t.Errorf("歌单页缺少 %q；当前文本：%v", s, tester.Texts())
		}
	}
}

func at(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
