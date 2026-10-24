package appui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 快捷键分两个作用域，动作集合共用：
//
//   - 全局（scopeGlobal）：系统级注册，Astra 在后台甚至隐藏到托盘时照样生效。
//     Windows 用 RegisterHotKey、macOS 用 Carbon 热键、Linux X11 直接抓键，
//     ——这些都在 MyGo 的 mygo.GlobalShortcut 里，见 third_party/mygo/internal/
//     {windows/darwin/linux}/hotkey*.go。
//     Wayland 是刻意屏蔽的一档：应用抓不到键，「全局」只能由桌面持有，Astra
//     不去走 XDG GlobalShortcuts 门户自建一套，播放/上下曲/音量都交给桌面的
//     快捷键与系统媒体中心。判定与理由见 waylandSession。
//   - 窗口内（scopeLocal）：只在 Astra 拿着键盘焦点时生效，走 MyGo 原生 UI 的
//     ui.Context.Shortcut，由根视图每帧轮询（见 App.localHotkeys）。
//
// 两个作用域有一个绕不开的物理事实：全局注册会把按键从系统层面截走，
// 同一个组合键即使也绑到窗口内，窗口永远不会收到它。所以解析阶段就把
// 「撞车」判定出来并显示在设置页，而不是留一条点了没反应的绑定。

// hotkeyAction 是可绑定的播放动作。
type hotkeyAction string

const (
	hkPlayPause hotkeyAction = "playPause" // 播放/暂停（切换）
	hkPlay      hotkeyAction = "play"      // 只朝「播」走
	hkPause     hotkeyAction = "pause"     // 只朝「停」走
	hkPrev      hotkeyAction = "prev"
	hkNext      hotkeyAction = "next"
)

// hotkeyScope 是绑定的作用域。
type hotkeyScope uint8

const (
	scopeGlobal hotkeyScope = iota
	scopeLocal
)

// String 让作用域能直接进日志与错误文案。
func (s hotkeyScope) String() string {
	if s == scopeGlobal {
		return "全局"
	}
	return "窗口内"
}

// hotkeySpec 是一个动作的元数据：展示名、配置键后缀与两个作用域的出厂默认。
type hotkeySpec struct {
	action        hotkeyAction
	label         string
	suffix        string
	globalDefault string
	localDefault  string
	hint          string
}

// hotkeySpecs 的顺序就是设置页的展示顺序。
//
// 全局默认用媒体键：那是「播放暂停 / 上一曲 / 下一曲」在三个平台共同的语言，
// 不需要 Astra 在前台（Wayland 除外，见上）。窗口内用不了媒体键（MyGo 原生 UI 的 Key 集合里没有
// 它们，各平台也不把媒体键当普通按键送进窗口），所以默认给一组 Ctrl+Alt 组合，
// 并刻意不与全局默认撞车。逗号/句号取「< / >」的形状表示前后，同时避开
// Windows 上 Ctrl+Alt+方向键可能触发的显卡旋转热键。
var hotkeySpecs = []hotkeySpec{
	{action: hkPlayPause, label: "播放/暂停", suffix: "PlayPause", globalDefault: "MediaPlayPause", localDefault: "CmdOrCtrl+Alt+P"},
	{action: hkPlay, label: "播放", suffix: "Play", hint: "已在播放时不动作", globalDefault: "", localDefault: ""},
	{action: hkPause, label: "暂停", suffix: "Pause", hint: "已暂停或未播放时不动作", globalDefault: "", localDefault: ""},
	{action: hkPrev, label: "上一曲", suffix: "Prev", globalDefault: "MediaPreviousTrack", localDefault: "CmdOrCtrl+Alt+,"},
	{action: hkNext, label: "下一曲", suffix: "Next", globalDefault: "MediaNextTrack", localDefault: "CmdOrCtrl+Alt+."},
}

const (
	// hotkeyEnabledKey 是全局快捷键的总开关；窗口内快捷键不受它影响。
	hotkeyEnabledKey = "Hotkeys.Enabled"
	hotkeySection    = "Hotkeys."
)

