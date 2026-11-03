// Package appui 是 Quaver Astra 的原生 UI（MyGo ui 包）：视图是状态的函数。
package appui

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/smedia"
	"github.com/Team-Quaver/quaver-astra/internal/update"
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

	// Version 是当前构建版本（发布 CI 用 -X main.version 注入）；Upd 与 upd
	// 共同驱动「设置 → 关于」里的自动更新。
	Version string
	Upd     *update.Client
	upd     updateState
	// glowNow 是本帧使用的辅色（进度条/氛围），由 buildTheme 刷新。
	glowNow ui.Color

	npOpen       bool
	queueOpen    bool
	searchOpen   bool
	searchDraft  string
	pendingToast string
	volOpen      bool
	qualityOpen  bool
	// npMoreOpen / npQInfoOpen 是正在播放页「⋮ 菜单」与「音质胶囊浮窗」的开合。
	// 与播放条的 qualityOpen 各管各的：音质胶囊在正在播放页只展示音频流参数
	//（只读），切档仍只发生在播放条胶囊上，两个浮层互不联动。
	npMoreOpen  bool
	npQInfoOpen bool
	// npQInfo 是音质胶囊浮窗的流参数探测缓存（后台探测、回填重绘）。
	npQInfo       qinfoProbe
	seekDragging  bool
	seekFrac      float32
	probedMid     string
	lastLyricLine int

	// dark 是本帧的明暗外观（buildTheme 每帧刷新）。取色 goroutine 要读它，
	// 所以走原子量而不是裸 bool。
	dark atomic.Bool

	// enter 是路由页错峰入场的时钟；pos 把低频采样位置外推成每帧连续值；
	// karaBuf 是逐字进度的复用缓冲。歌词 spans 不能跨行复用：列表的绘制
	// 回调到帧末才执行，后建的行会覆盖先建行的缓冲区。
	enter   enterState
	pos     posSmoother
	karaBuf []float32

	// npBlurKey/npBlurAt 是正在播放页模糊底图的换曲淡入时钟。
	npBlurKey string
	npBlurAt  time.Time

	// splashAt/splashDone 是启动画面的时钟：Attach 时起算，淡出结束置
	// Done，此后不再建层。splashAt 零值（测试直构 App）= 不播，见 splash.go。
	splashAt   time.Time
	splashDone bool

	npList       ui.ListState // 正在播放页歌词列表
	queueList    ui.ListState // 播放队列列表
	reproCloseAt time.Time

	// 歌词跟随：npScroll 接管列表偏移，换行时按 lyricfollow.go 的曲线缓动
	// 滚到下一句，而不是瞬间跳位。npListRect/npShown 是上一帧的列表视口与
	// 实际偏移，和 LayoutBounds 同口径，用来算居中目标。
	npScroll   ui.ScrollState
	npListRect ui.Rect
	npShown    float32 // npRowRect 这份几何对应的列表偏移
	npPassOff  float32 // 本趟 build 开始时 npScroll.Y（即上一次布局的偏移）
	npRowLine  int     // npRowRect 是哪一句的行号
	npRowRect  ui.Rect // 上一次算目标时当前句的 LayoutBounds
	npFollow   lyricFollow
	// npFollowPause 是用户滚过歌词列表后的静默截止时间：在它之前换行不
	// 自动把列表拽回当前句，好让用户找歌词、点歌词。见 lyricfollow.go。
	npFollowPause time.Time
	// npScrollSeen 是上一帧结束时列表的偏移，用来认出帧与帧之间由用户
	// （滚轮、拖动滚动条）造成的位移；npScrollAbsorb 是「下一帧的偏移
	// 是我们自己滚的」，不计为用户滚动。
	npScrollSeen     float32
	npScrollSeenOK   bool
	npScrollAbsorb   bool
	npScrollAbsorbAt time.Time
	npWrote          bool // 上一帧的偏移是跟随动画写的
	// npResume/npSnapAt：静默期结束、当前句又不在已建的行里时，列表只能
	// 瞬间跳过去；npSnapAt 让这一跳淡入，而不是硬切。
	npResume bool
	npSnapAt time.Time
	// npSongKey 是歌词跟随所属的曲目（Mid）。换曲时清掉上一首的偏移与
	// 动画，新歌从第一句重新开始。
	npSongKey string

	// media 是系统媒体控制中心会话（Linux MPRIS / Windows SMTC），
	// 在 New 建立会话，Attach 时补窗口句柄，Close 时注销。
	media smedia.Controller
	// hotkeys 是快捷键运行态：全局注册（mygo.GlobalShortcut）与窗口内
	// 轮询（ui.Context.Shortcut）共用一套动作，见 hotkeys.go。
	// 零值可用：测试直构 App 不碰注册面。
	hotkeys hotkeyState
	// 托盘与应用级资源。Close 幂等：OnQuit 与 main 的 defer 都会走一次。
	tray        *mygo.Tray
	trayRoot    *mygo.Menu
	trayItems   *trayMenu
	traySig     string
	trayClicks  trayClickTracker
	sleep       *sleepBlocker
	quitStarted atomic.Bool
	closeOnce   sync.Once

	// 各页面的数据态（历史上往返保留数据本身）
	home        homeState
	liked       songListState
	favLists    favListsState
	daily       songListState
	guess       guessState
	playlist    map[int64]*playlistState
	playlistRec []int64
	search      searchState
	login       loginState
	profile     profileState
	settings    settingsState
	singers     map[string]*singerState // 歌手页（按 mid 缓存）
	singerRec   []string
	albums      map[string]*albumState // 专辑页（按 mid 缓存）
	albumRec    []string
	toplists    map[int64]*toplistState // 排行榜页（按 top id 缓存）
	toplistRec  []int64

	// apiSlots 限制 UI 侧同时进行的 HTTP 请求，避免快速浏览时多个大响应
	// 同时驻留。请求本身仍由各页面后台 goroutine 发起。
	apiOnce  sync.Once
	apiSlots chan struct{}

	// cacheSeed 是本地缓存前端半种（见 localcache.go），首次用到时生成。
	cacheSeedOnce sync.Once
	cacheSeed     string

	// wasLoggedIn 记录上一次通知时的登录态，用来识别「登录/登出」这一跳变
	wasLoggedIn bool
}

