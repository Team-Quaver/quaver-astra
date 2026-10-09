package appui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 缓动与进度 =====

// TestBezierMatchesCSSShape：cubic-bezier 是「以 x 为自变量」的曲线，
// 不能用参数 t 直接当进度。这里用几条可手算的曲线钉住实现。
func TestBezierMatchesCSSShape(t *testing.T) {
	curves := []struct {
		x1, y1, x2, y2 float32
		fast           bool // 起步快：中点应已过半
	}{
		{0.22, 0.61, 0.36, 1, true}, // easePage
		{0.32, 0.72, 0.24, 1, true}, // easePanel
		{0, 0, 1, 1, false},         // 线性：中点正好一半
	}
	for _, c := range curves {
		e := bezier(c.x1, c.y1, c.x2, c.y2)
		if v := e(0); v != 0 {
			t.Errorf("bezier%v(0)=%v，应为 0", c, v)
		}
		if v := e(1); v != 1 {
			t.Errorf("bezier%v(1)=%v，应为 1", c, v)
		}
		mid := e(0.5)
		if math.Abs(float64(mid-0.5)) > 1e-3 {
			if !c.fast || mid <= 0.5 {
				t.Errorf("bezier%v(0.5)=%.3f，与预期不符", c, mid)
			}
		}
		// 单调：缓动不能回头。
		prev := float32(-1)
		for i := 0; i <= 40; i++ {
			v := e(float32(i) / 40)
			if v < prev-1e-4 {
				t.Fatalf("bezier%v 在 x=%.3f 处回落：%.4f → %.4f", c, float32(i)/40, prev, v)
			}
			prev = v
		}
	}
}

// TestMotionProgress：零值 start 表示「没跑过动画」，进度取 1——
// 首帧与测试环境必须直接落在终态，否则内容会藏在透明里。
func TestMotionProgress(t *testing.T) {
	var m motion
	if p, moving := m.at(time.Now()); p != 1 || moving {
		t.Errorf("零值 motion 应为终态，实测 %v/%v", p, moving)
	}

	base := time.Unix(1_700_000_000, 0)
	m = motion{start: base, dur: 100 * time.Millisecond, ease: ui.Linear}
	if p, moving := m.at(base.Add(-time.Millisecond)); p != 0 || !moving {
		t.Errorf("起始前应为 0 且还在动，实测 %v/%v", p, moving)
	}
	if p, moving := m.at(base.Add(50 * time.Millisecond)); p != 0.5 || !moving {
		t.Errorf("半程应为 0.5 且还在动，实测 %v/%v", p, moving)
	}
	if p, moving := m.at(base.Add(200 * time.Millisecond)); p != 1 || moving {
		t.Errorf("结束后应为终态，实测 %v/%v", p, moving)
	}

	// 延迟：延迟期内进度为 0，且仍在动。
	m = motion{start: base, delay: 40 * time.Millisecond, dur: 100 * time.Millisecond, ease: ui.Linear}
	if p, moving := m.at(base.Add(20 * time.Millisecond)); p != 0 || !moving {
		t.Errorf("延迟期内应为 0 且还在动，实测 %v/%v", p, moving)
	}
	if p, _ := m.at(base.Add(90 * time.Millisecond)); math.Abs(float64(p-0.5)) > 1e-3 {
		t.Errorf("延迟结束后半程应为 0.5，实测 %v", p)
	}
}

// ===== 红心可读性 =====

