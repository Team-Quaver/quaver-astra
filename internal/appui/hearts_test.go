package appui

import (
	"image"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 列表行的红心 =====
//
// 用户反馈：「我喜欢的红心有时候渲染不出来」。两个成因——
//  1. 只在「已收藏或鼠标悬停」时才画，于是「我喜欢」没同步上来的那一轮，
//     收藏过的歌整行不见红心，看起来像图标没渲染（其实是状态没到货）；
//  2. 分页拉「我喜欢」时第 2 页失败会连第 1 页一起丢掉。
//
// 修法：红心每行常驻，未收藏画空心、已收藏画实心红；拉取侧「拿到多少落多少」。
// 这里守的是渲染侧（状态侧的回归在 internal/player 的 LoadLoved 测试里）。

// lovedBackend 只把「我喜欢」这条路接通：红心画不画得出来全看它。
type lovedBackend struct {
	npBackend
	liked []player.Song
}

func (b lovedBackend) LoginStatus() (bool, error) { return true, nil }

func (b lovedBackend) LikedPage(page, num int) ([]player.Song, int64, bool, error) {
	if page > 1 {
		return nil, 0, false, nil
	}
	return b.liked, int64(len(b.liked)), false, nil
}

// heartsTestApp 渲染给定路由：m1 已收藏、m2 未收藏，队列是空的。
// 用 /daily 看列表行的红心；用 /settings 时页面上没有歌，能看到的红心
// 只可能来自播放条——两处断言因此不会互相顶掉。
func heartsTestApp(t *testing.T, route string) (*App, *ui.Tester) {
	t.Helper()
	p := player.New(
		lovedBackend{liked: []player.Song{{Mid: "m1", SongID: 1, SongType: 1}}},
		loadEngine{}, &nullPrefs{})
	p.LoadLoved()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !p.IsLoved("m1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.IsLoved("m1") {
		t.Fatal("夹具的「我喜欢」没加载进来，测试无从谈起")
	}

	app := &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter(route),
		Covers:        NewCoverCache(),
		daily:         songListState{loaded: true, songs: menuSongs},
		singers:       map[string]*singerState{},
		albums:        map[string]*albumState{},
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	return app, ui.NewTester(app.View, testWinW, testWinH)
}

// TestSongRowHeartsAlwaysDrawn 不碰鼠标，两行红心都必须存在。
func TestSongRowHeartsAlwaysDrawn(t *testing.T) {
	_, tester := heartsTestApp(t, "/daily")
	tester.Frame()

	filled, ok := tester.Find("取消收藏")
	if !ok {
		t.Fatalf("已收藏的行没画出红心；当前帧文本：%v", tester.Texts())
	}
	hollow, ok := tester.Find("收藏")
	if !ok {
		t.Fatalf("未收藏的行也要有空心红心（不能只在悬停时出现）；当前帧文本：%v", tester.Texts())
	}

	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	t.Logf("红心落墨：空心 %d 像素（红 %d），实心 %d 像素（红 %d）",
		countInk(img, hollow), countRed(img, hollow), countInk(img, filled), countRed(img, filled))
	if n := countInk(img, hollow); n < 8 {
		t.Errorf("空心红心没画出来（该区域只有 %d 个落墨像素）", n)
	}
	if n := countInk(img, filled); n < 20 {
		t.Errorf("实心红心落墨太少（%d 个像素），像是没渲染", n)
	}
	// 实心是红的，空心不是：两者的区别必须在像素上看得见
	if n := countRed(img, filled); n < 20 {
		t.Errorf("已收藏应为实心红心，红色像素只有 %d", n)
	}
	if n := countRed(img, hollow); n > 4 {
		t.Errorf("未收藏不应是实心红，红色像素 %d", n)
	}
}

// TestPlayerBarHeartReflectsLoved 播放条上的红心同样跟着收藏态：
// 当前曲收藏过 → 实心（标签「取消收藏」），没收藏 → 空心（标签「收藏」），
// 没有当前曲 → 什么都不画（没得收藏）。
func TestPlayerBarHeartReflectsLoved(t *testing.T) {
	app, tester := heartsTestApp(t, "/settings")
	tester.Frame()
	if _, ok := tester.Find("收藏"); ok {
		t.Error("没有当前曲目时播放条不该画红心")
	}

	app.PL.PlayList([]player.Song{{Mid: "m1", Title: "稻香", Artists: "周杰伦", Interval: 239}}, 0)
	tester.Frame()
	if _, ok := tester.Find("取消收藏"); !ok {
		t.Fatalf("播放条上正在播放的收藏曲应是实心红心；当前帧文本：%v", tester.Texts())
	}

	app.PL.PlayList([]player.Song{{Mid: "mX", Title: "没收藏的歌", Artists: "周杰伦", Interval: 200}}, 0)
	tester.Frame()
	if _, ok := tester.Find("收藏"); !ok {
		t.Fatalf("播放条上未收藏的歌应有空心红心；当前帧文本：%v", tester.Texts())
	}
	if _, ok := tester.Find("取消收藏"); ok {
		t.Error("换歌后还挂着「取消收藏」，收藏态没跟着当前曲刷新")
	}
}

// ===== 像素小工具 =====

// rectPx 把 Find 回来的 DIP 矩形夹进图像范围内。
func rectPx(img *image.RGBA, r ui.Rect) image.Rectangle {
	b := img.Bounds()
	x0 := clampInt(int(r.X), b.Min.X, b.Max.X)
	y0 := clampInt(int(r.Y), b.Min.Y, b.Max.Y)
	x1 := clampInt(int(r.X+r.W), b.Min.X, b.Max.X)
	y1 := clampInt(int(r.Y+r.H), b.Min.Y, b.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return image.Rectangle{}
	}
	return image.Rect(x0, y0, x1, y1)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// countInk 数一块区域里「与四周底色不同」的像素，即图标真正落了多少墨。
func countInk(img *image.RGBA, r ui.Rect) int {
	area := rectPx(img, r)
	if area.Empty() {
		return 0
	}
	bg := at(img, area.Min.X, area.Min.Y)
	n := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			c := at(img, x, y)
			if absDiff(int(c.R), int(bg.R))+absDiff(int(c.G), int(bg.G))+absDiff(int(c.B), int(bg.B)) > 60 {
				n++
			}
		}
	}
	return n
}

// countRed 数一块区域里的红心色像素（主题的 Danger=#e8465a 一带）。
func countRed(img *image.RGBA, r ui.Rect) int {
	area := rectPx(img, r)
	if area.Empty() {
		return 0
	}
	n := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			c := at(img, x, y)
			if int(c.R) > 130 && int(c.R)-int(c.G) > 55 && int(c.R)-int(c.B) > 30 {
				n++
			}
		}
	}
	return n
}