// waylandSession 判断当前是否跑在 Wayland 会话里——是的话 Astra 不注册任何
// 全局快捷键，整个「全局」作用域交给桌面。
//
// 为什么屏蔽：Wayland 的安全模型不让应用抓键，想「后台也生效」只有两条路，
// 而两条都不是 Astra 该走的。一是 XDG GlobalShortcuts 门户：要求按安装包运行
// （桌面要能读到 app_id 对应的 desktop entry），可能弹确认框，被拒绝时没有
// 回执，等于把一堆「按了没反应还修不掉」的绑定留给用户；二是 i3/ sway 那类
// 按键绑定，属于桌面配置而不是应用配置。更关键的是，KDE/GNOME 本来就把
// 播放暂停、上下曲、音量这些媒体键送给当前活跃的播放器，Astra 经 MPRIS2
// 收得到（见 internal/smedia/mpris.go 与 appui.mediaCommands）。
// 所以这里的口径是：不自建一套和桌面抢键的全局快捷键，把设置完全交给桌面。
//
// 判定顺序：QAA_SESSION_TYPE（只为测试与排障保留，优先级最高）→ 显式的
// XDG_SESSION_TYPE（登录器写入，最权威）→ GDK_BACKEND=x11 → WAYLAND_DISPLAY。
//
// XDG_SESSION_TYPE 说的是「这个会话由谁持有键」，压得过 GDK_BACKEND：Wayland
// 会话里把 GTK 推到 X11 只是转去 XWayland，抓到的键也只在 XWayland 窗口
// 有焦点时生效，照样不是「后台也生效」。只有会话类型没写（裸起 X 会话、
// startx、某些嵌入式桌面）时，才拿 GDK_BACKEND 与 WAYLAND_DISPLAY 当线索。
func waylandSession() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	switch strings.ToLower(os.Getenv("QAA_SESSION_TYPE")) {
	case "wayland":
		return true
	case "x11":
		return false
	}
	switch strings.ToLower(os.Getenv("XDG_SESSION_TYPE")) {
	case "wayland":
		return true
	case "x11":
		return false
	}
	if strings.EqualFold(os.Getenv("GDK_BACKEND"), "x11") {
		return false
	}
	return os.Getenv("WAYLAND_DISPLAY") != ""
}

// init 处理一个平台差异：macOS 的 Carbon RegisterEventHotKey 只认 ANSI 虚拟键，
// 媒体键（播放/暂停、上一曲、下一曲）不在其中，注册必然失败；而 macOS 本来就
// 把媒体键送给「正在播放」的那个播放器，Astra 通过 MPRemoteCommandCenter
// 接住了这些命令（见 internal/smedia/nowplaying_darwin.go）。
// 所以在 macOS 上全局一列出厂留空，不留下三条按不了也修不掉的报错。
func init() {
	if runtime.GOOS != "darwin" {
		return
	}
	for i, spec := range hotkeySpecs {
		if strings.HasPrefix(spec.globalDefault, "Media") {
			hotkeySpecs[i].globalDefault = ""
		}
	}
}

// hotkeyConfKey 是一项绑定的配置键。
func hotkeyConfKey(scope hotkeyScope, suffix string) string {
	if scope == scopeGlobal {
		return hotkeySection + "Global." + suffix
	}
	return hotkeySection + "Local." + suffix
}

// hotkeySpecOf 按动作取元数据（设置页与提示文案都要用）。
func hotkeySpecOf(action hotkeyAction) (hotkeySpec, bool) {
	for _, spec := range hotkeySpecs {
		if spec.action == action {
			return spec, true
		}
	}
	return hotkeySpec{}, false
}

// ===== accelerator 解析 =====

// hkMod 是解析后的修饰键集合。位值与 MyGo 的 accelerator.Modifiers 一致。
type hkMod uint8

const (
	hkSuper hkMod = 1 << iota
	hkCtrl
	hkAlt
	hkShift
)

// hotkeyBinding 是一条解析完成的绑定。String 是规范形式，可直接交给
// mygo.GlobalShortcut.Register，也是去重/比对用的键。
type hotkeyBinding struct {
	mods hkMod
	// key 是规范键名：单个字符用小写（"a"、","），具名键用其本名（"Space"）。
	key string
}

// String 规范化成 MyGo accelerator 的写法：修饰键按 Super/Ctrl/Alt/Shift 排序，
// 单字符键大写。与内部包 accelerator.Accelerator.String() 逐字对齐，
// 于是同一条绑定在两个作用域里是同一个字符串。
func (b hotkeyBinding) String() string {
	var parts []string
	if b.mods&hkSuper != 0 {
		parts = append(parts, "Super")
	}
	if b.mods&hkCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.mods&hkAlt != 0 {
		parts = append(parts, "Alt")
	}
	if b.mods&hkShift != 0 {
		parts = append(parts, "Shift")
	}
	key := b.key
	if len([]rune(key)) == 1 {
		key = strings.ToUpper(key)
	}
	return strings.Join(append(parts, key), "+")
}

