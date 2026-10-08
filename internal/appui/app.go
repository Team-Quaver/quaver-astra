// Package appui 是 Quaver Astra 的原生 UI（MyGo ui 包）：视图是状态的函数。
package appui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/vault"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// App 是整个窗口的应用状态；view 从它构建每一帧。
type App struct {
	Win    *mygo.Window
	API    *backend.Client
	Conf   *conf.Store
	Vault  *vault.Vault
	PL     *player.Player
	Router *ui.Router
	Covers *CoverCache
	tint   tintState
	// glowNow 是本帧使用的辅色（进度条/氛围），由 buildTheme 刷新。
	glowNow ui.Color

	npOpen        bool
	queueOpen     bool
	sbCollapsed   bool
	searchDraft   string
	volOpen       bool
	qualityOpen   bool
	seekDragging  bool
	seekFrac      float32
	probedMid     string
	lastLyricLine int

	// dark 是本帧的明暗外观（buildTheme 每帧刷新）。取色 goroutine 要读它，
	// 所以走原子量而不是裸 bool。
	dark atomic.Bool

	// enter 是路由页错峰入场的时钟；pos 把低频采样位置外推成每帧连续值；
	// karaBuf/karaSpans 是逐字高亮的复用缓冲（每帧都要算，不该每帧分配）。
	enter     enterState
	pos       posSmoother
	karaBuf   []float32
	karaSpans []ui.Span

	// npBlurKey/npBlurAt 是正在播放页模糊底图的换曲淡入时钟。
	npBlurKey string
	npBlurAt  time.Time

	npList    ui.ListState // 正在播放页歌词列表
	queueList ui.ListState // 播放队列列表

	// 各页面的数据态（历史上往返保留数据本身）
	home     homeState
	liked    songListState
	favLists favListsState
	daily    songListState
	guess    guessState
	playlist map[int64]*playlistState
	search   searchState
	login    loginState
	settings settingsState
	singers  map[string]*singerState // 歌手页（按 mid 缓存）
	albums   map[string]*albumState  // 专辑页（按 mid 缓存）

	// wasLoggedIn 记录上一次通知时的登录态，用来识别「登录/登出」这一跳变
	wasLoggedIn bool
}

// New 组装应用（后端已由 main 启动；v 为凭证加密存储，可 nil）。
func New(base string, store *conf.Store, v *vault.Vault) *App {
	api := backend.NewClient(base)
	eng := audio.New()
	pl := player.New(newAPIAdapter(api), eng, store)
	a := &App{
		API:           api,
		Conf:          store,
		PL:            pl,
		Router:        ui.NewRouter("/"),
		Covers:        NewCoverCache(),
		sbCollapsed:   store.Bool("Window.SidebarCollapsed", false),
		queueOpen:     store.Bool("Window.QueueOpen", false),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	pl.OnNotify(func() {
		// 登录态变化时，收藏的歌单依赖登录态，缓存要作废重来。
		// （player 不知道 appui 的页面状态，所以在 App 层做。）
		if a.PL.LoggedIn() != a.wasLoggedIn {
			a.wasLoggedIn = a.PL.LoggedIn()
			a.favLists = favListsState{}
		}
		if a.Win != nil {
			a.Win.Update(func() {})
		}
	})
	return a
}

// Attach 绑定窗口：接上重绘通知、启动播放器与登录态刷新、跑自检钩子。
func (a *App) Attach(win *mygo.Window) {
	a.Win = win
	a.PL.Boot()
	a.PL.RefreshUser()
	a.maybeSelfCheck()
}

// View 是窗口根视图。
func (a *App) View(c *ui.Context) {
	t := a.buildTheme(c)
	c.SetTheme(t)
	// 每帧推进路由入场时钟（页面里没有入场区块时也要走，否则路径变化会被漏掉）。
	a.routeEnter(c)

	if a.npOpen && c.Shortcut(0, ui.KeyEscape) {
		a.npOpen = false
	}

	// 当前歌变化时重取色
	if cur, ok := a.PL.Current(); ok && cur.Mid != a.probedMid {
		a.probedMid = cur.Mid
		a.probeCover(coverURL(cur.AlbumPmid, cur.AlbumMid, 300))
	}

	root := ui.Column(c).Fill()
	root.Children(func() {
		body := ui.Box(c).Grow(1).Clip()
		body.Children(func() {
			a.mainRow(c)
			if a.npOpen {
				a.nowPlaying(c, t)
			}
		})
		a.playerBar(c, t)
	})
}

func (a *App) mainRow(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).Fill().Children(func() {
			a.topBar(c)
			ui.Row(c).Grow(1).FillWidth().AlignItems(ui.Stretch).Children(func() {
				a.routeArea(c)
				if a.queueOpen {
					a.queuePanel(c)
				}
			})
		})
	})
}

