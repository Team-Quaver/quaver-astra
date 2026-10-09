package appui

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"unicode"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
	"golang.org/x/image/draw"
)

// 对齐 Quaver Astra 的托盘文案与曲目行口径。
const (
	trayProductName       = "Quaver Astra"
	trayIdleLabel         = "未在播放"
	trayTitleMaxCols      = 40
	trayTitleEllipsis     = "…"
	trayTrackSeparator    = " - "
	trayArtistSeparator   = " / "
	trayWindowToggleLabel = "显示/隐藏 " + trayProductName
)

type trayMenu struct {
	track  *mygo.MenuItem
	prev   *mygo.MenuItem
	toggle *mygo.MenuItem
	next   *mygo.MenuItem
	off    *mygo.MenuItem
	all    *mygo.MenuItem
	one    *mygo.MenuItem
	shuf   *mygo.MenuItem
}

// buildTrayMenu 构造对齐 Quaver Astra 的托盘菜单。抽成纯构造便于锁住
// 项目顺序、中文文案与单选/勾选类型。
func (a *App) buildTrayMenu() (*mygo.Menu, *trayMenu) {
	menu := &trayMenu{
		track:  &mygo.MenuItem{Label: trayIdleLabel, Disabled: true},
		prev:   &mygo.MenuItem{Label: "上一曲", Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.Prev() }},
		toggle: &mygo.MenuItem{Label: "播放", Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.PlayOrPause() }},
		next:   &mygo.MenuItem{Label: "下一曲", Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.Next(false) }},
		off:    &mygo.MenuItem{Label: "顺序播放", Type: mygo.MenuItemRadio, Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.SetMode("off") }},
		all:    &mygo.MenuItem{Label: "列表循环", Type: mygo.MenuItemRadio, Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.SetMode("all") }},
		one:    &mygo.MenuItem{Label: "单曲循环", Type: mygo.MenuItemRadio, Click: func(*mygo.MenuItem, *mygo.Window) { a.PL.SetMode("one") }},
		shuf:   &mygo.MenuItem{Label: "随机播放", Type: mygo.MenuItemCheckbox, Click: func(it *mygo.MenuItem, _ *mygo.Window) { a.PL.SetShuffle(it.IsChecked()) }},
	}
	root := mygo.NewMenu([]*mygo.MenuItem{
		menu.track,
		menu.prev,
		menu.toggle,
		menu.next,
		{Label: "循环模式", Submenu: []*mygo.MenuItem{menu.off, menu.all, menu.one, mygo.Separator(), menu.shuf}},
		mygo.Separator(),
		{Label: trayWindowToggleLabel, Click: func(*mygo.MenuItem, *mygo.Window) { a.toggleWindow() }},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
	return root, menu
}

// setupTray 创建托盘。Linux 缺少 AppIndicator 时只记录告警，不影响主窗口；
// 播放控制仍可从播放条使用。
func (a *App) setupTray() {
	silenceAppIndicatorDeprecationWarning()
	root, menu := a.buildTrayMenu()
	icon, err := trayIcon(mygo.Theme.IsDark())
	if err != nil {
		fmt.Fprintf(os.Stderr, "appui: 托盘图标生成失败（%v）\n", err)
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           icon,
		IconIsTemplate: false,
		ToolTip:        trayProductName,
		Menu:           root,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "appui: 托盘不可用（%v）\n", err)
		return
	}
	a.tray = tray
	a.trayItems = menu
	// Windows/macOS 可区分左右键：左键切换窗口，右键自然弹菜单。
	// Linux AppIndicator 只提供菜单，因此保留菜单里的显示/隐藏入口。
	tray.OnClick(a.toggleWindow)
	a.syncTray()
}

// trayView 是托盘菜单一次刷新所需的最小快照。
type trayView struct {
	track   string
	enabled bool
	playing bool
	mode    string
	shuf    bool
}

func (a *App) traySnapshot() trayView {
	queue := a.PL.Queue()
	cur, has := a.PL.Current()
	v := trayView{
		track:   trayIdleLabel,
		enabled: len(queue) > 0,
		playing: a.PL.Playing(),
		mode:    a.PL.Mode(),
		shuf:    a.PL.Shuffle(),
	}
	if has {
		v.track = trayTitleLine(cur)
	}
	return v
}