// hkNamedKeys 是具名键的别名表，与 MyGo internal/accelerator 的 namedKeys
// 对齐（那是内部包，不可 import，这里按同一套语法自己解一遍）。
var hkNamedKeys = map[string]string{
	"plus": "+", "space": "Space", "tab": "Tab", "capslock": "CapsLock",
	"numlock": "NumLock", "scrolllock": "ScrollLock", "backspace": "Backspace",
	"delete": "Delete", "del": "Delete", "insert": "Insert", "ins": "Insert",
	"return": "Enter", "enter": "Enter", "up": "Up", "down": "Down",
	"left": "Left", "right": "Right", "home": "Home", "end": "End",
	"pageup": "PageUp", "pagedown": "PageDown", "escape": "Escape",
	"esc": "Escape", "volumeup": "VolumeUp", "volumedown": "VolumeDown",
	"volumemute": "VolumeMute", "medianexttrack": "MediaNextTrack",
	"mediaprevioustrack": "MediaPreviousTrack", "mediastop": "MediaStop",
	"mediaplaypause": "MediaPlayPause", "printscreen": "PrintScreen",
	"numdec": "NumDec", "numadd": "NumAdd", "numsub": "NumSub",
	"nummult": "NumMult", "numdiv": "NumDiv",
}

// parseHotkey 解析一条 accelerator（"Ctrl+Alt+,"、"CmdOrCtrl+Shift+Space"）。
//
// 规则与 MyGo 的解析器一致：按 "+" 切，末段是键、其余全是修饰键，"++"
// 结尾表示键本身是加号。CmdOrCtrl 在本机展开成 Command（macOS）或 Control
// 并写进规范形式——配置里存的是展开后的字符串，去重比较才不会把
// "CmdOrCtrl+P" 与 "Ctrl+P" 看成两条不同的绑定。
func parseHotkey(raw string) (hotkeyBinding, error) {
	var b hotkeyBinding
	s := strings.TrimSpace(raw)
	if s == "" {
		return b, fmt.Errorf("未填写快捷键")
	}
	parts := splitAccelerator(s)
	for i, seg := range parts {
		p := strings.TrimSpace(seg)
		if p == "" {
			return b, fmt.Errorf("快捷键 %q 格式不正确", s)
		}
		last := i == len(parts)-1
		if m, ok := hotkeyModifier(p); ok && !last {
			if b.mods&m != 0 {
				return b, fmt.Errorf("快捷键 %q 里修饰键重复", s)
			}
			b.mods |= m
			continue
		}
		if !last {
			return b, fmt.Errorf("快捷键 %q 里的 %q 不是修饰键", s, p)
		}
		key, err := hotkeyName(p)
		if err != nil {
			return b, fmt.Errorf("快捷键 %q：%v", s, err)
		}
		b.key = key
	}
	if b.key == "" {
		return b, fmt.Errorf("快捷键 %q 缺少按键", s)
	}
	if b.mods == 0 && !hotkeyStandaloneOK(b.key) {
		return b, fmt.Errorf("快捷键 %q 至少要带一个修饰键（空格、回车、字母等会打字或抢焦点的键不能单绑）", s)
	}
	return b, nil
}

// splitAccelerator 按 "+" 切，末尾的 "++" 视作「加号键」。
func splitAccelerator(s string) []string {
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") {
		parts = append(parts[:len(parts)-2], "+")
	} else if s == "+" {
		parts = []string{"+"}
	}
	return parts
}

// hotkeyModifier 解析修饰键别名。
func hotkeyModifier(p string) (hkMod, bool) {
	switch strings.ToLower(p) {
	case "command", "cmd", "super", "meta", "win":
		return hkSuper, true
	case "control", "ctrl":
		return hkCtrl, true
	case "commandorcontrol", "cmdorctrl":
		if runtime.GOOS == "darwin" {
			return hkSuper, true
		}
		return hkCtrl, true
	case "alt", "option", "altgr":
		return hkAlt, true
	case "shift":
		return hkShift, true
	}
	return 0, false
}

// hotkeyName 解析键名：单字符、具名键、F1-F24、小键盘 Num0-9。
func hotkeyName(p string) (string, error) {
	if runes := []rune(p); len(runes) == 1 {
		return strings.ToLower(p), nil
	}
	lower := strings.ToLower(p)
	if k, ok := hkNamedKeys[lower]; ok {
		return k, nil
	}
	if len(lower) >= 2 && lower[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(lower[1:], "%d", &n); err == nil && n >= 1 && n <= 24 && fmt.Sprint(n) == lower[1:] {
			return fmt.Sprintf("F%d", n), nil
		}
	}
	if strings.HasPrefix(lower, "num") && len(lower) == 4 && lower[3] >= '0' && lower[3] <= '9' {
		return "Num" + lower[3:], nil
	}
	return "", fmt.Errorf("未知的按键 %q", p)
}