// routeArea 把路由页铺进内容区。
func (a *App) routeArea(c *ui.Context) {
	a.Router.View(c, func(r *ui.Route) {
		switch {
		case r.Match("/"):
			r.Title("首页")
			a.homeView(c)
		case r.Match("/guess"):
			r.Title("猜你喜欢")
			a.guessView(c)
		case r.Match("/daily"):
			r.Title("每日 30 首")
			a.dailyView(c)
		case r.Match("/liked"):
			r.Title("我喜欢")
			a.likedView(c)
		case r.Match("/favlists"):
			r.Title("收藏的歌单")
			a.favListsView(c)
		case r.Match("/playlist/{id}"):
			r.Title("歌单")
			id, _ := strconv.ParseInt(r.Param("id"), 10, 64)
			a.playlistView(c, id)
		case r.Match("/singer/{mid}"):
			r.Title("歌手")
			a.singerView(c, r.Param("mid"), r.Query("name"))
		case r.Match("/album/{mid}"):
			r.Title("专辑")
			a.albumView(c, r.Param("mid"), r.Query("name"))
		case r.Match("/search"):
			r.Title("搜索")
			a.searchView(c, r.Query("kw"))
		case r.Match("/login"):
			r.Title("登录")
			a.loginView(c)
		case r.Match("/settings"):
			r.Title("设置")
			a.settingsView(c)
		default:
			ui.Text(c, "页面不存在").TextColor(c.Theme().TextMuted).Padding(24)
		}
	})
}

// ===== 侧栏 =====

type navItem struct {
	path  string
	icon  string
	label string
}

var navItems = []navItem{
	{"/", "home", "首页"},
	{"/guess", "guess", "猜你喜欢"},
	{"/daily", "daily", "每日 30 首"},
	{"/liked", "heart", "我喜欢"},
	{"/favlists", "star", "收藏的歌单"},
}

// 侧栏几何。展开 216，收起 64。收起态只放得下 44 宽的内容区，
// 所以所有子元素都要走 sidePad() 统一缩进，避免各处手写数值不一致。
//
// 垂直节奏同样要统一：导航项、用户区、底部按钮共用 sideBtnHeight 的行高与
// sideIconSize 的图标尺寸，这样它们的图标中心线才在同一条水平线上。
//
// 布局上有一条铁律：侧栏里的按钮不许 FillWidth+MarginX 组合。列的交叉轴
// 默认 Stretch 本来就会先扣掉 MarginX 再拉伸；显式 100% 宽却是按容器全宽
// 解析的，Margin 叠加在其外，按钮会向右溢出（收起态 64 宽 → 按钮实际占
// 10..74，内容盒右移 12px，图标整体偏右）。
//
// 用户头像曾经用 Avatar 的默认尺寸（FontSize*2.25≈32px），比导航图标
// 大一大圈且让用户区行高（48）与导航行高（40）不一致——于是头像既凸出、
// 又和下方图标错位，看起来「没对齐」。
const (
	sideWidth       = 216.0
	sideWidthSmall  = 64.0
	sidePadX        = 10.0
	sideBtnHeight   = 40.0
	sideFootBtnSize = 36.0
	sideIconSize    = 18.0
	sideAvatarSize  = 26.0
)

// sideLabelMin 是「还放得下文字标签」的宽度下限。收起/展开是宽度过渡，
// 标签若跟 a.sbCollapsed 布尔量走，动画中段就会突兀地出现/消失；
// 以实际宽度为准，标签与裁剪同步进出。
const sideLabelMin = 132.0