// New 组装应用（后端已由 main 启动；v 为凭证加密存储，可 nil；version 为
// 当前构建版本，用于更新比较）。
func New(base string, store *conf.Store, v *vault.Vault, version string) *App {
	api := backend.NewClient(base)
	eng := audio.New() // Liino 原生模式：进程内解码 + 直连系统音频
	a := &App{
		API:           api,
		Conf:          store,
		Version:       version,
		sleep:         newSystemSleepBlocker(),
		Router:        ui.NewRouter("/"),
		Covers:        NewCoverCache(),
		queueOpen:     store.Bool("Window.QueueOpen", false),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	pl := player.New(newAPIAdapter(api, a.localCacheSeed), eng, store)
	a.PL = pl
	// 缓存上限只存在内嵌后端的内存里，启动后按用户设置重推一次。
	a.applyLocalCacheLimit()
	// Wayland 会话下全局快捷键经 XDG GlobalShortcuts 门户由桌面代持，
	// 设置页不再展示这一列（见 hotkeys.go）。
	a.hotkeys.wayland = waylandSession()
	a.initMedia()
	a.initUpdate()
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitStarted.Store(true) })
	pl.OnNotify(func() {
		// 登录态变化时，收藏的歌单与个人中心都依赖登录态，缓存要作废重来。
		// 尤其后者：换账号后若还挂着上一位账号的档位和到期时间，等于把别人的
		// 会员信息展示在你面前。（player 不知道 appui 的页面状态，所以在 App 层做。）
		if a.PL.LoggedIn() != a.wasLoggedIn {
			a.wasLoggedIn = a.PL.LoggedIn()
			a.favLists = favListsState{}
			a.profile = profileState{}
		}
		a.syncSleep()
		a.syncTray()
		a.syncMedia()
		a.invalidate()
	})
	return a
}