// syncTray 把 player 的状态投影到原生菜单。通知高频到达，只有可见状态变化时
// 才调用 MyGo 的菜单更新接口。
func (a *App) syncTray() {
	if a.trayItems == nil {
		return
	}
	v := a.traySnapshot()
	sig := fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%t", v.track, v.enabled, v.playing, v.mode, v.shuf)
	a.update(func() {
		if sig == a.traySig {
			return
		}
		a.traySig = sig
		it := a.trayItems
		it.track.SetLabel(v.track)
		it.prev.SetEnabled(v.enabled)
		it.next.SetEnabled(v.enabled)
		if v.playing {
			it.toggle.SetLabel("暂停")
		} else {
			it.toggle.SetLabel("播放")
		}
		it.toggle.SetEnabled(v.enabled)
		it.off.SetChecked(v.mode == "off")
		it.all.SetChecked(v.mode == "all")
		it.one.SetChecked(v.mode == "one")
		it.shuf.SetChecked(v.shuf)
	})
}

// toggleWindow 是托盘左键/菜单「显示或隐藏」共用入口。
func (a *App) toggleWindow() {
	if a.Win == nil || a.Win.IsDestroyed() {
		return
	}
	if a.Win.IsVisible() {
		a.Win.Hide()
		return
	}
	if a.Win.IsMinimized() {
		a.Win.Restore()
	}
	a.Win.Show()
	a.Win.Focus()
}

// closeWindow 按设置处理窗口关闭：默认收进托盘，也可直接退出应用。
// 没有托盘时无法「收到托盘」，退回真正的关闭。
func (a *App) closeWindow() {
	if a.Win == nil || a.Win.IsDestroyed() {
		return
	}
	action := "tray"
	if a.Conf != nil {
		action = a.Conf.String("Window.CloseAction", "tray")
	}
	if action == "quit" {
		mygo.App.Quit()
		return
	}
	if a.tray != nil {
		a.Win.Hide()
		return
	}
	a.quitStarted.Store(true)
	a.Win.Close()
}

// trayTitleLine 生成「歌名 - 歌手」；无曲目返回占位。这里沿用 songTitle：
// Title 是主名+版本后缀，Subtitle 是说明文字，不进托盘标题。
func trayTitleLine(song player.Song) string {
	name := oneLine(song.DisplayName())
	if name == "" {
		return trayIdleLabel
	}
	names := make([]string, 0, len(song.Singers))
	for _, singer := range song.Singers {
		if s := oneLine(singer.Name); s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 && song.Artists != "" {
		for _, s := range strings.Split(song.Artists, "/") {
			if s = oneLine(s); s != "" {
				names = append(names, s)
			}
		}
	}
	full := name
	if len(names) > 0 {
		full += trayTrackSeparator + strings.Join(names, trayArtistSeparator)
	}
	return clampDisplayCols(full, trayTitleMaxCols)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// displayCols 按 East Asian Width 简化口径量宽：CJK/全角/emoji 记 2 列。
// 原生菜单宽度跟像素走，不能用 rune 数量近似中文标题。
func displayCols(s string) int {
	n := 0
	for _, r := range s {
		n += runeCols(r)
	}
	return n
}

func runeCols(r rune) int {
	if unicode.IsControl(r) {
		return 0
	}
	if r >= 0x1100 &&
		(r <= 0x115f ||
			(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
			(r >= 0xac00 && r <= 0xd7a3) ||
			(r >= 0xf900 && r <= 0xfaff) ||
			(r >= 0xfe30 && r <= 0xfe4f) ||
			(r >= 0xff00 && r <= 0xff60) ||
			(r >= 0xffe0 && r <= 0xffe6) ||
			(r >= 0x1f300 && r <= 0x1faff) ||
			(r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

// clampDisplayCols 超宽时保留开头并在尾巴补省略号；省略号自身占一列。
func clampDisplayCols(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if displayCols(s) <= max {
		return s
	}
	if max <= displayCols(trayTitleEllipsis) {
		return trayTitleEllipsis
	}
	budget := max - displayCols(trayTitleEllipsis)
	out := make([]rune, 0, len(s))
	used := 0
	for _, r := range s {
		w := runeCols(r)
		if used+w > budget {
			break
		}
		out = append(out, r)
		used += w
	}
	trimmed := strings.TrimRightFunc(string(out), func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '/'
	})
	return trimmed + trayTitleEllipsis
}

//go:embed assets/tray-icon-dark-shell.png
var trayIconDarkShell []byte

//go:embed assets/tray-icon-light-shell.png
var trayIconLightShell []byte

// trayIcon 把随包 256px 母图缩到 32px（16pt @2x）。深色外壳用浅色图，
// 浅色外壳用深色图；macOS 后端还会按菜单栏高度再归一。
func trayIcon(darkShell bool) ([]byte, error) {
	srcData := trayIconLightShell
	if darkShell {
		srcData = trayIconDarkShell
	}
	src, err := png.Decode(bytes.NewReader(srcData))
	if err != nil {
		return nil, err
	}
	dst := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
