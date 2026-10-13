package appui

import (
	"runtime"

	"github.com/egoist/mygo/ui"
)

const (
	// hotkeyActionCol 是动作名列宽；hotkeyClearCol 是解绑按钮列宽。
	// 两列定宽，绑定按钮才能对齐成两条竖线，不随文案长度跳动。
	// 动作列还要放一句「单向动作」的说明：给到 150 才不至于折成三行，
	// 把整行拉高、五行表格变成高低不齐。
	hotkeyActionCol = 150.0
	hotkeyClearCol  = 26.0
	// hotkeyCellMin 是绑定按钮的最小宽度：「媒体播放/暂停键」不短，
	// 太窄会被省略号吃掉，用户就看不出自己绑了什么。
	hotkeyCellMin = 150.0
)

// hotkeysSettings 是「设置 → 快捷键」：全局开关 + 一张两列绑定表。
//
// 交互口径与主流播放器一致：点一下绑定格进入录制，按下新的组合键即改，
// Esc 取消，右侧小叉解绑——不让用户手写 Electron 风格的 accelerator 串。
func (a *App) hotkeysSettings(c *ui.Context, t *ui.Theme) {
	// 内容比窗口高是常态（六行绑定 + 两段说明），必须可滚：
	// 不能靠用户把窗口拉到最大才能点到「恢复默认」。
	body := ui.Scroll(c).Grow(1).FillWidth().Gap(16).MaxWidth(680)
	a.enterBlock(c, 0, body)
	body.Children(func() {
		a.hotkeyGlobalSwitch(c, t)

		ui.Divider(c)

		a.hotkeyTableHeader(c, t)
		for i, spec := range hotkeySpecs {
			a.hotkeyBindRow(c, t, spec, i)
		}

		ui.Divider(c)
		a.hotkeyHints(c, t)

		plainBtn(c, "恢复默认快捷键", func() { a.resetHotkeys() })
	})
}

// hotkeyGlobalSwitch 是全局快捷键总开关与其机制说明。
func (a *App) hotkeyGlobalSwitch(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, "全局快捷键").FontSize(fz(14)).FontWeight(700)
		a.prefRow(c, "启用全局快捷键", a.hotkeyEnabled, func(v bool) {
			if a.Conf != nil {
				a.Conf.Set(hotkeyEnabledKey, v)
			}
			a.syncHotkeys()
		})
		ui.Text(c, hotkeyPlatformNote()).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(4)
	})
}

// hotkeyPlatformNote 说明三个平台各自怎么拿到全局快捷键，以及已知的边界。
func hotkeyPlatformNote() string {
	switch runtime.GOOS {
	case "darwin":
		return "全局快捷键在 Astra 不在前台时同样生效。macOS 的媒体键由系统直接送给「正在播放」的那个播放器，Astra 已经接住（见「设置 → 关于」的媒体中心），所以全局一列出厂留空；要自己绑一个的话点绑定格按下组合键即可。与系统或其他应用撞车的组合键会在下表标出来。"
	case "windows":
		return "全局快捷键走系统 RegisterHotKey，Astra 收进托盘后照样生效。与系统或其他应用撞车的组合键会在下表标出来，换一个即可。"
	default:
		return "X11 会话由 Astra 自己抓取按键；Wayland 会话经 XDG 桌面门户注册（KDE Plasma 6、GNOME 48 及以上支持），需要先按 deb/rpm/pacman/AppImage 安装好，让桌面读到 Quaver Astra 的 desktop entry。门户可能请你确认或改键；被桌面拒绝时 Astra 收不到回执，只在日志里留一行。"
	}
}

// hotkeyTableHeader 是绑定表表头，列宽与 hotkeyBindRow 一致。
func (a *App) hotkeyTableHeader(c *ui.Context, t *ui.Theme) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "动作").FontSize(fz(12)).TextColor(t.TextMuted).Width(hotkeyActionCol)
		ui.Text(c, "全局（后台也生效）").FontSize(fz(12)).TextColor(t.TextMuted).Grow(1)
		ui.Text(c, "窗口内（需聚焦 Astra）").FontSize(fz(12)).TextColor(t.TextMuted).Grow(1)
	})
}

// hotkeyBindRow 是一行：动作名 + 全局绑定格 + 窗口内绑定格。
func (a *App) hotkeyBindRow(c *ui.Context, t *ui.Theme, spec hotkeySpec, idx int) {
	row := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(8)
	a.enterBlock(c, idx+1, row)
	row.Children(func() {
		label := ui.Column(c).Width(hotkeyActionCol).Gap(2)
		label.Children(func() {
			ui.Text(c, spec.label).FontSize(fz(13))
			if spec.hint != "" {
				ui.Text(c, spec.hint).FontSize(fz(10.5)).TextColor(t.TextMuted).MaxLines(3)
			}
		})
		a.hotkeyBindCell(c, t, scopeGlobal, spec)
		a.hotkeyBindCell(c, t, scopeLocal, spec)
	})
}

