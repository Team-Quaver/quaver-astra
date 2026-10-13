package appui

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// ===== 测试夹具 =====

// fakeHotkeyRegistrar 是全局注册面的假实现：MyGo 在 App.Run 之前不允许注册
// （needsApp 直接 panic），单测只能走这个缝。
type fakeHotkeyRegistrar struct {
	mu          sync.Mutex
	registered  map[string]func()
	unregisters int
	fail        map[string]error // accelerator → 注册失败
}

func (f *fakeHotkeyRegistrar) Register(acc string, fn func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registered == nil {
		f.registered = map[string]func(){}
	}
	if _, ok := f.registered[acc]; ok {
		return fmt.Errorf("mygo: shortcut %s is already registered", acc)
	}
	if err, bad := f.fail[acc]; bad {
		return err
	}
	f.registered[acc] = fn
	return nil
}

func (f *fakeHotkeyRegistrar) UnregisterAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = map[string]func(){}
	f.unregisters++
}

func (f *fakeHotkeyRegistrar) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.registered))
	for acc := range f.registered {
		out = append(out, acc)
	}
	slices.Sort(out)
	return out
}

func (f *fakeHotkeyRegistrar) has(acc string) bool {
	return slices.Contains(f.keys(), acc)
}

// press 模拟系统送来一次全局按键。
func (f *fakeHotkeyRegistrar) press(t *testing.T, acc string) {
	t.Helper()
	f.mu.Lock()
	fn := f.registered[acc]
	f.mu.Unlock()
	if fn == nil {
		t.Fatalf("全局快捷键 %q 没有注册（当前：%v）", acc, f.keys())
	}
	fn()
}

// hotkeyEngine 是可观测的假音频引擎：暂停态与播放次数都记下来。
type hotkeyEngine struct {
	nilEngine
	mu     sync.Mutex
	paused bool
	plays  int
	pauses int
}

func (e *hotkeyEngine) Play() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused, e.plays = false, e.plays+1
}

func (e *hotkeyEngine) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused, e.pauses = true, e.pauses+1
}