// hotkeyStandaloneOK 允许不带修饰键的按键。功能键与导航键不打字、不抢
// 常规输入，单绑安全；字母/数字/空格/回车/Tab 之类必须搭配修饰键，
// 否则打字、按钮激活、焦点跳转全都会被劫走。
func hotkeyStandaloneOK(key string) bool {
	if len(key) >= 2 && key[0] == 'F' {
		return true
	}
	switch key {
	case "Up", "Down", "Left", "Right", "Home", "End", "PageUp", "PageDown",
		"MediaPlayPause", "MediaNextTrack", "MediaPreviousTrack", "MediaStop",
		"VolumeUp", "VolumeDown", "VolumeMute", "PrintScreen":
		return true
	}
	return false
}

// hkKeyToUI 是 accelerator 规范键名 → MyGo 原生 UI 的 Key。
//
// 媒体键、音量键、锁定键与小键盘键不在表里：它们不作为普通按键送达窗口，
// 只能绑到全局作用域。加号（Shift+Equal 的另一种写法）与上下文菜单键同理。
var hkKeyToUI = map[string]ui.Key{
	"a": ui.KeyA, "b": ui.KeyB, "c": ui.KeyC, "d": ui.KeyD, "e": ui.KeyE,
	"f": ui.KeyF, "g": ui.KeyG, "h": ui.KeyH, "i": ui.KeyI, "j": ui.KeyJ,
	"k": ui.KeyK, "l": ui.KeyL, "m": ui.KeyM, "n": ui.KeyN, "o": ui.KeyO,
	"p": ui.KeyP, "q": ui.KeyQ, "r": ui.KeyR, "s": ui.KeyS, "t": ui.KeyT,
	"u": ui.KeyU, "v": ui.KeyV, "w": ui.KeyW, "x": ui.KeyX, "y": ui.KeyY,
	"z": ui.KeyZ,
	"0": ui.Key0, "1": ui.Key1, "2": ui.Key2, "3": ui.Key3, "4": ui.Key4,
	"5": ui.Key5, "6": ui.Key6, "7": ui.Key7, "8": ui.Key8, "9": ui.Key9,
	"-": ui.KeyMinus, "=": ui.KeyEqual, ",": ui.KeyComma, ".": ui.KeyPeriod,
	"/": ui.KeySlash, ";": ui.KeySemicolon, "'": ui.KeyQuote,
	"[": ui.KeyBracketLeft, "]": ui.KeyBracketRight, `\`: ui.KeyBackslash,
	"`":     ui.KeyBackquote,
	"Enter": ui.KeyEnter, "Escape": ui.KeyEscape, "Backspace": ui.KeyBackspace,
	"Tab": ui.KeyTab, "Space": ui.KeySpace, "Delete": ui.KeyDelete,
	"Insert": ui.KeyInsert, "Home": ui.KeyHome, "End": ui.KeyEnd,
	"PageUp": ui.KeyPageUp, "PageDown": ui.KeyPageDown,
	"Left": ui.KeyLeft, "Right": ui.KeyRight, "Up": ui.KeyUp, "Down": ui.KeyDown,
	"F1": ui.KeyF1, "F2": ui.KeyF2, "F3": ui.KeyF3, "F4": ui.KeyF4,
	"F5": ui.KeyF5, "F6": ui.KeyF6, "F7": ui.KeyF7, "F8": ui.KeyF8,
	"F9": ui.KeyF9, "F10": ui.KeyF10, "F11": ui.KeyF11, "F12": ui.KeyF12,
}

// hkUIToKey 是 hkKeyToUI 的反向表：录制控件把按下的 Key 换回键名。
var hkUIToKey = func() map[ui.Key]string {
	m := make(map[ui.Key]string, len(hkKeyToUI))
	for name, k := range hkKeyToUI {
		m[k] = name
	}
	return m
}()