// Close 应用退出时释放睡眠禁止并终止音频引擎。幂等，可同时挂 OnQuit 与 defer。
func (a *App) Close() {
	a.closeOnce.Do(func() {
		a.quitStarted.Store(true)
		if a.media != nil {
			a.media.Close()
		}
		a.closeHotkeys()
		a.sleep.close()
		a.PL.Close()
		if a.Covers != nil {
			a.Covers.Close()
		}
		if a.tray != nil {
			a.tray.Destroy()
			a.tray = nil
		}
	})
}

const uiRequestLimit = 6

// withAPILimit 在 UI 侧 HTTP 调用外加一个全局并发闸门，限制响应解析和
// 临时对象同时占用的内存。闸门初始化是幂等的，测试中直接构造 App 也可用。
func (a *App) withAPILimit(fn func()) {
	a.apiOnce.Do(func() { a.apiSlots = make(chan struct{}, uiRequestLimit) })
	a.apiSlots <- struct{}{}
	defer func() { <-a.apiSlots }()
	fn()
}

// syncSleep 只在「设置开启 + 音频引擎实际出声」时持有系统睡眠禁止。
// 暂停、停止、载入中和关闭开关都会立即释放。
func (a *App) syncSleep() {
	want := a.Conf != nil && a.Conf.Bool(sleepInhibitKey, true) && a.PL.PlayingAudio()
	a.sleep.set(want)
}

// Attach 绑定窗口：接上重绘通知、启动播放器与登录态刷新、跑自检钩子。
func (a *App) Attach(win *mygo.Window) {
	a.Win = win
	// 首帧前窗口已经存在；先按配置铺底色，避免深色模式下等首帧时闪
	// 一下白底。MyGo 的首帧随后覆盖，设置页切主题时会再次同步。
	a.ApplyWindowBackground()
	// 系统标题栏/Alt+F4 走 OnClose，与自绘关闭按钮共用「关闭窗口行为」设置。
	// 真正退出时 OnBeforeQuit 已置 quitStarted，放行原生关闭流程。
	win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitStarted.Load() {
			return
		}
		e.PreventDefault()
		a.closeWindow()
	})
	// 启动画面从窗口就绪起算（与帧时钟同为 wall clock，首帧即 0.00s）。
	a.splashAt = time.Now()
	a.PL.Boot()
	a.PL.RefreshUser()
	a.setupTray()
	// 全局快捷键必须等应用就绪才能注册（MyGo 在 App.Run 之前会直接 panic），
	// Attach 正好在 WhenReady 之后。
	a.syncHotkeys()
	// SMTC 需要窗口句柄；MPRIS 忽略此调用。
	if a.media != nil {
		a.media.AttachWindow(win.NativeHandle())
	}
	a.maybeSelfCheck()
	a.maybeAutoCheckUpdate()
}

// ApplyWindowBackground 让原生窗口的启动底色跟上配置的明暗。
func (a *App) ApplyWindowBackground() error {
	if a.Win == nil || a.Conf == nil {
		return nil
	}
	return a.Win.SetBackgroundColor(WindowBackground(a.Conf))
}

// WindowBackground 返回启动首帧前的窗口底色：强制浅/深色按应用调色板，
// 跟随系统时交给 MyGo 的 light-dark（原生侧据此解析当前桌面外观）。
func WindowBackground(store *conf.Store) string {
	if store == nil {
		return "light-dark(#f3efe6, #101412)"
	}
	switch store.String("Style.Theme", "system") {
	case "light":
		return "#f3efe6"
	case "dark":
		return "#101412"
	default:
		return "light-dark(#f3efe6, #101412)"
	}
}