func (e *hotkeyEngine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

func (e *hotkeyEngine) IsPlaying() bool          { return !e.Paused() }
func (e *hotkeyEngine) Duration(float64) float64 { return 269 }

// hotkeyApp 造一个只用来跑快捷键的 App：真 conf、假注册面、可控引擎。
func hotkeyApp(t *testing.T) (*App, *fakeHotkeyRegistrar, *hotkeyEngine) {
	t.Helper()
	dir := t.TempDir()
	eng := &hotkeyEngine{}
	app := &App{
		PL:            player.New(npBackend{}, eng, conf.Open(dir)),
		Conf:          conf.Open(dir),
		Router:        ui.NewRouter("/settings"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		albums:        map[string]*albumState{},
		singers:       map[string]*singerState{},
		lastLyricLine: -2,
	}
	app.settings.draftsLoaded = true
	app.hotkeys.reg = &fakeHotkeyRegistrar{fail: map[string]error{}}
	return app, app.hotkeys.reg.(*fakeHotkeyRegistrar), eng
}

// waitPlaying 等异步起播把 playing 拉起来/落下去。
func waitPlaying(t *testing.T, p *player.Player, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.Playing() == want && !p.Loading() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("播放状态没到 playing=%v（当前 playing=%v loading=%v err=%q）", want, p.Playing(), p.Loading(), p.Error())
}

// cmdOrCtrl 是 CmdOrCtrl 在本机展开后的修饰键名。
func cmdOrCtrl() string {
	if runtime.GOOS == "darwin" {
		return "Super"
	}
	return "Ctrl"
}

// ===== 解析与展示 =====

func TestParseHotkeyCanonical(t *testing.T) {
	cc := cmdOrCtrl()
	cases := []struct{ raw, want string }{
		{"CmdOrCtrl+Alt+P", cc + "+Alt+P"},
		{"ctrl+alt+p", "Ctrl+Alt+P"},
		{"Ctrl+Alt+,", "Ctrl+Alt+,"},
		{"Ctrl+Alt+.", "Ctrl+Alt+."},
		{"Shift+CmdOrCtrl+k", cc + "+Shift+K"},
		{"Command+Shift+Space", "Super+Shift+Space"},
		{"Win+D", "Super+D"},
		{"MediaPlayPause", "MediaPlayPause"},
		{"F8", "F8"},
		{"Ctrl+Alt++", "Ctrl+Alt++"},
		{"Alt+PageDown", "Alt+PageDown"},
		{"  Ctrl + Alt + Enter  ", "Ctrl+Alt+Enter"},
	}
	for _, tc := range cases {
		bind, err := parseHotkey(tc.raw)
		if err != nil {
			t.Errorf("parseHotkey(%q) 报错：%v", tc.raw, err)
			continue
		}
		if got := bind.String(); got != tc.want {
			t.Errorf("parseHotkey(%q).String() = %q，期望 %q", tc.raw, got, tc.want)
		}
		// 规范形式必须能再解一遍且结果不变（配置往返稳定）。
		again, err := parseHotkey(bind.String())
		if err != nil || again.String() != tc.want {
			t.Errorf("规范形式 %q 再解析失败：%v (%q)", bind.String(), err, again.String())
		}
	}
}

func TestParseHotkeyRejects(t *testing.T) {
	cases := []struct{ raw, why string }{
		{"", "空值"},
		{"   ", "只有空格"},
		{"P", "单绑字母会劫走打字"},
		{"Space", "单绑空格会抢按钮激活"},
		{"Enter", "单绑回车会抢默认按钮"},
		{"Ctrl+P+Q", "中间段不是修饰键"},
		{"Ctrl++Alt", "切分不清"},
		{"Ctrl+Alt+", "末段为空"},
		{"Ctrl+Ctrl+P", "修饰键重复"},
		{"Ctrl+不存在的键", "未知按键"},
	}
	for _, tc := range cases {
		if _, err := parseHotkey(tc.raw); err == nil {
			t.Errorf("parseHotkey(%q)（%s）应当报错，实际通过", tc.raw, tc.why)
		}
	}
}

func TestHotkeyToUIRejectsMediaKeys(t *testing.T) {
	// 媒体键不作为普通按键送达窗口：只能绑全局。
	for _, acc := range []string{"MediaPlayPause", "MediaNextTrack", "MediaPreviousTrack", "VolumeMute"} {
		bind, err := parseHotkey(acc)
		if err != nil {
			t.Fatalf("parseHotkey(%q)：%v", acc, err)
		}
		if _, _, err := bind.toUI(); err == nil {
			t.Errorf("%s 应当不能作为窗口内快捷键", acc)
		} else if !strings.Contains(err.Error(), "全局") {
			t.Errorf("%s 的错误文案应指向全局一列：%v", acc, err)
		}
	}

	// 普通键换算：修饰键与按键都要对上 MyGo 原生 UI 的常量。
	bind, err := parseHotkey("Ctrl+Alt+,")
	if err != nil {
		t.Fatal(err)
	}
	mods, key, err := bind.toUI()
	if err != nil {
		t.Fatal(err)
	}
	if mods != ui.Ctrl|ui.Alt || key != ui.KeyComma {
		t.Errorf("toUI() = (%v, %v)，期望 (Ctrl|Alt, KeyComma)", mods, key)
	}

	// 录制回来的串要能和解析器对上（round-trip）。
	name, recErr := hotkeyFromEvent(ui.InputEvent{Kind: ui.InputKeyDown, Mods: ui.Ctrl | ui.Shift, Key: ui.KeyF5})
	if recErr != nil || name != "Ctrl+Shift+F5" {
		t.Errorf("hotkeyFromEvent = %q / %v，期望 Ctrl+Shift+F5", name, recErr)
	}
	if _, err := hotkeyFromEvent(ui.InputEvent{Kind: ui.InputKeyDown, Mods: ui.Ctrl, Key: ui.KeyUnknown}); err == nil {
		t.Error("只按修饰键（KeyUnknown）应当报错，让录制继续等下一个键")
	}
	if _, err := hotkeyFromEvent(ui.InputEvent{Kind: ui.InputKeyDown, Mods: 0, Key: ui.KeyA}); err == nil {
		t.Error("不带修饰键的字母不该被录进来")
	}
}

func TestHotkeyDisplay(t *testing.T) {
	cases := []struct{ acc, want string }{
		{"MediaPlayPause", "媒体播放/暂停键"},
		{"MediaPreviousTrack", "媒体上一曲键"},
		{"Ctrl+Alt+,", "Ctrl+Alt+,"},
		{"", ""},
		{"乱写的旧配置", "乱写的旧配置"}, // 解不开就原样显示，用户才改得掉
	}
	for _, tc := range cases {
		if got := hotkeyDisplay(tc.acc); got != tc.want {
			t.Errorf("hotkeyDisplay(%q) = %q，期望 %q", tc.acc, got, tc.want)
		}
	}
	// 修饰键顺序按平台习惯：macOS 用 ⌃⇧⌘ 符号，其余用 Ctrl+Alt+Shift+Win/Super。
	got := hotkeyDisplay("Super+Ctrl+Alt+Shift+P")
	want := "Ctrl+Alt+Shift+Super+P"
	switch runtime.GOOS {
	case "darwin":
		want = "⌃⇧⌘P"
	case "windows":
		want = "Ctrl+Alt+Shift+Win+P"
	}
	if got != want {
		t.Errorf("hotkeyDisplay(全修饰键) = %q，期望 %q（%s）", got, want, runtime.GOOS)
	}
	if got := hotkeyDisplay("Ctrl+Alt+Space"); got != "Ctrl+Alt+空格" && runtime.GOOS != "darwin" {
		t.Errorf("具名键应显示中文名，实际 %q", got)
	}
}

// ===== 注册与冲突 =====

func TestSyncHotkeysDefaults(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.syncHotkeys()

	// 全局出厂默认是三个媒体键；macOS 上媒体键归系统「正在播放」，
	// 默认留空（见 hotkeys.go 的 init）。
	want := []string{"MediaNextTrack", "MediaPlayPause", "MediaPreviousTrack"}
	if runtime.GOOS == "darwin" {
		want = nil
	}
	if got := reg.keys(); !slices.Equal(got, want) {
		t.Errorf("全局注册 = %v，期望 %v", got, want)
	}
	if len(app.hotkeys.local) != 3 {
		t.Errorf("窗口内绑定 = %d 条，期望 3 条（播放/暂停 + 上一曲 + 下一曲）", len(app.hotkeys.local))
	}
	for _, b := range app.hotkeys.local {
		if b.mods != ui.Ctrl|ui.Alt {
			t.Errorf("窗口内绑定 %+v 的修饰键不对", b)
		}
	}
	for _, acc := range want {
		if !reg.has(acc) {
			t.Errorf("默认少了全局绑定 %s", acc)
		}
	}
	if n := len(app.hotkeys.notes); n != 0 {
		t.Errorf("默认配置不该有问题提示，实际 %d 条：%v", n, app.hotkeys.notes)
	}
	// 播放/暂停两个单向动作默认不绑：避免出厂就抢走 Ctrl+Alt+... 这类常用组合。
	for _, scope := range []hotkeyScope{scopeGlobal, scopeLocal} {
		for _, suffix := range []string{"Play", "Pause"} {
			if app.hotkeyRaw(scope, hotkeySpec{suffix: suffix, globalDefault: "", localDefault: ""}) != "" {
				t.Errorf("%s 的 %s 默认应当为空", scope, suffix)
			}
		}
	}
}

func TestSyncHotkeysGlobalDisabledKeepsLocal(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.Conf.Set(hotkeyEnabledKey, false)
	app.syncHotkeys()

	if len(reg.keys()) != 0 {
		t.Errorf("关掉开关后仍注册了全局快捷键：%v", reg.keys())
	}
	if len(app.hotkeys.local) != 3 {
		t.Errorf("窗口内快捷键不该被全局开关关掉，实际 %d 条", len(app.hotkeys.local))
	}
}

func TestSyncHotkeysSameKeyAcrossScopes(t *testing.T) {
	app, _, _ := hotkeyApp(t)
	// 把窗口内的「播放/暂停」和全局的「下一曲」绑到同一个组合键。
	// 媒体键那种「只能全局」的绑定另有专门断言，这里要的是两域都能用的键。
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Next"), "Ctrl+Alt+J")
	app.Conf.Set(hotkeyConfKey(scopeLocal, "PlayPause"), "ctrl+alt+j")
	app.syncHotkeys()

	note := app.hotkeyNote(scopeLocal, hotkeySpecs[0])
	if note == "" || !strings.Contains(note, "窗口内不会触发") {
		t.Fatalf("同键冲突应当提示，实际：%q", note)
	}
	for _, b := range app.hotkeys.local {
		if b.action == hkPlayPause {
			t.Error("冲突的窗口内绑定应当被跳过，否则会留一条按了没反应的快捷键")
		}
	}

	// 关掉全局开关之后，按键不再被系统截走，同一条窗口内绑定重新可用。
	app.Conf.Set(hotkeyEnabledKey, false)
	app.syncHotkeys()
	var revived bool
	for _, b := range app.hotkeys.local {
		if b.action == hkPlayPause {
			revived = true
		}
	}
	if !revived {
		t.Error("全局关闭后，同键的窗口内绑定应当恢复生效")
	}
}
func TestSyncHotkeysDuplicateInScope(t *testing.T) {
	app, _, _ := hotkeyApp(t)
	app.Conf.Set(hotkeyConfKey(scopeLocal, "Prev"), "Ctrl+Alt+J")
	app.Conf.Set(hotkeyConfKey(scopeLocal, "Next"), "ctrl+alt+j") // 同一键，只是大小写不同
	app.syncHotkeys()

	// 后声明的一条（specs[4] = 下一曲）让位并给出提示。
	note := app.hotkeyNote(scopeLocal, hotkeySpecs[4])
	if note == "" || !strings.Contains(note, "同一个组合键") {
		t.Fatalf("同作用域重复应当提示后一条，实际：%q", note)
	}
	var hasPrev bool
	for _, b := range app.hotkeys.local {
		if b.action == hkPrev {
			hasPrev = true
		}
	}
	if !hasPrev {
		t.Error("重复时应当保留先声明的一条")
	}
}
func TestSyncHotkeysRegisterFailureFreesLocal(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	// 全局和窗口内绑同一个普通键；系统把全局注册拒了（别的应用先占了）。
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "PlayPause"), "Ctrl+Alt+P")
	reg.fail["Ctrl+Alt+P"] = errors.New("mygo: Ctrl+Alt+P is already a global shortcut of another app")
	app.syncHotkeys()

	note := app.hotkeyNote(scopeGlobal, hotkeySpecs[0])
	if !strings.Contains(note, "已被其他应用占用") {
		t.Fatalf("全局注册失败要报得明白，实际：%q", note)
	}
	// 系统既然没真的截走按键，窗口内这条就该照常生效。
	var hasLocal bool
	for _, b := range app.hotkeys.local {
		if b.action == hkPlayPause {
			hasLocal = true
		}
	}
	if !hasLocal {
		t.Error("全局注册失败时，同键的窗口内绑定不应被禁掉")
	}
	if reg.has("Ctrl+Alt+P") {
		t.Error("失败的绑定不应算作已注册")
	}

	// 媒体键只能出现在全局一列。
	app.Conf.Set(hotkeyConfKey(scopeLocal, "Next"), "MediaNextTrack")
	app.syncHotkeys()
	if note := app.hotkeyNote(scopeLocal, hotkeySpecs[4]); !strings.Contains(note, "只能绑到全局") {
		t.Errorf("窗口内绑媒体键应当提示改用全局，实际：%q", note)
	}
}
func TestSyncHotkeysBadConfigOnlyNotes(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Next"), "喵")
	app.syncHotkeys()

	if note := app.hotkeyNote(scopeGlobal, hotkeySpecs[4]); note == "" {
		t.Error("坏配置应当有提示")
	}
	if reg.has("喵") {
		t.Error("坏配置不该送去注册")
	}
	// 其余默认绑定照常。
	if !reg.has("MediaPlayPause") {
		t.Errorf("一条坏配置不该影响其他绑定：%v", reg.keys())
	}
}

