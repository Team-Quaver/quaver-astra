package appui

import (
	"fmt"
	"image"
	"image/color"
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

// sideTestApp 造一个只用于跑视图的 App。
//
// 不走 New()：那会真连后端、真启 mpv 子进程，测试既慢又不确定。
// 视图需要的几样东西在这里补齐：
//   - Conf 必须是真 Store（视图到处读配置，nil 会 panic）
//   - Router 停在 /settings（首页会 ensureHome 去连后端，测试里没有）
//   - Covers 不能为 nil（用户区要读头像）
func sideTestApp(t *testing.T, collapsed bool) (*App, *ui.Tester) {
	t.Helper()
	p := player.New(nil, nilEngine{}, &nullPrefs{})
	app := &App{
		PL:            p,
		API:           nil,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/settings"),
		Covers:        NewCoverCache(),
		sbCollapsed:   collapsed,
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	return app, ui.NewTester(app.View, testWinW, testWinH)
}

// nilEngine 是空实现，仅为让 player 构造出来（侧栏渲染不碰播放）。
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
func (nilEngine) OnObserve(func())                             {}

type nullPrefs struct{}

func (*nullPrefs) String(string, string) string  { return "" }
func (*nullPrefs) Float(string, float64) float64 { return 0 }
func (*nullPrefs) Bool(string, bool) bool        { return false }
func (*nullPrefs) Set(string, any)               {}

// mustFind 取一个必然存在的文本块；失败时列出当前帧所有文本，
// 让断言报错能直接看出「侧栏到底渲染了什么」。
func mustFind(t *testing.T, tester *ui.Tester, s string) ui.Rect {
	t.Helper()
	r, ok := tester.Find(s)
	if !ok {
		t.Fatalf("未找到 %q；当前帧文本：%v", s, tester.Texts())
	}
	return r
}

// loggedBackend 是已登录的假后端：让用户区渲染出头像和歌单。
// 对齐断言要量的是头像/图标的实际落点，文本锚点在收起态不存在，
// 只能走像素；所以测试里造一个不触网的登录态。
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
func (loggedBackend) Tiers() (*player.TierTable, error) { return nil, nil }
func (loggedBackend) Resolve(string, string, int64, string, []string) (*player.StreamInfo, error) {
	return nil, nil
}
func (loggedBackend) FetchLyric(string, bool) (string, string, error) { return "", "", nil }
func (loggedBackend) LikeSong(int64, int64, bool) error               { return nil }

// sideLoggedApp 渲染已登录形态的 App，等异步用户信息就绪后返回。
// 路由停在 /settings：导航区没有激活项，像素扫描不会被激活底色污染。
func sideLoggedApp(t *testing.T, collapsed bool) *App {
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
		sbCollapsed:   collapsed,
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
}

// inkBox 是扫描区内内容像素（图标/头像笔画）的水平范围。
type inkBox struct{ minX, maxX, midX int }

// inkBounds 在 (x0..x1, y0..y1) 里找与底色差异明显的像素，返回其水平范围。
// 头像（圆形填充）与图标（线框字形）形状不同，比较左缘/中点比比较质心稳。
func inkBounds(tester *ui.Tester, x0, x1, y0, y1 int) (inkBox, error) {
	img := tester.Image()
	if img == nil {
		return inkBox{}, fmt.Errorf("无法取得渲染结果")
	}
	bg, ok := dominantColor(img, x0, y0, y1)
	if !ok {
		return inkBox{}, fmt.Errorf("扫描区 x=%d..%d y=%d..%d 无像素", x0, x1, y0, y1)
	}
	const delta = 25
	isInk := func(x, y int) bool {
		c := at(img, x, y)
		return absDiff(int(c.R), int(bg.R)) > delta &&
			absDiff(int(c.G), int(bg.G)) > delta &&
			absDiff(int(c.B), int(bg.B)) > delta
	}
	box := inkBox{minX: -1, maxX: -1}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if isInk(x, y) {
				if box.minX < 0 || x < box.minX {
					box.minX = x
				}
				if x > box.maxX {
					box.maxX = x
				}
			}
		}
	}
	if box.minX < 0 {
		return inkBox{}, fmt.Errorf("区域 x=%d..%d y=%d..%d 内没有内容像素", x0, x1, y0, y1)
	}
	box.midX = (box.minX + box.maxX) / 2
	return box, nil
}

