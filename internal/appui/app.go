// Package appui 是 Quaver Astra 的原生 UI（MyGo ui 包）：视图是状态的函数。
package appui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/colorprobe"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// App 是整个窗口的应用状态；view 从它构建每一帧。
type App struct {
	Win    *mygo.Window
	API    *backend.Client
	Conf   *conf.Store
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

	npList    ui.ListState // 正在播放页歌词列表
	queueList ui.ListState // 播放队列列表

	// 各页面的数据态（历史上往返保留数据本身）
	home     homeState
	liked    songListState
	daily    songListState
	guess    guessState
	playlist map[int64]*playlistState
	search   searchState
	login    loginState
	settings settingsState
}

// New 组装应用（后端已由 main 启动）。
func New(base string, store *conf.Store) *App {
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
		case r.Match("/playlist/{id}"):
			r.Title("歌单")
			id, _ := strconv.ParseInt(r.Param("id"), 10, 64)
			a.playlistView(c, id)
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
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	w := float32(216)
	if a.sbCollapsed {
		w = 64
	}
	side := ui.Column(c).Width(w).PaddingY(10).Gap(2).Background(a.sideBg(t)).Clip()
	side.Children(func() {
		// 用户区
		me := a.PL.Me()
		loggedIn := a.PL.LoggedIn()
		userBtn := ui.ButtonBase(c).FillWidth().Height(48).Padding(0, 10).Margin(0, 10, 6, 10).Radius(10).AlignItems(ui.Center).Gap(10)
		if loggedIn {
			ava := a.Covers.Get(me.Avatar, a.invalidate)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				if a.sbCollapsed {
					ui.Avatar(c, me.Name, ava).AlignSelf(ui.Center)
				} else {
					ui.Avatar(c, me.Name, ava)
					ui.Column(c).Grow(1).Gap(0).Children(func() {
						ui.Text(c, me.Name).FontSize(13).FontWeight(600).SingleLine().Ellipsis("…")
						if me.VipLabel != "" {
							ui.Text(c, me.VipLabel).FontSize(10).TextColor(t.Accent)
						}
					})
				}
			})
			if userBtn.Clicked() {
				a.Router.Push("/settings")
			}
		} else {
			userBtn.Children(func() {
				ui.Icon(c, Icons["user"]).FontSize(20).AlignSelf(ui.Center)
				if !a.sbCollapsed {
					ui.Text(c, "点击登录").FontSize(13).TextColor(t.TextMuted)
				}
			})
			if userBtn.Clicked() {
				a.Router.Push("/login")
			}
		}

		for _, it := range navItems {
			a.sideNavItem(c, t, it)
		}

		// 歌单列表
		pls := a.PL.Playlists()
		if len(pls) > 0 && !a.sbCollapsed {
			ui.Text(c, "我创建的歌单").FontSize(11).TextColor(t.TextMuted).Padding(10, 12, 4, 14)
		}
		for _, pl := range pls {
			path := fmt.Sprintf("/playlist/%d", pl.ID)
			btn := ui.ButtonBase(c).FillWidth().Height(36).Padding(0, 10).MarginX(10).Radius(8).AlignItems(ui.Center).Gap(10)
			active := a.Router.Path() == path
			btn.Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(16).AlignSelf(ui.Center)
				if !a.sbCollapsed {
					ui.Text(c, pl.Title).FontSize(13).SingleLine().Ellipsis("…").Grow(1)
				}
			})
			a.styleSideBtn(btn, active, c)
			if btn.Clicked() {
				a.Router.Push(path)
			}
		}

		ui.Spacer(c)

		// 底部：设置 + 收起
		foot := ui.Row(c).FillWidth().PaddingX(10).Gap(4).AlignItems(ui.Center)
		foot.Children(func() {
			gear := ui.ButtonBase(c).Size(36, 36).Radius(8).Center()
			gear.Children(func() { ui.Icon(c, Icons["settings"]).FontSize(18).AlignSelf(ui.Center) })
			if gear.Hovered() {
				gear.Background(t.SurfaceHover)
			}
			if gear.Clicked() {
				a.Router.Push("/settings")
			}
			col := ui.ButtonBase(c).Size(36, 36).Radius(8).Center()
			col.Children(func() {
				ic := Icons["collapse"]
				if a.sbCollapsed {
					ic = Icons["expand"]
				}
				ui.Icon(c, ic).FontSize(18).AlignSelf(ui.Center)
			})
			if col.Hovered() {
				col.Background(t.SurfaceHover)
			}
			if col.Clicked() {
				a.sbCollapsed = !a.sbCollapsed
				a.Conf.Set("Window.SidebarCollapsed", a.sbCollapsed)
			}
		})
	})
}

func (a *App) sideNavItem(c *ui.Context, t *ui.Theme, it navItem) {
	active := a.Router.Path() == it.path
	btn := ui.ButtonBase(c).FillWidth().Height(40).Padding(0, 10).MarginX(10).Radius(8).AlignItems(ui.Center).Gap(10)
	btn.Children(func() {
		ui.Icon(c, Icons[it.icon]).FontSize(18).AlignSelf(ui.Center)
		if !a.sbCollapsed {
			ui.Text(c, it.label).FontSize(13.5)
		}
	})
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
			back.Children(func() { ui.Icon(c, Icons["back"]).FontSize(17).AlignSelf(ui.Center) })
			if back.Hovered() {
				back.Background(t.SurfaceHover)
			}
			if back.Clicked() {
				a.Router.Back()
			}
		} else {
			ui.Icon(c, Icons["note"]).FontSize(17).AlignSelf(ui.Center).TextColor(t.Accent)
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
	t := c.Theme()
	mk := func(icon string, danger bool, fn func()) {
		b := ui.ButtonBase(c).Size(24, 24).Radius(12).Center()
		b.Children(func() { ui.Icon(c, Icons[icon]).FontSize(14).AlignSelf(ui.Center) })
		if b.Hovered() {
			if danger {
				b.Background(ui.Hex("#e81123"))
			} else {
				b.Background(t.SurfaceHover)
			}
		}
		if b.Clicked() && a.Win != nil {
			fn()
		}
	}
	mk("winMin", false, func() { a.Win.Minimize() })
	if a.Win.IsMaximized() {
		mk("winRestore", false, func() { a.Win.ToggleMaximize() })
	} else {
		mk("winMax", false, func() { a.Win.ToggleMaximize() })
	}
	mk("winClose", true, func() { a.Win.Close() })
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
		a.tint.set(r, mygo.Theme.IsDark())
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