func TestSyncHotkeysRebuildsFromScratch(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.syncHotkeys()
	first := reg.unregisters

	app.Conf.Set(hotkeyConfKey(scopeGlobal, "PlayPause"), "")
	app.syncHotkeys()

	if reg.unregisters <= first {
		t.Error("每次同步都应先撤干净再重注册（Wayland 门户一次会话绑全部）")
	}
	if reg.has("MediaPlayPause") {
		t.Errorf("解绑后不应还挂着 MediaPlayPause：%v", reg.keys())
	}
}

// ===== 动作分发 =====

func TestGlobalHotkeyDispatch(t *testing.T) {
	app, reg, eng := hotkeyApp(t)
	app.PL.EnqueueMany([]player.Song{{Mid: "a", SongID: 1, Interval: 269}, {Mid: "b", SongID: 2, Interval: 269}})
	// 自带一套与平台无关的绑定：默认值是媒体键，macOS 上根本注册不了。
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "PlayPause"), "Ctrl+Alt+U")
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Prev"), "Ctrl+Alt+I")
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Next"), "Ctrl+Alt+O")
	app.syncHotkeys()

	// 起播：全局播放键在没有当前曲时从队列第一首开始。
	reg.press(t, "Ctrl+Alt+U")
	waitPlaying(t, app.PL, true)

	reg.press(t, "Ctrl+Alt+O")
	if idx := app.PL.Index(); idx != 1 {
		t.Errorf("下一曲后 index = %d，期望 1", idx)
	}
	// 换曲的载入是异步的：等它落定，否则后面的暂停会被起播回调覆盖。
	waitPlaying(t, app.PL, true)

	reg.press(t, "Ctrl+Alt+I")
	if idx := app.PL.Index(); idx != 0 {
		t.Errorf("上一曲后 index = %d，期望 0", idx)
	}
	waitPlaying(t, app.PL, true)

	// 切换：播放 ↔ 暂停真的来回走。
	reg.press(t, "Ctrl+Alt+U")
	waitPlaying(t, app.PL, false)
	if pauses := eng.pauses; pauses == 0 {
		t.Error("暂停没有落到引擎")
	}
	reg.press(t, "Ctrl+Alt+U")
	waitPlaying(t, app.PL, true)

	// 单向动作：显式绑一对播放/暂停键，状态已符合时不该再动引擎。
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Play"), "Ctrl+Alt+K")
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Pause"), "Ctrl+Alt+L")
	app.syncHotkeys()

	reg.press(t, "Ctrl+Alt+L")
	waitPlaying(t, app.PL, false)
	pauses := eng.pauses
	reg.press(t, "Ctrl+Alt+L")
	if eng.pauses != pauses {
		t.Error("已经暂停时，「暂停」不该再动引擎")
	}
	reg.press(t, "Ctrl+Alt+K")
	waitPlaying(t, app.PL, true)
	plays := eng.plays
	reg.press(t, "Ctrl+Alt+K")
	if eng.plays != plays {
		t.Error("已经在播时，「播放」不该再动引擎")
	}
}
func TestLocalHotkeyFiresAndYieldsToRecorder(t *testing.T) {
	app, _, eng := hotkeyApp(t)
	app.syncHotkeys()
	app.PL.EnqueueMany([]player.Song{{Mid: "a", SongID: 1, Interval: 269}})

	tester := ui.NewTester(app.View, testWinW, testWinH)
	tester.Frame()

	tester.Key(ui.Ctrl|ui.Alt, ui.KeyP) // 播放/暂停
	waitPlaying(t, app.PL, true)
	tester.Frame()
	tester.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	waitPlaying(t, app.PL, false)

	tester.Key(ui.Ctrl|ui.Alt, ui.KeyPeriod) // 下一曲（默认 Ctrl+Alt+.）
	if app.PL.QueueLen() == 0 {
		t.Fatal("队列应当还在")
	}
	// 换曲的载入是异步的，等它落定再断言：否则「在播」会在下一行才成立。
	waitPlaying(t, app.PL, true)

	// 录制中：窗口内快捷键整段让路，否则按键永远进不了录制控件。
	app.startRecording(scopeLocal, hotkeySpecs[1])
	tester.Frame()
	before := eng.plays
	tester.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	tester.Frame()
	if eng.plays != before || app.PL.Playing() {
		t.Error("录制期间窗口内快捷键不应该触发")
	}

	// 离开「快捷键」tab 时录制状态自动收尾：不然所有窗口内快捷键都被压住。
	app.settings.tab = aboutTab
	tester.Frame()
	if app.hotkeys.record != nil {
		t.Error("切走 tab 后录制状态应当清空")
	}
	tester.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	waitPlaying(t, app.PL, true)
}