// rowClusters 沿 y 扫描 (x0..x1)×(y0..y1)，把含内容像素的行按连续性聚成
// 簇，返回每簇的水平范围与 y 区间（按 y 升序）。收起态侧栏没有文本锚点，
// 图标落点只能从渲染像素反查。
func rowClusters(t *testing.T, tester *ui.Tester, x0, x1, y0, y1 int) []iconCluster {
	t.Helper()
	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	bg, ok := dominantColor(img, x0, y0, y1)
	if !ok {
		t.Fatal("扫描区无像素")
	}
	const delta = 25
	isInk := func(x, y int) bool {
		c := at(img, x, y)
		return absDiff(int(c.R), int(bg.R)) > delta &&
			absDiff(int(c.G), int(bg.G)) > delta &&
			absDiff(int(c.B), int(bg.B)) > delta
	}
	rowHas := func(y int) bool {
		for x := x0; x <= x1; x++ {
			if isInk(x, y) {
				return true
			}
		}
		return false
	}
	// 簇内允许的最大空隙：图标笔画之间的空行会被并进同一簇，
	// 图标与图标之间（约 24px 空白）则必须分开。
	const maxGap = 8
	var out []iconCluster
	y := y0
	for y <= y1 {
		if !rowHas(y) {
			y++
			continue
		}
		top, last := y, y
		for y <= y1 {
			if rowHas(y) {
				last = y
				y++
				continue
			}
			if y-last > maxGap {
				break
			}
			y++
		}
		box := inkBox{minX: x1, maxX: x0}
		for yy := top; yy <= last; yy++ {
			for x := x0; x <= x1; x++ {
				if isInk(x, yy) {
					if x < box.minX {
						box.minX = x
					}
					if x > box.maxX {
						box.maxX = x
					}
				}
			}
		}
		box.midX = (box.minX + box.maxX) / 2
		out = append(out, iconCluster{midX: box.midX, minX: box.minX, maxX: box.maxX, top: top, bot: last})
	}
	return out
}

// TestSidebarAvatarAlignsWithIcons 展开态：头像左缘必须与导航图标左缘同列。
//
// 用户反馈「头像没对齐图标」。历史上有两个根因都能让这条断言失败：
//   - 头像 Row 建在 userBtn.Children 之外——MyGo 的元素在创建处挂到当前
//     父节点，于是头像行成了侧栏列的直接子节点，贴到侧栏最左（x≈0），
//     而图标列在 x≈22；
//   - 头像吃 Avatar 的默认尺寸（≈32px），比导航图标大一大圈。
func TestSidebarAvatarAlignsWithIcons(t *testing.T) {
	app := sideLoggedApp(t, false)
	tester := ui.NewTester(app.View, testWinW, testWinH)

	name := mustFind(t, tester, "Ne0W0r1d新界")
	home := mustFind(t, tester, "首页")
	ava, err := inkBounds(tester, 2, 52, int(name.Y)-10, int(name.Y+name.H)+10)
	if err != nil {
		t.Fatalf("扫不到头像：%v", err)
	}
	ico, err := inkBounds(tester, 2, 45, int(home.Y)-6, int(home.Y+home.H)+6)
	if err != nil {
		t.Fatalf("扫不到首页图标：%v", err)
	}
	if d := ava.minX - ico.minX; d < -5 || d > 5 {
		t.Errorf("头像左缘 x=%d 与导航图标左缘 x=%d 相差 %dpx，未对齐", ava.minX, ico.minX, d)
	}
}

// TestSidebarCollapsedIconsCentered 收起态：侧栏里所有内容（头像、导航
// 图标、歌单图标）都必须以侧栏中线为中心，而不是整体靠右。
//
// 历史根因：按钮 FillWidth()+MarginX(10)——100% 宽按容器全宽解析、Margin
// 叠加在其外，按钮实际占 10..74（向右溢出被裁剪），内容盒右移 12px，
// 图标中心落到 x≈43。固定尺寸的底部按钮不吃这套布局，所以只有
// 导航区整体偏右——正是用户看到的「图标全部靠右而非居中」。
func TestSidebarCollapsedIconsCentered(t *testing.T) {
	app := sideLoggedApp(t, true)
	tester := ui.NewTester(app.View, testWinW, testWinH)

	clusters := rowClusters(t, tester, 2, int(sideWidthSmall)-2, 10, 530)
	// 头像 + 5 个导航项（再加歌单图标）至少 6 簇；过少说明有内容没
	// 落进侧栏，或被排成了别的形状。
	if len(clusters) < 6 {
		t.Fatalf("侧栏竖列应至少聚出 6 簇内容，实测 %d 簇：%+v", len(clusters), clusters)
	}
	for i, c := range clusters {
		if d := c.midX - int(sideWidthSmall/2); d < -3 || d > 3 {
			t.Errorf("第 %d 簇中心 x=%d（范围 %d..%d，y=%d..%d）偏离侧栏中线 %v 达 %dpx",
				i, c.midX, c.minX, c.maxX, c.top, c.bot, sideWidthSmall/2, d)
		}
	}
}