// sidePad 是当前形态下的水平内边距：收起态按钮要居中，缩进更小。
func (a *App) sidePad() float32 {
	if a.sbCollapsed {
		return 10
	}
	return sidePadX
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	target := float32(sideWidth)
	if a.sbCollapsed {
		target = sideWidthSmall
	}
	side := ui.Column(c).PaddingY(10).Gap(2).Background(a.sideBg(t)).Clip()
	// 收起/展开是宽度过渡，不是宽度硬切（主项目 .sidebar width .24s）。
	// 第一帧 Animate 直接返回目标值，所以启动画面与测试环境拿到的就是终态宽度。
	w := side.AnimateWith("sidebar-w", target, 220*time.Millisecond, easePage)
	side.Width(w)
	label := w > sideLabelMin
	collapsed := !label
	side.Children(func() {
		// 用户区。
		//
		// 头像要和下面导航项的图标对齐，两侧必须共享同一套几何：
		//   - 按钮不设 FillWidth（见上方常量注释），列交叉轴 Stretch 会
		//     扣掉 MarginX 后拉伸，展开/收起态的按钮盒都不溢出
		//   - 行高都取 sideBtnHeight，AlignItems(Center) 让头像处于行中线
		//   - 头像显式给定边长，不吃 Avatar 的默认 FontSize*2.25（约 32px，
		//     比导航图标大一大圈，视觉上就凸出来了）
		//   - 子元素必须在 userBtn.Children 里创建：MyGo 的元素在创建处
		//     就挂到当前父节点，在 Children 外先建 Row 会把它挂成按钮的
		//     兄弟——头像因此贴到侧栏最左边，和图标错开一整列
		me := a.PL.Me()
		loggedIn := a.PL.LoggedIn()

		userBtn := ui.ButtonBase(c).Height(sideBtnHeight).Padding(0, a.sidePad()).
			Margin(0, a.sidePad(), 6, a.sidePad()).Radius(10).AlignItems(ui.Center).Gap(10).
			Transition(hoverFade)
		if loggedIn {
			ava := a.Covers.Get(me.Avatar, a.invalidate)
			userBtn.Children(func() {
				ui.Avatar(c, me.Name, ava).Size(sideAvatarSize, sideAvatarSize)
				if label {
					ui.Column(c).Grow(1).Gap(0).Children(func() {
						ui.Text(c, me.Name).FontSize(fz(13)).FontWeight(600).SingleLine().Ellipsis("…")
						if me.VipLabel != "" {
							ui.Text(c, me.VipLabel).FontSize(fz(10)).TextColor(t.Accent)
						}
					})
				}
			})
			if userBtn.Clicked() {
				a.Router.Push("/settings")
			}
		} else {
			userBtn.Children(func() {
				ui.Icon(c, Icons["user"]).FontSize(fz(sideIconSize))
				if label {
					ui.Text(c, "点击登录").FontSize(fz(13)).TextColor(t.TextMuted)
				}
			})
			if userBtn.Clicked() {
				a.Router.Push("/login")
			}
		}

		for _, it := range navItems {
			a.sideNavItem(c, t, it, label)
		}

		// 歌单列表（收起态只留图标）
		pls := a.PL.Playlists()
		if len(pls) > 0 && label {
			ui.Text(c, "我创建的歌单").FontSize(fz(11)).TextColor(t.TextMuted).
				Padding(10, 12, 4, 14)
		}
		for _, pl := range pls {
			path := fmt.Sprintf("/playlist/%d", pl.ID)
			// growFade：歌单是登录后异步到的，让它撑开入场而不是凭空出现。
			btn := ui.ButtonBase(c).Height(sideBtnHeight).Padding(0, a.sidePad()).
				MarginX(a.sidePad()).Radius(8).AlignItems(ui.Center).Gap(10).
				Transition(growFade)
			active := a.Router.Path() == path
			btn.Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(fz(16)).AlignSelf(ui.Center)
				if label {
					ui.Text(c, pl.Title).FontSize(fz(13)).SingleLine().Ellipsis("…").Grow(1)
				}
			})
			if collapsed {
				btn.Center()
			}
			a.styleSideBtn(btn, active, c)
			if btn.Clicked() {
				a.Router.Push(path)
			}
		}

		ui.Spacer(c)

		a.sideFooter(c, t, collapsed, w)
	})
}