// 录了但没按（用户转头去点别处、焦点跑了）不能把窗口内快捷键永久压住。
func TestRecordingTimesOut(t *testing.T) {
	app, _, _ := hotkeyApp(t)
	app.settings.tab = hotkeysTab
	app.PL.EnqueueMany([]player.Song{{Mid: "a", SongID: 1, Interval: 269}})
	app.syncHotkeys()

	tester := ui.NewTester(app.View, 1200, 1100)
	tester.Frame()
	app.startRecording(scopeLocal, hotkeySpecs[1])
	tester.Frame()
	if app.hotkeys.record == nil {
		t.Fatal("刚进入录制就被清了")
	}

	app.hotkeys.recordAt = time.Now().Add(-2 * hotkeyRecordTimeout)
	tester.Frame()
	if app.hotkeys.record != nil {
		t.Error("超时后录制态应当自动结束")
	}
	// 结束后窗口内快捷键立刻恢复。
	tester.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	waitPlaying(t, app.PL, true)
}

func TestRecorderCapturesAndClearsBinding(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.settings.tab = hotkeysTab
	app.syncHotkeys()

	// 快捷键页比默认窗口高，用够高的视口，保证「恢复默认」在可视区内可点。
	tester := ui.NewTester(app.View, 1200, 1100)
	tester.Frame()

	if err := tester.Click("窗口内快捷键：播放"); err != nil {
		t.Fatalf("点不到录制格：%v", err)
	}
	tester.Frame()
	if !app.recording(scopeLocal, hotkeySpecs[1]) {
		t.Fatal("点击后应进入录制")
	}
	if !tester.HasText("按下新快捷键") {
		t.Errorf("录制态没有提示文案：%v", tester.Texts())
	}

	// 录制期间的按键不该再被窗口快捷键吃掉。
	tester.Key(ui.Ctrl|ui.Alt, ui.KeyR)
	if got := app.Conf.String(hotkeyConfKey(scopeLocal, "Play"), ""); got != "Ctrl+Alt+R" {
		t.Errorf("录到的绑定 = %q，期望 Ctrl+Alt+R（conf=%v）", got, app.Conf.String(hotkeyConfKey(scopeLocal, "Play"), ""))
	}
	if app.hotkeys.record != nil {
		t.Error("录完应退出录制态")
	}
	tester.Frame()
	if !tester.HasText("Ctrl+Alt+R") {
		t.Errorf("表格里没显示新绑定：%v", tester.Texts())
	}

	// Esc 取消，不改配置。
	if err := tester.Click("全局快捷键：暂停"); err != nil {
		t.Fatalf("点不到全局录制格：%v", err)
	}
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	if app.hotkeys.record != nil {
		t.Error("Esc 应当取消录制")
	}
	if app.Conf.String(hotkeyConfKey(scopeGlobal, "Pause"), "") != "" {
		t.Error("取消录制不该写配置")
	}

	// 解绑按钮 + 恢复默认。
	if err := tester.Click("清除全局快捷键：播放/暂停"); err != nil {
		t.Fatalf("点不到解绑按钮：%v", err)
	}
	tester.Frame()
	if reg.has("MediaPlayPause") {
		t.Error("解绑后全局注册应当撤掉")
	}
	if err := tester.Click("恢复默认快捷键"); err != nil {
		t.Fatalf("点不到恢复默认：%v", err)
	}
	tester.Frame()
	if def := hotkeySpecs[0].globalDefault; def != "" && !reg.has(def) {
		t.Errorf("恢复默认后应重新绑定 %s：%v", def, reg.keys())
	}
	if def := hotkeySpecs[0].globalDefault; def == "" && len(reg.keys()) == 0 {
		t.Errorf("恢复默认后窗口内/全局配置不该全空：%v", reg.keys())
	}
}