// TestHeartUnlovedHasTextLevelContrast 是「无红心状态的红心在深色模式无
// 可读性」这条反馈的回归保护：未收藏的红心要在两套主题、两种落底（播放条
// 与列表行的悬停底）上都有正文级的对比度。
//
// 原先列表行写死 palLight.heart（浅色主题的硬编码红），深色悬停底上只有
// 3.4:1；播放条用 TextMuted，浅色下只有 4.8:1。
func TestHeartUnlovedHasTextLevelContrast(t *testing.T) {
	cases := []struct {
		name string
		dark bool
		mode string
		bg   func(t *ui.Theme) ui.Color
	}{
		{"浅色·播放条", false, "light", func(*ui.Theme) ui.Color { return palLight.bg }},
		{"浅色·列表悬停底", false, "light", func(t *ui.Theme) ui.Color { return t.SurfaceHover }},
		{"深色·播放条", true, "dark", func(t *ui.Theme) ui.Color { return palDark.card }},
		{"深色·列表悬停底", true, "dark", func(t *ui.Theme) ui.Color { return t.SurfaceHover }},
	}
	for _, tc := range cases {
		th := themeFor(t, tc.dark, tc.mode)
		_, col := heartStyle(false, false, th)
		if r := contrastRatio(col, tc.bg(th)); r < 7 {
			t.Errorf("%s：未收藏红心对比度仅 %.2f:1（要求 ≥7:1）", tc.name, r)
		}
		// 已收藏的实心红心也不能糊在底色里（红本身对比度受限，取 3:1 的
		// 非文本 UI 门槛）。
		_, loved := heartStyle(true, false, th)
		if r := contrastRatio(loved, tc.bg(th)); r < 3 {
			t.Errorf("%s：已收藏红心对比度仅 %.2f:1（要求 ≥3:1）", tc.name, r)
		}
	}
}

// themeFor 用给定外观与 Style.Theme 跑一帧空视图，取回 buildTheme 的结果。
func themeFor(t *testing.T, dark bool, mode string) *ui.Theme {
	t.Helper()
	app := &App{Conf: conf.Open(t.TempDir())}
	app.Conf.Set("Style.Theme", mode)
	var got *ui.Theme
	tester := ui.NewTester(func(c *ui.Context) {
		th := app.buildTheme(c)
		c.SetTheme(th)
		got = th
	}, 320, 200)
	tester.SetDark(dark)
	got = nil
	tester.Frame()
	if got == nil {
		t.Fatal("视图没有运行")
	}
	return got
}