// hotkeyBindCell 是一格的纵向小组：上面「绑定按钮 + 解绑」，下面问题提示。
// 提示必须在同一列里，否则它会变成行的第三个兄弟，把表格挤歪。
func (a *App) hotkeyBindCell(c *ui.Context, t *ui.Theme, scope hotkeyScope, spec hotkeySpec) {
	col := ui.Column(c).Grow(1).Gap(3)
	col.Children(func() {
		a.hotkeyBindButton(c, t, scope, spec)
		if note := a.hotkeyNote(scope, spec); note != "" {
			ui.Text(c, note).FontSize(fz(10.5)).TextColor(ui.Hex("#e81123")).MaxLines(3)
		}
	})
}

// hotkeyBindButton 渲染一格绑定，外加它的解绑按钮。
func (a *App) hotkeyBindButton(c *ui.Context, t *ui.Theme, scope hotkeyScope, spec hotkeySpec) {
	recording := a.recording(scope, spec)
	shown := hotkeyDisplay(a.hotkeyRaw(scope, spec))

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
		cell := ui.ButtonBase(c).Key(hotkeyConfKey(scope, spec.suffix)).
			Padding(6, 10).Radius(8).Border(1, t.Border).AlignItems(ui.Center).Gap(6).
			Grow(1).MinWidth(hotkeyCellMin).Transition(hoverFade).
			Label(hotkeyBindLabel(scope, spec))
		if !recording && cell.Hovered() {
			cell.Background(t.SurfaceHover)
		}
		if recording {
			cell.Background(t.Accent.Alpha(0.14))
			cell.Border(1, t.Accent)
			// Focus 每帧调用，把键盘留在这格上：HandleInput 只在元素
			// 持有键盘焦点时收得到按键。
			cell.Focus()
			cell.HandleInput(func(ev ui.InputEvent) bool { return a.captureHotkey(scope, spec, ev) })
		}

		cell.Children(func() {
			switch {
			case recording:
				ui.Text(c, "按下新快捷键…").FontSize(fz(12)).FontWeight(600).TextColor(t.Accent).SingleLine().Ellipsis("…")
			case shown != "":
				ui.Text(c, shown).FontSize(fz(12)).FontWeight(600).SingleLine().Ellipsis("…")
			default:
				ui.Text(c, "未绑定").FontSize(fz(12)).TextColor(t.TextMuted).SingleLine()
			}
		})
		if !recording && cell.Clicked() {
			a.toggleRecording(scope, spec)
			a.invalidate() // 立刻重画：录制态要马上看起来像被按下了
		}

		// 解绑按钮：占位常驻（只在有绑定时画图标），避免表格随绑定左右跳动。
		x := ui.ButtonBase(c).Size(hotkeyClearCol, 24).Radius(6).Label(hotkeyClearLabel(scope, spec))
		if shown != "" && !recording {
			x.Children(func() {
				ui.Icon(c, Icons["clear"]).FontSize(fz(12)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			})
			if x.Hovered() {
				x.Background(t.SurfaceHover)
			}
			x.Tooltip(hotkeyClearLabel(scope, spec))
			if x.Clicked() {
				a.clearHotkey(scope, spec)
			}
		}
	})
}

// hotkeyBindLabel / hotkeyClearLabel 是给录制按钮与解绑按钮的可访问名：
// 同一行里「未绑定」在两列重复出现，读屏和 UI 测试都只能靠标签定位。
func hotkeyBindLabel(scope hotkeyScope, spec hotkeySpec) string {
	return scope.String() + "快捷键：" + spec.label
}

func hotkeyClearLabel(scope hotkeyScope, spec hotkeySpec) string {
	return "清除" + scope.String() + "快捷键：" + spec.label
}

// captureHotkey 是录制控件的按键入口，返回是否吃掉这次事件。
//
// 录制期间不能再让窗口快捷键把按键截走，所以 App.localHotkeys 在
// record 非空时整段跳过注册（见 hotkeys.go 与 mygo/ui/input.go 的 keyDown）。
// 只按修饰键或按到不支持的键时吞掉事件、继续等下一个键；Esc 取消。
func (a *App) captureHotkey(scope hotkeyScope, spec hotkeySpec, ev ui.InputEvent) bool {
	if !a.recording(scope, spec) {
		return false
	}
	if ev.Kind != ui.InputKeyDown {
		return true // 抬起/重复/文本事件一律吞掉，别漏给别的控件
	}
	if ev.Key == ui.KeyEscape && ev.Mods == 0 {
		a.hotkeys.record = nil
		return true
	}
	acc, err := hotkeyFromEvent(ev)
	if err != nil {
		return true
	}
	a.setHotkey(scope, spec, acc)
	return true
}

// hotkeyHints 是表尾的使用说明：冲突口径与媒体键限制。
func (a *App) hotkeyHints(c *ui.Context, t *ui.Theme) {
	// 两段说明各自成组（标题贴正文，组与组之间留白），不然第二条的标题
	// 会像是第一条正文的续行。
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, "同键时全局优先").FontSize(fz(12)).FontWeight(700)
			ui.Text(c, "组合键被系统截走之后，窗口就收不到它了。某条窗口内快捷键若与生效中的全局快捷键同键，会在上表标注，并且不会触发。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(3)
		})
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, "媒体键只能作全局快捷键").FontSize(fz(12)).FontWeight(700)
			ui.Text(c, "播放/暂停、上一曲、下一曲这类媒体键不作为普通按键送进窗口，只能绑到「全局」一列。macOS 上媒体键由系统直接送给「正在播放」的播放器，Astra 走同一套命令回接，因此全局一列出厂留空。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(3)
		})
	})
}