// sideFooter 是侧栏底部：设置 + 收起/展开。
//
// 收起态改为纵向排列：侧栏此时只有 64 宽，两个 36 的按钮加 gap 放不下，
// 横排会被挤出边界、与上方导航项的图标列错位。纵排后每个按钮都与展开态
// 的图标占据同一条中心线。
//
// collapsed 由实际宽度算出（见 sidebar），过渡中段宽度不够就提前排成纵排，
// 免得两个按钮被挤出侧栏。w 用于收起/展开图标的旋转进度。
//
// 返回容器元素，供布局测试读实际位置（测试不该硬编码 y 坐标——播放器条
// 占掉底部高度，靠算常数极易与真实布局脱节）。
func (a *App) sideFooter(c *ui.Context, t *ui.Theme, collapsed bool, w float32) *ui.Element {
	// 收起/展开图标按宽度进度旋转 180°（主项目是同一个 chevron 转 180°，
	// 而不是在两个方向上硬换图标）。0 = 收起态指右（提示「展开」），
	// 180 = 展开态指左（提示「收起」）。
	k := float32(0)
	if d := float32(sideWidth - sideWidthSmall); d > 0 {
		k = clampF32((w - sideWidthSmall) / d)
	}
	if collapsed {
		// 纵排：与上方 40 高的导航项共用同一条中心线
		return ui.Column(c).FillWidth().PaddingX(a.sidePad()).Gap(2).Children(func() {
			a.sideIconBtn(c, t, "settings", "设置", 0, func() {
				a.Router.Push("/settings")
			})
			a.sideIconBtn(c, t, "expand", "展开侧栏", 180*k, a.toggleSidebar)
		})
	}

	return ui.Row(c).FillWidth().PaddingX(a.sidePad()).Gap(4).Children(func() {
		a.sideIconBtn(c, t, "settings", "设置", 0, func() {
			a.Router.Push("/settings")
		})
		a.sideIconBtn(c, t, "expand", "收起侧栏", 180*k, a.toggleSidebar)
	})
}

// sideIconBtn 是侧栏底部/收起态用的纯图标按钮（带 tooltip）。
// rot 是图标的旋转角度（度），0 为不转。
func (a *App) sideIconBtn(c *ui.Context, t *ui.Theme, icon, tip string, rot float32, onClick func()) {
	b := ui.ButtonBase(c).Size(sideFootBtnSize, sideFootBtnSize).Radius(8).Center().
		Transition(hoverFade)
	b.Children(func() {
		ic := ui.Icon(c, Icons[icon]).FontSize(fz(sideIconSize)).AlignSelf(ui.Center)
		if rot != 0 {
			ic.Rotate(rot)
		}
	})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Tooltip(tip)
	if b.Clicked() {
		onClick()
	}
}

func (a *App) toggleSidebar() {
	a.sbCollapsed = !a.sbCollapsed
	a.Conf.Set("Window.SidebarCollapsed", a.sbCollapsed)
}

func (a *App) sideNavItem(c *ui.Context, t *ui.Theme, it navItem, label bool) {
	active := a.Router.Path() == it.path
	btn := ui.ButtonBase(c).Height(sideBtnHeight).Padding(0, a.sidePad()).
		MarginX(a.sidePad()).Radius(8).AlignItems(ui.Center).Gap(10).
		Transition(hoverFade)
	btn.Children(func() {
		ui.Icon(c, Icons[it.icon]).FontSize(fz(sideIconSize)).AlignSelf(ui.Center)
		if label {
			ui.Text(c, it.label).FontSize(fz(13.5)).Grow(1)
		}
	})
	if !label {
		// 收起态无标题：ButtonBase 默认主轴居中，图标落在按钮正中。
		btn.Center()
	} else {
		btn.Justify(ui.Start)
	}
	a.styleSideBtn(btn, active, c)
	if btn.Clicked() {
		a.Router.Push(it.path)
	}
}

// styleSideBtn 统一侧栏按钮的激活/悬停样式。
func (a *App) styleSideBtn(btn *ui.Element, active bool, c *ui.Context) {
	t := c.Theme()
	if active {
		accent, _, _ := a.tint.get()
		if !tintReady(a) {
			accent = t.Accent
		}
		btn.Background(accent.Alpha(0.13))
		btn.TextColor(accent)
	} else if btn.Hovered() {
		btn.Background(t.SurfaceHover)
	}
}

func tintReady(a *App) bool {
	_, _, ok := a.tint.get()
	return ok
}

func (a *App) sideBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#1b1d21")
	}
	return ui.Hex("#ececec")
}

// ===== 顶栏 =====

func (a *App) topBar(c *ui.Context) {
	t := c.Theme()
	bar := ui.Row(c).FillWidth().Height(44).Padding(0, 14).Gap(8).AlignItems(ui.Center).DragWindow()
	bar.Children(func() {
		if a.Router.CanGoBack() {
			back := ui.ButtonBase(c).Size(30, 30).Radius(15).Center()
			back.Children(func() { ui.Icon(c, Icons["back"]).FontSize(fz(17)).AlignSelf(ui.Center) })
			if back.Hovered() {
				back.Background(t.SurfaceHover)
			}
			back.Tooltip("返回")
			if back.Clicked() {
				a.Router.Back()
			}
		} else {
			ui.Icon(c, Icons["note"]).FontSize(fz(17)).AlignSelf(ui.Center).TextColor(t.Accent)
		}

		// 居中搜索胶囊
		ui.Spacer(c)
		sf := ui.SearchField(c, &a.searchDraft).Width(250).Label("搜索音乐")
		if sf.Submitted() {
			kw := strings.TrimSpace(a.searchDraft)
			if kw != "" {
				a.Router.Push("/search?kw=" + kw)
			}
		}
		ui.Spacer(c)

		a.windowButtons(c)
	})
}