// View 是窗口根视图。
func (a *App) View(c *ui.Context) {
	t := a.buildTheme(c)
	c.SetTheme(t)
	// 每帧推进路由入场时钟（页面里没有入场区块时也要走，否则路径变化会被漏掉）。
	a.routeEnter(c)

	// 录制态要有尽头：离开「设置 → 快捷键」这一页、或者按下新键之前超时，
	// 都要收回去——录制中会整段压住窗口内快捷键，放着不管等于把它们全关掉。
	recording := a.expireRecording(c)
	// 窗口内快捷键（全局那条由系统直接回调，不经过这里）。
	if !recording {
		a.localHotkeys(c)
	}

	if a.npOpen && c.Shortcut(0, ui.KeyEscape) {
		// 浮层从后往前收：队列浮窗是后开的，先收它，再按一次才关整页。
		if a.queueOpen {
			a.queueOpen = false
			a.Conf.Set("Window.QueueOpen", false)
		} else {
			a.npOpen = false
		}
	}

	// 电台没有可编辑队列：状态一旦生效就收起队列停靠/浮窗。
	if a.PL.RadioActive() && a.queueOpen {
		a.queueOpen = false
		a.Conf.Set("Window.QueueOpen", false)
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
				// 队列浮窗建在正在播放页之后：绝对子元素按建树顺序绘制、
				// 也按同一顺序命中，晚建的才盖得住整页、也才收得到点击。
				if a.queueOpen {
					a.queueFloat(c, t)
				}
			}
		})
		a.playerBar(c, t)
	})

	if a.pendingToast != "" {
		c.Toast(a.pendingToast)
		a.pendingToast = ""
	}

	// 启动画面盖在最上，播完自动撤层；Escape 跳过剩余动画（见 splash.go）。
	if a.splashShow(c) {
		if c.Shortcut(0, ui.KeyEscape) {
			a.splashAt = c.Now().Add(-splashFadeDelay)
		}
		a.splash(c)
	}
}

func (a *App) mainRow(c *ui.Context) {
	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.topBar(c)
		ui.Row(c).Grow(1).FillWidth().AlignItems(ui.Stretch).Children(func() {
			a.routeArea(c)
			// 正在播放页会把内容区整页盖住，队列这时改走浮窗
			//（queueFloat）：停靠栏建了也看不见，只会白占一次列表状态。
			if a.queueOpen && !a.npOpen {
				a.queuePanel(c)
			}
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
		case r.Match("/playlists"):
			r.Title("歌单")
			a.playlistsView(c)
		case r.Match("/favlists"):
			r.Title("收藏的歌单")
			a.favListsView(c)
		case r.Match("/toplist/{id}"):
			r.Title("排行榜")
			id, _ := strconv.ParseInt(r.Param("id"), 10, 64)
			a.toplistView(c, id)
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
		case r.Match("/profile"):
			r.Title("个人中心")
			a.profileView(c)
		default:
			ui.Text(c, "页面不存在").TextColor(c.Theme().TextMuted).Padding(24)
		}
	})
}

// ===== 顶部菜单 =====

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
	{"/playlists", "list", "歌单"},
}

// 顶栏几何。导航与右侧工具按钮共用接近的点击盒与图标尺寸，
// 它们的中线因此天然落在同一条水平线上。
const (
	topBarH       = 46.0
	topBtnSize    = 32.0
	topNavSize    = 34.0
	topIconSize   = 17.0
	topAvatarSize = 26.0
	macDotSize    = 12.0
	macDotHit     = 20.0
)

// macTrafficOnLeft 报告自绘窗口按钮是否按 macOS HIG 放在左边。
// 单独成函数便于在没有 macOS 构建的测试里断言这条平台约定。
func macTrafficOnLeft(goos string) bool { return goos == "darwin" }

// backOrBrand 画返回按钮；没有历史时退回品牌音符。顶栏只此一处品牌标记。
func (a *App) backOrBrand(c *ui.Context, t *ui.Theme) {
	if a.Router.CanGoBack() {
		a.topIconBtn(c, "back", "返回", func() { a.Router.Back() })
		return
	}
	ui.Icon(c, Icons["note"]).FontSize(fz(topIconSize)).AlignSelf(ui.Center).TextColor(t.Accent)
}

// topNavItem 是顶部菜单的一项：只有图标，功能名放在 tooltip 与无障碍名里。
func (a *App) topNavItem(c *ui.Context, t *ui.Theme, it navItem) {
	active := a.Router.Path() == it.path
	b := ui.ButtonBase(c).Size(topNavSize, topNavSize).Radius(9).Center().Transition(hoverFade)
	b.Children(func() {
		size := fz(topIconSize)
		// ∞ 在 24 网格里只有 10 单位高，按常规 17px 画会比邻居矮一截；
		// 单独放大到 21px，视觉重量才和日历/红心一致。
		if it.icon == "guess" {
			size = fz(21)
		}
		ui.Icon(c, Icons[it.icon]).FontSize(size).AlignSelf(ui.Center)
	})
	b.Tooltip(it.label)
	b.Label(it.label)
	if active {
		b.Background(t.Accent.Alpha(0.13))
		b.TextColor(t.Accent)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		a.searchOpen = false
		a.Router.Push(it.path)
	}
}