// toUI 把绑定换算成窗口内快捷键需要的 (修饰键, 按键)。
func (b hotkeyBinding) toUI() (ui.Modifiers, ui.Key, error) {
	key, ok := hkKeyToUI[b.key]
	if !ok {
		return 0, 0, fmt.Errorf("%s 只能绑到全局快捷键（它不作为普通按键送达窗口）", hotkeyKeyLabel(b.key))
	}
	var mods ui.Modifiers
	if b.mods&hkSuper != 0 {
		mods |= ui.Super
	}
	if b.mods&hkCtrl != 0 {
		mods |= ui.Ctrl
	}
	if b.mods&hkAlt != 0 {
		mods |= ui.Alt
	}
	if b.mods&hkShift != 0 {
		mods |= ui.Shift
	}
	return mods, key, nil
}

// hotkeyFromEvent 把录制控件收到的一次按键换成规范的 accelerator 字符串。
// 只按下修饰键时收到 KeyUnknown，返回 error 让调用方继续等下一个键。
func hotkeyFromEvent(ev ui.InputEvent) (string, error) {
	name, ok := hkUIToKey[ev.Key]
	if !ok {
		return "", fmt.Errorf("这个键不能用作快捷键")
	}
	var mods hkMod
	if ev.Mods&ui.Super != 0 {
		mods |= hkSuper
	}
	if ev.Mods&ui.Ctrl != 0 {
		mods |= hkCtrl
	}
	if ev.Mods&ui.Alt != 0 {
		mods |= hkAlt
	}
	if ev.Mods&ui.Shift != 0 {
		mods |= hkShift
	}
	if mods == 0 && !hotkeyStandaloneOK(name) {
		return "", fmt.Errorf("%s 需要搭配修饰键", hotkeyKeyLabel(name))
	}
	return hotkeyBinding{mods: mods, key: name}.String(), nil
}

// ===== 展示 =====

// hotkeyKeyLabel 把规范键名换成给用户看的按键名。
func hotkeyKeyLabel(key string) string {
	if label, ok := hkKeyNames[key]; ok {
		return label
	}
	return strings.ToUpper(key)
}

// hkKeyNames 是具名键的中文展示名（单字符键直接显示大写字符）。
var hkKeyNames = map[string]string{
	"Space": "空格", "Enter": "回车", "Escape": "Esc", "Tab": "Tab",
	"Backspace": "退格", "Delete": "Delete", "Insert": "Insert",
	"Home": "Home", "End": "End", "PageUp": "PageUp", "PageDown": "PageDown",
	"Left": "←", "Right": "→", "Up": "↑", "Down": "↓",
	"MediaPlayPause": "媒体播放/暂停键", "MediaNextTrack": "媒体下一曲键",
	"MediaPreviousTrack": "媒体上一曲键", "MediaStop": "媒体停止键",
	"VolumeUp": "音量+", "VolumeDown": "音量-", "VolumeMute": "静音键",
	"PrintScreen": "PrintScreen", "CapsLock": "CapsLock", "NumLock": "NumLock",
	"ScrollLock": "ScrollLock", "ContextMenu": "菜单键",
}

// hotkeyDisplay 把 accelerator 换成用户看的组合键串：macOS 用苹果符号
// （⌃⌥⇧⌘，按苹果的书写顺序），Windows 用 Win、Linux 用 Super，其余按
// Ctrl+Alt+Shift+Key 的直觉顺序。单字符键显示大写字符，具名键走中文名。
func hotkeyDisplay(acc string) string {
	if acc == "" {
		return ""
	}
	b, err := parseHotkey(acc)
	if err != nil {
		// 解析不了的旧配置原样显示，让用户看得见、也改得掉。
		return acc
	}
	key := b.key
	if len([]rune(key)) == 1 {
		key = strings.ToUpper(key)
	} else {
		key = hotkeyKeyLabel(key)
	}
	if runtime.GOOS == "darwin" {
		var mods []string
		for _, m := range []struct {
			mod hkMod
			sym string
		}{{hkCtrl, "⌃"}, {hkAlt, "⌥"}, {hkShift, "⇧"}, {hkSuper, "⌘"}} {
			if b.mods&m.mod != 0 {
				mods = append(mods, m.sym)
			}
		}
		return strings.Join(append(mods, key), "")
	}
	super := "Super"
	if runtime.GOOS == "windows" {
		super = "Win"
	}
	var mods []string
	for _, m := range []struct {
		mod  hkMod
		name string
	}{{hkCtrl, "Ctrl"}, {hkAlt, "Alt"}, {hkShift, "Shift"}, {hkSuper, super}} {
		if b.mods&m.mod != 0 {
			mods = append(mods, m.name)
		}
	}
	return strings.Join(append(mods, key), "+")
}

// ===== 运行态 =====