// contrastRatio 是 WCAG 相对亮度对比度。
func contrastRatio(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(c ui.Color) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// ===== 浅色模式 =====

// TestForcedLightThemeOverridesDarkDesktop 守住「浅色模式无法使用」：
//
// 根因是 mygo 的 Linux 后端——SetSource("light") 只清了 GTK 的
// gtk-application-prefer-dark-theme，紧接着 IsDark() 又去问 XDG portal 的
// color-scheme，深色桌面上 portal 说是深色，强制浅色就被覆盖了。
// 现在应用自己按 Style.Theme 定明暗，桌面说深色也必须给出浅色。
func TestForcedLightThemeOverridesDarkDesktop(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.Router = ui.NewRouter("/settings")

	// 先确认「跟随系统 + 深色桌面」确实是深色，否则下面的断言没有意义。
	tester.SetDark(true)
	assertWindowBg(t, tester, 850, 20, palDark.bg)

	// 强制浅色：桌面仍然报深色，画面必须是浅色。
	app.Conf.Set("Style.Theme", "light")
	tester.Frame()
	assertWindowBg(t, tester, 850, 20, palLight.bg)

	// 强制深色：桌面报浅色也得是深色。
	tester.SetDark(false)
	app.Conf.Set("Style.Theme", "dark")
	tester.Frame()
	assertWindowBg(t, tester, 850, 20, palDark.bg)
}

// assertWindowBg 断言某点的像素是给定的窗口底色（容许 ±2 的取整误差）。
// 顶栏没有背景，露出来的就是窗口底色（MyGo 用 theme.Background 填窗口）。
func assertWindowBg(t *testing.T, tester *ui.Tester, x, y int, want ui.Color) {
	t.Helper()
	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	got := at(img, x, y)
	if d := absDiff(int(got.R), int(want.R)); d > 2 {
		t.Fatalf("(%d,%d) 像素 %v，期望接近窗口底色 %v（R 差 %d）", x, y, got, want, d)
	}
}

// ===== 正在播放页：封面不能被背景层压住 =====

const testQRCXML = `<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1">` +
	`<Lyric_1 LyricType="1" Lyrics="[ti:测试]&#10;[0,2000]那(0,500)一(500,500)年 (1000,1000)&#10;[2000,2000]第(2000,600)二(2600,400)年(3000,1000)" /></LyricInfo></QrcInfos>`

// npBackend 是正在播放页用的假后端：不触网、不播放，只把「当前歌曲 +
// 逐字歌词」这条链喂起来。
//
// 配 loadEngine：Resolve 给一个假的流地址，OpenURL 直接成功、Duration
// 立刻为正，awaitLoaded 便判定「打开成功」，起播链走到底并触发 fetchLyric。
// 不触网、不碰 mpv。
type npBackend struct {
	qrc   string
	trans string
}

// loadEngine 是「载入立刻成功」的假引擎：awaitLoaded 靠 Duration>0 判定
// 打开成功，所以这里必须给出正的时长——否则起播协程会一直降档重试，
// 歌词那一步（fetchLyric 在载入成功之后）永远走不到。
type loadEngine struct{ nilEngine }

func (loadEngine) Duration(float64) float64 { return 269 }

func (npBackend) LoginStatus() (bool, error)         { return false, nil }
func (npBackend) UserInfo() (player.UserInfo, error) { return player.UserInfo{}, nil }
func (npBackend) LikedPage(int, int) ([]player.Song, int64, bool, error) {
	return nil, 0, false, nil
}
func (npBackend) Playlists() ([]player.PlaylistRef, error) { return nil, nil }
func (npBackend) Tiers() (*player.TierTable, error)        { return nil, nil }
func (npBackend) Resolve(string, string, int64, string, []string) (*player.StreamInfo, error) {
	return &player.StreamInfo{URL: "test://stream", Tier: "flac", TierLabel: "FLAC"}, nil
}
func (b npBackend) FetchLyric(string, bool) (string, string, error) { return b.qrc, b.trans, nil }
func (npBackend) LikeSong(int64, int64, bool) error                 { return nil }

// npTestApp 渲染一张正在播放页，封面是一整块纯色，便于按像素断言。
func npTestApp(t *testing.T, cover color.RGBA) (*App, *ui.Tester) {
	t.Helper()
	return npTestAppLyrics(t, cover, testQRCXML, "")
}

// npTestAppLyrics 允许测试注入翻译 LRC（版式回归用：长译文必须换行）。
func npTestAppLyrics(t *testing.T, cover color.RGBA, qrc, trans string) (*App, *ui.Tester) {
	t.Helper()
	const pmid = "001Qu4I30VtaFH"
	p := player.New(npBackend{qrc: qrc, trans: trans}, loadEngine{}, &nullPrefs{})
	p.PlayList([]player.Song{{
		Mid: "m1", Title: "晴天", Artists: "周杰伦", Album: "叶惠美",
		AlbumPmid: pmid, Interval: 269,
	}}, 0)
	// 歌词与封面解码都是异步的，等歌词就位再渲染。
	deadline := time.Now().Add(3 * time.Second)
	for {
		if lines, st := p.LyricLines(); st == "ok" && len(lines) > 0 {
			break
		}
		if time.Now().After(deadline) {
			st := ""
			if _, s := p.LyricLines(); s != "" {
				st = s
			}
			t.Fatalf("假歌词未就绪（state=%q）", st)
		}
		time.Sleep(2 * time.Millisecond)
	}

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	// 上面把 alpha 也刷成 255 了：整块不透明纯红。
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, cover)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成封面失败：%v", err)
	}
	covers := NewCoverCache()
	for _, size := range []int{300, 500} {
		covers.PutRaw(coverURL(pmid, "", size), buf.Bytes())
	}

	app := &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/settings"),
		Covers:        covers,
		npOpen:        true,
		probedMid:     "m1", // 跳过封面取色：否则会异步拉图并改强调色
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	return app, ui.NewTester(app.View, testWinW, testWinH)
}