func (a *App) windowButtons(c *ui.Context) {
	// Win 为 nil 时（无头测试/启动早期）不能碰窗口 API：那些调用会被转发到
	// 主线程执行，没有主线程就会永远等下去。
	if a.Win == nil {
		return
	}
	t := c.Theme()
	mk := func(icon, tip string, danger bool, fn func()) {
		b := ui.ButtonBase(c).Size(24, 24).Radius(12).Center()
		b.Children(func() { ui.Icon(c, Icons[icon]).FontSize(fz(14)).AlignSelf(ui.Center) })
		if b.Hovered() {
			if danger {
				b.Background(ui.Hex("#e81123"))
			} else {
				b.Background(t.SurfaceHover)
			}
		}
		b.Tooltip(tip)
		if b.Clicked() && a.Win != nil {
			fn()
		}
	}
	mk("winMin", "最小化", false, func() { a.Win.Minimize() })
	if a.Win.IsMaximized() {
		mk("winRestore", "向下还原", false, func() { a.Win.ToggleMaximize() })
	} else {
		mk("winMax", "最大化", false, func() { a.Win.ToggleMaximize() })
	}
	mk("winClose", "关闭", true, func() { a.Win.Close() })
}

// invalidate 是给异步回调用的重绘入口。
func (a *App) invalidate() {
	if a.Win != nil {
		a.Win.Update(func() {})
	}
}

// probeCover 封面可用后做取色（url 是封面地址）。
func (a *App) probeCover(url string) {
	if url == "" {
		return
	}
	raw := a.Covers.FetchRaw(url)
	if raw == nil {
		// 还没加载：挂一个加载完成回调再试
		a.Covers.Get(url, func() { a.probeCover(url) })
		return
	}
	go func() {
		r, ok := colorprobe.DominantFromBytes(raw)
		if !ok {
			return
		}
		// 明暗取 a.dark（由 buildTheme 按 Style.Theme 定），不用
		// mygo.Theme.IsDark()——Linux 上后者会被桌面的 portal 覆盖，
		// 强制浅色时取色映射也会跟着错。
		a.tint.set(r, a.dark.Load())
		a.invalidate()
	}()
}

// maybeSelfCheck 处理 QUAVER_ROUTE / QUAVER_SCREENSHOT / QUAVER_PLAY_FIRST 自检钩子。
func (a *App) maybeSelfCheck() {
	if route := os.Getenv("QUAVER_ROUTE"); route != "" && a.Win != nil {
		a.Win.Update(func() { a.Router.Push(route) })
	}
	if os.Getenv("QUAVER_PLAY_FIRST") == "1" {
		go func() {
			time.Sleep(2 * time.Second)
			res, err := a.API.RecommendNewsong()
			if err != nil {
				fmt.Fprintln(os.Stderr, "play-first:", err)
				return
			}
			a.update(func() {
				songs := toPlayerSongs(res.Songs)
				if len(songs) > 0 {
					a.playListNow(songs, 0)
					a.npOpen = true
				}
			})
		}()
	}
	if shot := os.Getenv("QUAVER_SCREENSHOT"); shot != "" && a.Win != nil {
		delay := 4.0
		if d, err := strconv.ParseFloat(os.Getenv("QUAVER_SCREENSHOT_DELAY"), 64); err == nil && d > 0 {
			delay = d
		}
		win := a.Win
		go func() {
			time.Sleep(time.Duration(delay * float64(time.Second)))
			png, err := win.CapturePage()
			if err != nil {
				fmt.Fprintln(os.Stderr, "screenshot:", err)
				win.Close()
				return
			}
			if err := os.WriteFile(shot, png, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "screenshot write:", err)
			} else {
				fmt.Fprintln(os.Stderr, "screenshot saved:", shot)
			}
			win.Close()
		}()
	}
}

// ApplyThemeSource 依据配置应用明暗主题来源（启动时调用一次）。
func (a *App) ApplyThemeSource() {
	switch a.Conf.String("Style.Theme", "system") {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}