// hotkeyRegistrar 是全局快捷键的注册面：生产用 mygo.GlobalShortcut，
// 单测注入假实现（MyGo 在 App.Run 之前不允许注册，会直接 panic）。
type hotkeyRegistrar interface {
	Register(acc string, fn func()) error
	UnregisterAll()
}

// mygoHotkeys 把接口接到 MyGo 的真实实现上。
type mygoHotkeys struct{}

func (mygoHotkeys) Register(acc string, fn func()) error {
	return mygo.GlobalShortcut.Register(acc, fn)
}
func (mygoHotkeys) UnregisterAll() { mygo.GlobalShortcut.UnregisterAll() }

// hotkeyTarget 是「正在录制哪一条绑定」。
type hotkeyTarget struct {
	scope  hotkeyScope
	action hotkeyAction
}

// hotkeyLocal 是一条窗口内绑定（已换算成原生 UI 的按键）。
type hotkeyLocal struct {
	mods   ui.Modifiers
	key    ui.Key
	action hotkeyAction
}

// hotkeyState 是快捷键的运行态。只在主线程访问：视图构建、MyGo 的快捷键
// 回调与设置页改动都排在主线程上（见 mygo.GlobalShortcut 的线程约定）。
type hotkeyState struct {
	// reg 是注册面，nil 表示用 MyGo 真实实现。
	reg hotkeyRegistrar
	// local 是每帧轮询的窗口内绑定表。
	local []hotkeyLocal
	// blocked 表示本会话屏蔽全局快捷键（Wayland）：设置页据此把「全局」一列
	// 整段标成「交给桌面」，syncHotkeys 据此一个键都不注册。
	blocked bool
	// notes 按配置键存放该条绑定的问题（解析失败、撞车、注册被拒），
	// 设置页就地显示；没有问题的绑定不入表。
	notes map[string]string
	// tips 是每个动作 tooltip 后缀（含括号），在 syncHotkeys 里算好。
	// 播放条每帧都要画这三个按钮，tooltip 不能每帧去解一遍 accelerator。
	tips map[hotkeyAction]string
	// record 非 nil 表示正在录制：窗口内快捷键整段让路。
	record *hotkeyTarget
	// recordAt 是这次录制的起点。录制会压住窗口内快捷键，所以必须有尽头：
	// 用户点了录制格之后去点别处（焦点跑掉、按键再也进不来）时，
	// 超时即视为放弃，别让整套窗口内快捷键一直哑着。
	recordAt time.Time
	// bound 记录「已向系统发起过注册」，Close 才知道要不要撤销。
	bound bool
}

func (h *hotkeyState) registrar() hotkeyRegistrar {
	if h.reg != nil {
		return h.reg
	}
	return mygoHotkeys{}
}

// hotkeyEnabled 是全局快捷键总开关（默认为开）。
func (a *App) hotkeyEnabled() bool {
	if a.Conf == nil {
		return true
	}
	return a.Conf.Bool(hotkeyEnabledKey, true)
}

// hotkeyRaw 读一条绑定的配置值，未设置时回落到出厂默认。
// 显式存过空串就是「解绑」，不会被默认值覆盖回来。
func (a *App) hotkeyRaw(scope hotkeyScope, spec hotkeySpec) string {
	def := spec.globalDefault
	if scope == scopeLocal {
		def = spec.localDefault
	}
	if a.Conf == nil {
		return def
	}
	return a.Conf.String(hotkeyConfKey(scope, spec.suffix), def)
}

