//go:build windows

package e2e

import (
	"testing"

	"github.com/egoist/mygo"
	win "github.com/egoist/mygo/internal/windows"
	"github.com/egoist/mygo/ui"
)

// The shortcuts of the app's menu items work in a window of native UI, as
// in a page: before the content, which gets the rest. The roles' keys, as
// Edit › Copy's Ctrl+C, go to the content first, as a terminal's Ctrl+C.
func TestNativeMenuShortcuts(t *testing.T) {
	var settings, split, copied, typed int
	prev := mygo.App.Menu()
	defer mygo.App.SetMenu(prev)
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "App", Submenu: []*mygo.MenuItem{
			{Label: "Settings", Accelerator: "Ctrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { settings++ }},
			{Label: "Split", Accelerator: "Alt+Shift+=", Click: func(*mygo.MenuItem, *mygo.Window) { split++ }},
		}},
		{Role: mygo.RoleEditMenu},
	}))
	w := newWindow(t, mygo.WindowOptions{Width: 300, Height: 200, Content: ui.View(func(c *ui.Context) {
		c.OnShortcut(ui.Ctrl, ui.KeyC, func() { copied++ })
		c.OnShortcut(ui.Ctrl, ui.KeyComma, func() { typed++ })
		ui.Text(c, "shortcuts")
	})})
	const vkControl, vkShift, vkMenu, vkComma, vkEqual = 0x11, 0x10, 0x12, 0xBC, 0xBB
	key := func(vk byte, mods ...byte) {
		t.Helper()
		var ok bool
		mygo.RunOnMain(func() { ok = win.TestSurfaceKey(w.NativeHandle(), vk, mods...) })
		if !ok {
			t.Fatal("no window of native UI to press keys in")
		}
	}
	counts := func() (s, p, c, ty int) {
		mygo.RunOnMain(func() { s, p, c, ty = settings, split, copied, typed })
		return
	}
	key(vkComma, vkControl)
	key(vkEqual, vkMenu, vkShift)
	key('C', vkControl)
	eventually(t, "the content's Ctrl+C", func() bool { _, _, c, _ := counts(); return c == 1 })
	if s, p, _, ty := counts(); s != 1 || p != 1 || ty != 0 {
		t.Errorf("Ctrl+, ran Settings %d times and reached the content %d, Alt+Shift+= ran Split %d times; want once, never, once", s, ty, p)
	}
}