// TestNowPlayingCoverIsNotVeiled 守住「正在播放页封面被透明化」：
//
// 根因是绘制顺序。MyGo 里 Absolute 子元素画在流式子元素【之上】，原先
// 「模糊封面底 + 黑色遮罩」是绝对定位、内容行是流式，所以两层背景整个压在
// 封面上——封面看着像掺进背景里、半透明。修法是内容层也绝对定位，绘制顺序
// 回到「底图 → 遮罩 → 内容」。
//
// 验证：封面是一整块纯红，数画面里最宽的一段「接近纯红」像素。背景层若压
// 在封面上，同一块会被掺进黑/模糊色，纯红就凑不出来。
func TestNowPlayingCoverIsNotVeiled(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	_, tester := npTestApp(t, red)

	// 开门动画（上滑 + 淡入）还在跑时整页都是半透明的，等它落定再做像素断言。
	time.Sleep(450 * time.Millisecond)
	tester.Frame()

	img := tester.Image()
	if img == nil {
		t.Fatal("无法取得渲染结果")
	}
	nearRed := func(x, y int) bool {
		c := at(img, x, y)
		return c.R >= 248 && c.G <= 16 && c.B <= 16
	}
	best := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		run := 0
		for x := 0; x < img.Bounds().Dx(); x++ {
			if nearRed(x, y) {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
	}
	// 封面是 300×300（圆角 18），过中心的一行应接近满宽。
	if best < 290 {
		t.Errorf("画面里最宽的纯红横段只有 %dpx（封面宽 300）——"+
			"背景层（模糊底图/遮罩）盖在了封面上", best)
	}
}

// TestNowPlayingLyricsHaveBreathingRoom：歌词首尾要留出舞台，不能贴着
// 内容层顶边；同时整行给出 pointer，告诉用户点行可以跳转。
func TestNowPlayingLyricsHaveBreathingRoom(t *testing.T) {
	_, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	time.Sleep(450 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find("那一年")
	if !ok {
		t.Fatalf("找不到首行歌词；当前帧文本：%v", tester.Texts())
	}
	// 1000×720 的定妆照里首行应落在约 218px：既不贴顶，也没有被推到下半屏。
	if r.Y < 100 || r.Y > 400 {
		t.Errorf("首行歌词 Y=%v，应在 100~400（上下留白失效）", r.Y)
	}

	tester.Move(r.X+4, r.Y+4)
	tester.Frame()
	if got := tester.Cursor(); got != ui.CursorPointer {
		t.Errorf("歌词行上的指针是 %v，期望 pointer（点击跳转提示）", got)
	}
}

// TestNowPlayingTranslationWraps：翻译不再 SingleLine 截断；长译文要折行，
// 完整信息必须能读到。
func TestNowPlayingTranslationWraps(t *testing.T) {
	body := strings.Repeat("这是一段很长的译文", 12)
	trans := "[00:00.00]" + body
	app, tester := npTestAppLyrics(t, color.RGBA{R: 96, G: 148, B: 210, A: 255}, testQRCXML, trans)
	app.PL.SetShowTranslation(true)

	deadline := time.Now().Add(3 * time.Second)
	for {
		lines, _ := app.PL.LyricLines()
		if len(lines) > 0 && lines[0].Trans != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("开启翻译后未对齐进首行")
		}
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(450 * time.Millisecond)
	tester.Frame()

	r, ok := tester.Find(body)
	if !ok {
		t.Fatalf("找不到翻译文本；当前帧文本：%v", tester.Texts())
	}
	// 单行中文字号约 12px（MyGo 的行盒约 20px）；这段文案按歌词列宽
	// 至少折两行，行盒高度应明显超过单行。
	if r.H <= 20 {
		t.Errorf("翻译只占 %.1fpx 高——仍被压成单行截断", r.H)
	}
}

// TestNowPlayingUsesWordLevelLyrics：逐字歌词链路（qrc=1 → ParseQRC →
// 列表按行渲染）走通，当前句由 Painter 逐字绘制，但仍要在无障碍树里留名。
func TestNowPlayingUsesWordLevelLyrics(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 255, A: 255})
	if n := len(app.PL.QrcLines()); n != 2 {
		t.Fatalf("逐字歌词应解析出 2 行，实测 %d", n)
	}
	if !tester.HasText("那一年 ") && !tester.HasText("那一年") {
		t.Errorf("逐字歌词行未出现在无障碍名里；当前帧文本：%v", tester.Texts())
	}
	if !tester.HasText("第二年") {
		t.Errorf("第二行逐字歌词缺失；当前帧文本：%v", tester.Texts())
	}
}