// syncHotkeys 依据配置重建快捷键：撤掉全部全局注册，再逐条注册，
// 同时重算窗口内绑定表与各条的问题提示。
//
// 整体重建而不是增量对齐：Wayland 的 GlobalShortcuts 门户一次会话绑定
// 全部快捷键，改一条就得换一次会话（见 mygo 的 hotkey_portal.go），
// Windows/macOS 的撤销也只是一个函数调用。换来的是配置、系统状态、
// 设置页三者的口径永远一致。
//
// Wayland 会话（hotkeys.blocked）下全局那一整列都不进注册面：一个键都不抓，
// 撤册照做，所以从 X11 换到 Wayland，或中途关掉开关，都不会留下半套绑定。
// 窗口内快捷键不受影响。
func (a *App) syncHotkeys() {
	// 会话类型在一次运行里不会变，所以判一次存下来（New 里），而不是每回都读环境：
	// 直接构造 App 的单测不继承任何桌面环境，注册面照旧可测。
	blocked := a.hotkeys.blocked
	enabled := a.hotkeyEnabled() && !blocked
	reg := a.hotkeys.registrar()
	reg.UnregisterAll()

	notes := map[string]string{}
	var locals []hotkeyLocal
	// 两个作用域各自「按动作记规范串」，供冲突判定与 tooltip 复用。
	globalAcc := map[hotkeyAction]string{}
	localAcc := map[hotkeyAction]string{}

	// 先排全局：窗口内要不要让路，取决于全局是否真的占住了这个键。
	globalTaken := map[string]hotkeyAction{}
	if enabled {
		for _, spec := range hotkeySpecs {
			conf := hotkeyConfKey(scopeGlobal, spec.suffix)
			raw := a.hotkeyRaw(scopeGlobal, spec)
			if strings.TrimSpace(raw) == "" {
				continue
			}
			bind, err := parseHotkey(raw)
			if err != nil {
				notes[conf] = err.Error()
				continue
			}
			acc := bind.String()
			if other, dup := globalTaken[acc]; dup {
				notes[conf] = "与「" + hotkeyLabelOfAction(other) + "」用了同一个组合键"
				continue
			}
			globalTaken[acc] = spec.action
			action := spec.action
			if err := reg.Register(acc, func() { a.runHotkeyAction(action) }); err != nil {
				delete(globalTaken, acc)
				notes[conf] = hotkeyRegisterProblem(err)
				continue
			}
			globalAcc[spec.action] = acc
		}
	}

	// 再排窗口内。
	localTaken := map[string]hotkeyAction{}
	for _, spec := range hotkeySpecs {
		conf := hotkeyConfKey(scopeLocal, spec.suffix)
		raw := a.hotkeyRaw(scopeLocal, spec)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		bind, err := parseHotkey(raw)
		if err != nil {
			notes[conf] = err.Error()
			continue
		}
		acc := bind.String()
		mods, key, err := bind.toUI()
		if err != nil {
			notes[conf] = err.Error()
			continue
		}
		if other, dup := globalTaken[acc]; enabled && dup {
			notes[conf] = "与「" + hotkeyLabelOfAction(other) + "」的全局快捷键同键：按键会被系统截走，窗口内不会触发"
			continue
		}
		if other, dup := localTaken[acc]; dup {
			notes[conf] = "与「" + hotkeyLabelOfAction(other) + "」用了同一个组合键"
			continue
		}
		localTaken[acc] = spec.action
		localAcc[spec.action] = acc
		locals = append(locals, hotkeyLocal{mods: mods, key: key, action: spec.action})
	}

	// tooltip 口径：窗口内优先（用户正看着窗口，按下去生效的就是它），
	// 没绑才退回全局那条。媒体键只能出现在全局一列，报出来是「媒体上一曲键」。
	tips := map[hotkeyAction]string{}
	for _, spec := range hotkeySpecs {
		acc := localAcc[spec.action]
		if acc == "" {
			acc = globalAcc[spec.action]
		}
		if acc != "" {
			tips[spec.action] = "（" + hotkeyDisplay(acc) + "）"
		}
	}

	a.hotkeys.local = locals
	a.hotkeys.notes = notes
	a.hotkeys.tips = tips
	a.hotkeys.bound = true
}

// hotkeyRegisterProblem 把 MyGo 的注册失败译成用户能看懂的话。
func hotkeyRegisterProblem(err error) string {
	msg := strings.TrimPrefix(err.Error(), "mygo: ")
	switch {
	case strings.Contains(msg, "already a global shortcut"),
		strings.Contains(msg, "may be used by another application"),
		strings.Contains(msg, "cannot register"):
		return "该系统组合键已被其他应用占用或不允许注册，请换一个"
	case strings.Contains(msg, "keyboard has no key"),
		strings.Contains(msg, "unsupported key"),
		strings.Contains(msg, "cannot be used in a global shortcut"):
		return "这个键不能用作全局快捷键（macOS 的媒体键由系统「正在播放」接管，无需在这里绑）"
	case strings.Contains(msg, "not supported"):
		return "当前桌面不支持全局快捷键"
	default:
		return "注册失败：" + msg
	}
}

// hotkeyLabelOfAction 取动作的中文名，用于冲突提示。
func hotkeyLabelOfAction(action hotkeyAction) string {
	if spec, ok := hotkeySpecOf(action); ok {
		return spec.label
	}
	return string(action)
}

// setHotkey 写入一条绑定并立即重注册。
func (a *App) setHotkey(scope hotkeyScope, spec hotkeySpec, acc string) {
	if a.Conf != nil {
		a.Conf.Set(hotkeyConfKey(scope, spec.suffix), acc)
	}
	a.hotkeys.record = nil
	a.syncHotkeys()
	a.invalidate()
}