func TestCloseHotkeysUnregisters(t *testing.T) {
	app, reg, _ := hotkeyApp(t)
	app.syncHotkeys()
	app.closeHotkeys()
	if len(reg.keys()) != 0 {
		t.Errorf("退出后仍挂着全局快捷键：%v", reg.keys())
	}
	if len(app.hotkeys.local) != 0 {
		t.Error("退出后窗口内绑定表应清空")
	}
	app.closeHotkeys() // 幂等
}

func TestHotkeyTipPrefersEffectiveBinding(t *testing.T) {
	app, _, _ := hotkeyApp(t)
	app.syncHotkeys()

	// 窗口内那条默认就有：用户在窗口里按键，生效的是它，tooltip 就该报它。
	if got := app.hotkeyTip(hkPlayPause); got != "（Ctrl+Alt+P）" {
		t.Errorf("tooltip = %q，期望（Ctrl+Alt+P）", got)
	}
	// 两个作用域都没绑：不带后缀，别在按钮上写「（）」占位。
	if got := app.hotkeyTip(hkPlay); got != "" {
		t.Errorf("未绑定的动作不该有 tooltip 后缀，实际 %q", got)
	}
	// 只绑全局时退回全局那条（媒体键）。媒体播放键一次只能归一个动作，
	// 所以先把默认占着它的「播放/暂停」解掉。
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "PlayPause"), "")
	app.Conf.Set(hotkeyConfKey(scopeGlobal, "Play"), "MediaPlayPause")
	app.syncHotkeys()
	if got := app.hotkeyTip(hkPlay); got != "（媒体播放/暂停键）" {
		t.Errorf("tooltip = %q，期望（媒体播放/暂停键）", got)
	}
	// 全局开关关掉之后它并不生效，后缀也要跟着消失。
	app.Conf.Set(hotkeyEnabledKey, false)
	app.syncHotkeys()
	if got := app.hotkeyTip(hkPlay); got != "" {
		t.Errorf("全局关闭后不该再报全局键，实际 %q", got)
	}
}