// TestHomeHeroNotCrushed 窄窗口（1000 逻辑宽）下，一行放不下「hero 最小
// 宽 + 新歌速递 430」时新歌速递必须折行，hero 不得被挤到标题折行。
//
// 历史根因：新歌速递列写死 Width(430)，hero Grow(1) 无最小宽——1000 宽
// 下 hero 只剩 290，扣掉 160 头图、内边距与间距后文字列仅 ~76px，
// 「PLAYLIST · 今日精选」折成两行，标题断得不成样子。
func TestHomeHeroNotCrushed(t *testing.T) {
	app := sideLoggedApp(t, false)
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

	label := mustFind(t, tester, "PLAYLIST · 今日精选")
	if label.H > 22 {
		t.Errorf("精选标签高度 %.0f，折行了（单行约 17）——hero 文字列仍被挤压", label.H)
	}
	news := mustFind(t, tester, "新歌速递")
	if news.Y < label.Y+60 {
		t.Errorf("新歌速递（y=%.0f）仍与 hero（y=%.0f）同行，折行未生效", news.Y, label.Y)
	}
}

// TestSidebarNavRowsEvenlySpaced 导航项之间的行距应均匀。
//
// 行高与行距统一后，图标中心线才落在同一条水平网格线上；行距不均是
// 「对齐很怪异」最常见的来源。
func TestSidebarNavRowsEvenlySpaced(t *testing.T) {
	_, tester := sideTestApp(t, false)

	labels := []string{"首页", "猜你喜欢", "每日 30 首", "我喜欢", "收藏的歌单"}
	boxes := make([]ui.Rect, 0, len(labels))
	for _, l := range labels {
		boxes = append(boxes, mustFind(t, tester, l))
	}

	step := boxes[1].Y - boxes[0].Y
	if step <= 0 {
		t.Fatalf("导航项行距异常: %v", step)
	}
	for i := 1; i < len(boxes); i++ {
		if d := boxes[i].Y - boxes[i-1].Y; d != step {
			t.Errorf("「%s」与上一项行距 %.1f，与首项行距 %.1f 不一致",
				labels[i], d, step)
		}
	}
}

// TestSidebarCollapsedFooterIsVertical 收起态下设置与展开按钮必须纵向排列。
//
// 用户明确点出的问题：原先底部是横排 Row，而收起态侧栏只有 64 宽，
// 两个 36 的按钮加 gap 放不下，会被挤出边界、与上方导航项错位。
//
// 验证方式：扫像素找图标笔画。按钮本身是透明底，只有图标有颜色，
// 因此以「深色像素」聚类出图标位置：
//   - 纵排 → 两簇图标 y 分开、x 中心相同
//   - 横排 → 两簇图标 y 相同、x 分列
func TestSidebarCollapsedFooterIsVertical(t *testing.T) {
	_, tester := sideTestApp(t, true)

	// 侧栏水平中心：收起态按钮以侧栏宽度居中
	const x0 = sideWidthSmall / 2

	// 只看侧栏底部：歌单区的音符图标也在 x0 这一列上，扫描起点必须
	// 落在它们下方，否则会一起被聚类进来。底部按钮紧贴侧栏下缘。
	clusters := iconClusters(t, tester, x0, 520, testWinH)
	if len(clusters) < 2 {
		t.Fatalf("底部应有两个图标（设置、展开），实测 %d 簇：%v；"+
			"只有一个说明它们被排成了横排", len(clusters), clusters)
	}

	first, second := clusters[0], clusters[1]
	if first.midY == second.midY {
		t.Errorf("两个图标 y 中心相同（%d），是横排而非纵排", first.midY)
	}
	if d := first.midX - second.midX; d > 2 || d < -2 {
		t.Errorf("两个图标 x 中心相差 %dpx（%d vs %d），未纵向对齐",
			d, first.midX, second.midX)
	}
	// 图标应落在侧栏范围内，不越界
	for i, c := range clusters[:2] {
		if c.midX < 0 || c.midX > int(sideWidthSmall) {
			t.Errorf("第 %d 个图标中心 x=%d 越出侧栏宽度 %v", i, c.midX, sideWidthSmall)
		}
	}
	t.Logf("收起态底部图标：设置(%d,%d) 展开(%d,%d)",
		first.midX, first.midY, second.midX, second.midY)
}