// clearHotkey 解绑一条。
func (a *App) clearHotkey(scope hotkeyScope, spec hotkeySpec) {
	a.setHotkey(scope, spec, "")
}

// resetHotkeys 恢复全部出厂绑定。
func (a *App) resetHotkeys() {
	if a.Conf != nil {
		for _, spec := range hotkeySpecs {
			a.Conf.Set(hotkeyConfKey(scopeGlobal, spec.suffix), spec.globalDefault)
			a.Conf.Set(hotkeyConfKey(scopeLocal, spec.suffix), spec.localDefault)
		}
	}
	a.hotkeys.record = nil
	a.syncHotkeys()
	a.invalidate()
}

// runHotkeyAction 把动作落到播放器上。回调来自系统（全局）或根视图轮询
// （窗口内），两者都在主线程；player 本身并发安全。
func (a *App) runHotkeyAction(action hotkeyAction) {
	if a.PL == nil {
		return
	}
	switch action {
	case hkPlayPause:
		a.PL.PlayOrPause()
	case hkPlay:
		a.PL.Play()
	case hkPause:
		a.PL.Pause()
	case hkPrev:
		a.PL.Prev()
	case hkNext:
		a.PL.Next(false)
	}
}

// localHotkeys 在根视图每帧轮询窗口内快捷键。
//
// 录制中整段让路：ui.Context.Shortcut 的注册会让按键在 keyDown 阶段被
// 「窗口快捷键」判定截走，录制控件（HandleInput）就拿不到用户按下的键了。
// 见 third_party/mygo/ui/input.go 的 keyDown / routeKeys。
func (a *App) localHotkeys(c *ui.Context) {
	if a.hotkeys.record != nil || len(a.hotkeys.local) == 0 {
		return
	}
	for _, b := range a.hotkeys.local {
		if c.Shortcut(b.mods, b.key) {
			a.runHotkeyAction(b.action)
			// 不 break：一次按键只可能命中一条绑定（解析阶段已去重），
			// 继续轮询只是把剩下的注册项照常登记进这一帧。
		}
	}
}

// hotkeyRecordTimeout 是一次录制的有效期。
const hotkeyRecordTimeout = 10 * time.Second

// startRecording 进入录制态并起超时表。
func (a *App) startRecording(scope hotkeyScope, spec hotkeySpec) {
	a.hotkeys.record = &hotkeyTarget{scope: scope, action: spec.action}
	a.hotkeys.recordAt = time.Now()
}

// expireRecording 收尾录制态：离开这一页、或超时。返回是否仍在录制，
// 并把下一次重绘排到超时那一刻（不然没有事件就永远停在「按下新快捷键…」）。
func (a *App) expireRecording(c *ui.Context) bool {
	if a.hotkeys.record == nil {
		return false
	}
	offPage := a.Router == nil || a.Router.Path() != "/settings" || a.settings.tab != hotkeysTab
	deadline := a.hotkeys.recordAt.Add(hotkeyRecordTimeout)
	if offPage || !c.Now().Before(deadline) {
		a.hotkeys.record = nil
		return false
	}
	c.After(time.Until(deadline))
	return true
}

// recording 报告某条绑定是否正在录制。
func (a *App) recording(scope hotkeyScope, spec hotkeySpec) bool {
	r := a.hotkeys.record
	return r != nil && r.scope == scope && r.action == spec.action
}

// toggleRecording 点一条绑定＝开始/结束录制。
func (a *App) toggleRecording(scope hotkeyScope, spec hotkeySpec) {
	if a.recording(scope, spec) {
		a.hotkeys.record = nil
		return
	}
	a.startRecording(scope, spec)
}

// hotkeyTip 取某个动作的 tooltip 后缀（形如「（Ctrl+Alt+P）」），
// 由 syncHotkeys 预算好，读表即得。没绑定或还没同步过就是空串。
func (a *App) hotkeyTip(action hotkeyAction) string {
	return a.hotkeys.tips[action]
}

// hotkeyNote 取一条绑定的问题提示（无问题返回空串）。
func (a *App) hotkeyNote(scope hotkeyScope, spec hotkeySpec) string {
	return a.hotkeys.notes[hotkeyConfKey(scope, spec.suffix)]
}

// closeHotkeys 退出时撤销全局注册。幂等：Close 可能走两条路径。
func (a *App) closeHotkeys() {
	if a.hotkeys.bound {
		a.hotkeys.registrar().UnregisterAll()
		a.hotkeys.bound = false
	}
	a.hotkeys.local = nil
}
