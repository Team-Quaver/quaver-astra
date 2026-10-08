package appui

import (
	"slices"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 测试夹具 =====

// menuSongs 是「每日 30 首」页的两首行歌：稻香带歌手 mid（跳歌手页用），
// 七里香用于复制链接/名称的断言。队列与列表刻意用不同的歌，
// 「插到队列的哪一位置」才看得出来。
var menuSongs = []player.Song{
	{
		Mid: "m1", Name: "稻香", Title: "稻香",
		Subtitle:  "《满城尽带黄金甲》主题曲",
		Artists:   "周杰伦",
		Album:     "叶惠美",
		AlbumPmid: "004NclXP3Equ1W", AlbumMid: "al1",
		Interval: 239,
		Singers:  []player.Singer{{Mid: "s1", Name: "周杰伦"}},
	},
	{
		Mid: "m2", Name: "七里香", Title: "七里香",
		Subtitle: "演唱会版",
		Artists:  "周杰伦",
		Album:    "七里香",
		Interval: 296,
		Singers:  []player.Singer{{Mid: "s1", Name: "周杰伦"}},
	},
}

// menuTestApp 造一个「每日 30 首」页已就绪、队列已起播的 App。
//
// 队列放 [夜曲, 以父之名]，列表放 [稻香, 七里香]——两边不重叠，
// 才能断言「下一首播放」是插在当前曲之后、而不是碰巧同名。
// API 指向一个必然拒连的地址：万一有路径漏进加载分支，是快速失败
// 而不是 nil 解引用炸掉测试。
func menuTestApp(t *testing.T) (*App, *player.Player, *ui.Tester) {
	t.Helper()
	p := player.New(npBackend{}, loadEngine{}, &nullPrefs{})
	p.PlayList([]player.Song{
		{Mid: "q1", Name: "夜曲", Artists: "周杰伦", Interval: 226},
		{Mid: "q2", Name: "以父之名", Artists: "周杰伦", Interval: 342},
	}, 0)
	app := &App{
		PL:            p,
		API:           backend.NewClient("http://127.0.0.1:1"),
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/daily"),
		Covers:        NewCoverCache(),
		daily:         songListState{loaded: true, songs: menuSongs},
		singers:       map[string]*singerState{},
		albums:        map[string]*albumState{},
		playlist:      map[int64]*playlistState{},
		probedMid:     "q1", // 跳过封面取色：否则会异步拉图
		lastLyricLine: -2,
	}
	return app, p, ui.NewTester(app.View, testWinW, testWinH)
}

// seedSinger 把歌手页 s1 的数据预置成已加载：测试没有后端，
// ensureSinger 真去拉数据只会等到连接拒绝。
func seedSinger(app *App) {
	app.singers["s1"] = &singerState{
		loaded:  true,
		name:    "周杰伦",
		info:    backend.SingerBase{Name: "周杰伦", Avatar: "https://example.invalid/pic.jpg"},
		profile: backend.SingerProfile{Name: "周杰伦", ForeignName: "Jay Chou", Area: "台湾", Desc: "华语流行男歌手。"},
		hot: []player.Song{
			{Mid: "h1", Name: "晴天", Artists: "周杰伦"},
			{Mid: "h2", Name: "搁浅", Artists: "周杰伦"},
		},
		hotTotal: 299,
		news:     []player.Song{{Mid: "n1", Name: "新歌蓝", Artists: "周杰伦"}},
		albums: []backend.SingerAlbum{
			{Mid: "al1", Pmid: "004NclXP3Equ1W", Name: "叶惠美", TimePublic: "2003-07-31"},
		},
		albumN: 12,
	}
}

// seedAlbum 把专辑页 al1 的数据预置成已加载（同 seedSinger 的理由）。
func seedAlbum(app *App) {
	app.albums["al1"] = &albumState{
		loaded: true,
		name:   "叶惠美",
		info: backend.AlbumInfo{
			Name: "叶惠美", TimePublic: "2003-07-31", Singer: "周杰伦", AlbumType: "专辑",
		},
		songs: []player.Song{
			{Mid: "h1", Name: "晴天", Artists: "周杰伦", Album: "叶惠美", AlbumMid: "al1", Interval: 269},
		},
		total: 14,
	}
}

// ===== 右键菜单 =====

// TestSongRowMenuStructure 钉住菜单结构：插队语义改为「下一首播放」后，
// 顶层必须是 [下一首播放, 添加到队列, -, 收藏, -, 跳转至, 更多操作]——
// 旧菜单的「插队播放（立即切歌）」「加入队列（语义名不副实）」不得回来。
func TestSongRowMenuStructure(t *testing.T) {
	_, _, tester := menuTestApp(t)
	if err := tester.RightClick("稻香"); err != nil {
		t.Fatal(err)
	}
	want := []string{"下一首播放", "添加到队列", "-", "收藏", "-", "跳转至", "更多操作"}
	if got := tester.Menu(); !slices.Equal(got, want) {
		t.Fatalf("右键菜单 = %v，期望 %v", got, want)
	}
	tester.CloseMenu()
}

// TestContextMenuNextPlayDoesNotInterrupt 「下一首播放」必须只排队不切歌：
// 当前曲不动、index 不动、引擎不再载入，歌被插到 index+1。
//
// 这条钉住的语义改动正是本次需求：旧实现走 PlayNextNow（立即切歌），
// 右键一次当前曲就被打断。
func TestContextMenuNextPlayDoesNotInterrupt(t *testing.T) {
	_, p, tester := menuTestApp(t)
	curBefore, _ := p.Current()

	if err := tester.RightClick("稻香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("下一首播放"); err != nil {
		t.Fatal(err)
	}
	q := p.Queue()
	if len(q) != 3 {
		t.Fatalf("队列应为 3 首，实测 %d：%v", len(q), q)
	}
	if q[1].Mid != "m1" {
		t.Errorf("稻香应插在当前曲之后（q[1]），实测 q[1]=%s", q[1].Mid)
	}
	if p.Index() != 0 {
		t.Errorf("index = %d，插入不应切换当前曲", p.Index())
	}
	if cur, _ := p.Current(); cur.Mid != curBefore.Mid {
		t.Errorf("当前曲从 %s 变成 %s——被插队打断了", curBefore.Mid, cur.Mid)
	}
	// 回执 toast：原地没有可见变化，没有 toast 用户会以为没点上。
	if !tester.HasText("已插队：稻香（下一首播放）") {
		t.Errorf("缺少插队回执 toast；当前帧文本：%v", tester.Texts())
	}

	// 添加到队列 = 追加到队尾，同样不打断。
	if err := tester.RightClick("稻香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("添加到队列"); err != nil {
		t.Fatal(err)
	}
	q = p.Queue()
	if len(q) != 4 || q[3].Mid != "m1" {
		t.Errorf("「添加到队列」应追加到队尾，实测队列 %v", q)
	}
	if p.Index() != 0 {
		t.Errorf("index = %d，追加不应切换当前曲", p.Index())
	}
}

// TestContextMenuJumpsToSingerThenAlbum 跳转至 ▸ 歌手 → 歌手页三块数据与
// 标签切换；歌手页的专辑卡 → 专辑页。路由与页面状态由本次需求新接。
func TestContextMenuJumpsToSingerThenAlbum(t *testing.T) {
	app, _, tester := menuTestApp(t)
	seedSinger(app)
	seedAlbum(app)

	if err := tester.RightClick("稻香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("跳转至", "歌手", "周杰伦"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/singer/s1" {
		t.Fatalf("路由 = %q，期望 /singer/s1", got)
	}
	if got := app.Router.Query("name"); got != "周杰伦" {
		t.Errorf("name 查询参数 = %q，期望 周杰伦", got)
	}

	// 信息头：名称在页面上部（列表行的歌手列也会出现「周杰伦」，靠 y 区分）。
	name := mustFind(t, tester, "周杰伦")
	if name.Y > 250 {
		t.Errorf("歌手名落在 y=%.0f，应在信息头（<250）", name.Y)
	}
	if !tester.HasText("歌曲 299") {
		t.Errorf("缺少歌曲总数；当前帧文本：%v", tester.Texts())
	}
	if !tester.HasText("专辑 12") {
		t.Errorf("缺少专辑总数；当前帧文本：%v", tester.Texts())
	}
	for _, tab := range []string{"热歌", "新歌", "专辑"} {
		mustFind(t, tester, tab)
	}
	mustFind(t, tester, "晴天")

	// 切新歌标签：只换显示，不重新加载（状态是预置的，加载会打到假 API）。
	app.singers["s1"].tab = 1
	tester.Frame()
	mustFind(t, tester, "新歌蓝")

	// 专辑标签：计数 + 专辑卡。
	app.singers["s1"].tab = 2
	tester.Frame()
	mustFind(t, tester, "共 12 张")
	mustFind(t, tester, "叶惠美")

	// 专辑卡 → 专辑页。
	if err := tester.Click("叶惠美"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/album/al1" {
		t.Fatalf("路由 = %q，期望 /album/al1", got)
	}
	if !tester.HasText("歌曲 14") {
		t.Errorf("专辑页缺少歌曲总数；当前帧文本：%v", tester.Texts())
	}
	mustFind(t, tester, "播放全部")
	mustFind(t, tester, "晴天")
}

// TestContextMenuCopyAndSearch 跳转至 ▸ 同名搜索落点 + 更多操作的两份复制。
func TestContextMenuCopyAndSearch(t *testing.T) {
	app, _, tester := menuTestApp(t)

	if err := tester.RightClick("七里香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("跳转至", "同名搜索"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := app.Router.Path(); got != "/search" {
		t.Errorf("路由 = %q，期望 /search", got)
	}
	if got := app.Router.Query("kw"); got != "七里香" {
		t.Errorf("搜索词 = %q，期望 七里香", got)
	}

	// 跳转已经离开每日列表页；后面的复制动作还在歌曲行上，跳回去再右键。
	app.Router.Push("/daily")
	tester.Frame()

	// 复制链接：与主项目 songShareUrl 同格式。
	if err := tester.RightClick("七里香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("更多操作", "复制歌曲链接"); err != nil {
		t.Fatal(err)
	}
	if got, want := tester.Clipboard(), "https://y.qq.com/n/ryqq/songDetail/m2"; got != want {
		t.Errorf("复制链接 = %q，期望 %q", got, want)
	}

	// 复制名称：主名 + 副标题（对齐主项目 songTitle + songSubtitle 的拼法）。
	if err := tester.RightClick("七里香"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("更多操作", "复制歌曲名称"); err != nil {
		t.Fatal(err)
	}
	if got, want := tester.Clipboard(), "七里香 演唱会版"; got != want {
		t.Errorf("复制名称 = %q，期望 %q", got, want)
	}
}