// topIconBtn 是顶栏的纯图标按钮（悬停底色 + tooltip + 无障碍名）。
func (a *App) topIconBtn(c *ui.Context, icon, tip string, onClick func()) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(topBtnSize, topBtnSize).Radius(8).Center().Transition(hoverFade)
	b.Children(func() {
		ui.Icon(c, Icons[icon]).FontSize(fz(topIconSize)).AlignSelf(ui.Center)
	})
	b.Tooltip(tip)
	b.Label(tip)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		onClick()
	}
	return b
}

// searchButton 是搜索入口：按钮 + 下拉浮窗，浮窗里才放输入框。
func (a *App) searchButton(c *ui.Context, t *ui.Theme) {
	b := a.topIconBtn(c, "search", "搜索", func() { a.searchOpen = !a.searchOpen })
	if a.searchOpen {
		b.Background(t.Accent.Alpha(0.13))
		b.TextColor(t.Accent)
	}
	ui.PopoverBase(c, b, &a.searchOpen, func(panel ui.Element) {
		// 右缘对齐搜索按钮，320 宽的浮窗才不会被窗口右缘裁掉。
		panel.AttachTo(b, ui.AnchorBottomRight, ui.AnchorTopRight).Margin(8, 0, 0, 0)
		panel.Width(320).Padding(10).Background(t.Surface).Radius(12).
			Border(1, t.Border).Shadow(0, 10, 32, 0, ui.RGBA(0, 0, 0, 0.18))
		panel.Children(func() {
			sf := ui.SearchField(c, &a.searchDraft).FillWidth().AutoFocus()
			sf.Label("搜索音乐")
			sf.Placeholder("搜索音乐")
			if sf.Submitted() {
				kw := strings.TrimSpace(a.searchDraft)
				if kw != "" {
					a.searchOpen = false
					a.Router.Push("/search?kw=" + kw)
				}
			}
		})
	})
}

// avatarButton 是右上角的用户入口：登录后进个人中心，未登录进登录页。
func (a *App) avatarButton(c *ui.Context, t *ui.Theme) {
	me := a.PL.Me()
	b := ui.ButtonBase(c).Size(topNavSize, topNavSize).Radius(999).Center().Transition(hoverFade)
	if !a.PL.LoggedIn() {
		b.Children(func() {
			ui.Icon(c, Icons["user"]).FontSize(fz(topIconSize)).AlignSelf(ui.Center)
		})
		b.Tooltip("登录")
		b.Label("登录")
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		if b.Clicked() {
			a.searchOpen = false
			a.Router.Push("/login")
		}
		return
	}
	ava := a.Covers.Get(me.Avatar, a.invalidate)
	b.Children(func() {
		ui.Avatar(c, me.Name, ava).Size(topAvatarSize, topAvatarSize)
	})
	b.Tooltip("个人中心")
	b.Label("个人中心")
	if a.Router.Path() == "/profile" {
		b.Background(t.Accent.Alpha(0.13))
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		a.searchOpen = false
		a.Router.Push("/profile")
	}
}

// macWindowButtons 按 macOS HIG 把交通灯放在左上：关、最小化、缩放。
// 自绘 CSD 时它们只是三枚圆点，不依赖系统标题栏。
func (a *App) macWindowButtons(c *ui.Context) {
	if a.Win == nil {
		return
	}
	row := ui.Row(c).AlignItems(ui.Center).Gap(8)
	row.Children(func() {
		a.macTrafficLight(c, "关闭", "#ff5f57", "#e0443e", "×", a.closeWindow)
		a.macTrafficLight(c, "最小化", "#febc2e", "#d89e24", "−", func() { a.Win.Minimize() })
		a.macTrafficLight(c, "缩放", "#28c840", "#1aab29", "+", func() { a.Win.ToggleMaximize() })
	})
}