// iconCluster 是一簇图标像素。
type iconCluster struct {
	midX, midY int
	minX, maxX int
	top, bot   int
}

// iconClusters 在 (x0, y0..y1) 这一竖列上找深色像素（图标笔画），
// 按 y 聚类成若干簇。返回按 y 升序排列。
//
// 判定「深色」用相对亮度而非固定阈值：主题可明可暗，
// 但图标总是比侧栏底色更深/更不透明。
func iconClusters(t *testing.T, tester *ui.Tester, x0 float32, y0, y1 int) []iconCluster {
	t.Helper()
	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	// 侧栏底色取该列众数
	bg, ok := dominantColor(img, int(x0), y0, y1)
	if !ok {
		t.Fatal("扫描区无像素")
	}
	// 明暗差异阈值：图标与底色至少差 25 级，否则视为底色
	const delta = 25
	isIcon := func(x, y int) bool {
		c := at(img, x, y)
		return absDiff(int(c.R), int(bg.R)) > delta &&
			absDiff(int(c.G), int(bg.G)) > delta &&
			absDiff(int(c.B), int(bg.B)) > delta
	}

	// 簇内允许的最大空隙：图标笔画之间、图标与图标之间都会有空行，
	// 但空隙超过这个值就认为是两个不同的东西（如歌单图标与底部按钮）。
	const maxGap = 6

	var out []iconCluster
	y := y0
	for y <= y1 {
		if !hasIconAt(img, int(x0), y, y, isIcon) {
			y++
			continue
		}
		top := y
		last := y
		for y <= y1 {
			if hasIconAt(img, int(x0), y, y, isIcon) {
				last = y
				y++
				continue
			}
			// 连续空白超过 maxGap 则收束当前簇
			if y-last > maxGap {
				break
			}
			y++
		}
		bot := last
		// 在中段那一行左右量水平范围
		mid := (top + bot) / 2
		minX, maxX := int(x0), int(x0)
		for x := int(x0); x >= 0; x-- {
			if !hasIconAt(img, x, mid, mid, isIcon) {
				break
			}
			minX = x
		}
		for x := int(x0); x < img.Bounds().Dx(); x++ {
			if !hasIconAt(img, x, mid, mid, isIcon) {
				break
			}
			maxX = x
		}
		out = append(out, iconCluster{
			midX: (minX + maxX) / 2, midY: (top + bot) / 2,
			minX: minX, maxX: maxX, top: top, bot: bot,
		})
	}
	return out
}

// hasIconAt 检查 (x, yFrom..yTo) 这一小段里是否有图标像素。
func hasIconAt(img image.Image, x, yFrom, yTo int, isIcon func(int, int) bool) bool {
	if x < 0 || x >= img.Bounds().Dx() {
		return false
	}
	for y := yFrom; y <= yTo; y++ {
		if y < 0 || y >= img.Bounds().Dy() {
			return false
		}
		if isIcon(x, y) {
			return true
		}
	}
	return false
}

// TestSidebarGeometryInvariants 侧栏几何常量本身的自洽性。
//
// 这些数字是「对齐」的全部依据，改动时值得先看这里。
func TestSidebarGeometryInvariants(t *testing.T) {
	// 收起态：footer 按钮 + 两侧内边距要能放进侧栏宽度
	if sideFootBtnSize+2*sidePadX > sideWidthSmall {
		t.Errorf("收起态放不下底部按钮: %v + %v*2 > %v",
			sideFootBtnSize, sidePadX, sideWidthSmall)
	}
	// 头像不能大于行高，否则会溢出按钮、圆角与对齐都失真
	if sideAvatarSize > sideBtnHeight {
		t.Errorf("头像 %v 大于行高 %v，会溢出按钮", sideAvatarSize, sideBtnHeight)
	}
	// 头像不应比导航图标大出一大圈（视觉上会凸出来）
	if sideAvatarSize > sideIconSize*2 {
		t.Errorf("头像 %v 相对图标 %v 过大", sideAvatarSize, sideIconSize)
	}
}

// dominantColor 取 (x, y0..y1) 这一列中出现最多的颜色（当背景色）。
func dominantColor(img image.Image, x, y0, y1 int) (color.RGBA, bool) {
	counts := map[color.RGBA]int{}
	for y := y0; y <= y1; y++ {
		if x < 0 || x >= img.Bounds().Dx() || y < 0 || y >= img.Bounds().Dy() {
			continue
		}
		counts[at(img, x, y)]++
	}
	var best color.RGBA
	bestN := 0
	for c, n := range counts {
		if n > bestN {
			best, bestN = c, n
		}
	}
	return best, bestN > 0
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