// macTrafficLight 画一枚交通灯圆点；悬停时显示 HIG 的符号。
func (a *App) macTrafficLight(c *ui.Context, tip, fill, ring, glyph string, fn func()) {
	b := ui.ButtonBase(c).Size(macDotHit, macDotHit).Radius(999).Center()
	b.Children(func() {
		hovered := b.Hovered()
		ui.Box(c).Size(macDotSize, macDotSize).Radius(999).
			Background(ui.Hex(fill)).Border(1, ui.Hex(ring)).Center().Children(func() {
			if hovered {
				ui.Text(c, glyph).FontSize(fz(9)).FontWeight(800).
					TextColor(ui.RGBA(0, 0, 0, 0.62)).AlignSelf(ui.Center)
			}
		})
	})
	b.Tooltip(tip)
	b.Label(tip)
	if b.Clicked() {
		fn()
	}
}

// panelBg 是侧边面板/队列停靠栏的底色（与窗口底色区分开）。
func panelBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#151a16")
	}
	return ui.Hex("#e9e3d7")
}

// ===== 顶栏 =====

func (a *App) topBar(c *ui.Context) {
	t := c.Theme()
	bar := ui.Row(c).FillWidth().Height(topBarH).Padding(0, 14).Gap(6).
		AlignItems(ui.Center).DragWindow()
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border.Alpha(0.65), 0)
	})
	bar.Children(func() {
		// 左组：macOS 的交通灯在最左（HIG），随后是返回/品牌图标。
		// 左右两组等权 Grow，导航组因此落在窗口真正的中线上。
		left := ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(8)
		left.Children(func() {
			if macTrafficOnLeft(runtime.GOOS) {
				a.macWindowButtons(c)
				ui.Box(c).Width(2)
			}
			a.backOrBrand(c, t)
		})

		nav := ui.Row(c).AlignItems(ui.Center).Gap(2)
		nav.Children(func() {
			for _, it := range navItems {
				a.topNavItem(c, t, it)
			}
		})

		// 右组：搜索、设置、头像。非 macOS 再跟上自绘 CSD 按钮；
		// macOS 的窗口按钮已经在左边，设置与头像留在最右。
		right := ui.Row(c).Grow(1).Justify(ui.End).AlignItems(ui.Center).Gap(2)
		right.Children(func() {
			a.searchButton(c, t)
			a.topIconBtn(c, "settings", "设置", func() {
				a.searchOpen = false
				a.Router.Push("/settings")
			})
			a.avatarButton(c, t)
			if !macTrafficOnLeft(runtime.GOOS) {
				ui.Box(c).Width(6)
				a.windowButtons(c)
			}
		})
	})
}

func (a *App) windowButtons(c *ui.Context) {
	// Win 为 nil 时（无头测试/启动早期）不能碰窗口 API：那些调用会被转发到
	// 主线程执行，没有主线程就会永远等下去。
	if a.Win == nil {
		return
	}
	if !a.reproCloseAt.IsZero() && time.Now().After(a.reproCloseAt) {
		a.reproCloseAt = time.Time{}
		a.closeWindow()
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
	mk("winClose", "关闭", true, a.closeWindow)
}

// invalidate 是给异步回调用的重绘入口。
func (a *App) invalidate() {
	if a.Win != nil {
		a.Win.Update(func() {})
	}
}

// isCreatedPlaylist 报告 id 是否是当前登录用户自建的歌单。
// 自建歌单在歌单页列表里（PL.Playlists），tid 即路由参数 id。
func (a *App) isCreatedPlaylist(id int64) bool {
	for _, pl := range a.PL.Playlists() {
		if pl.ID == id {
			return true
		}
	}
	return false
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
			var res struct {
				Songs []backend.Song `json:"songs"`
			}
			var err error
			a.withAPILimit(func() { res, err = a.API.RecommendNewsong() })
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
	if os.Getenv("QUAVER_REPRO_CLOSE_WINDOW") == "1" {
		go func() {
			time.Sleep(4 * time.Second)
			a.update(func() {
				a.npOpen = false
				a.reproCloseAt = time.Now().Add(100 * time.Millisecond)
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
