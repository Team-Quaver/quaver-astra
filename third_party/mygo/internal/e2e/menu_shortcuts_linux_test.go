//go:build linux

package e2e

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The shortcuts of the app's menu items work in a window of native UI:
// before the content, which gets the rest. The roles' keys, as Edit ›
// Copy's Ctrl+C, go to the content first, as a terminal's Ctrl+C.
func TestNativeMenuShortcutsLinux(t *testing.T) {
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
	// A click gives the window the keyboard, which needs no window manager.
	if !click(w, 50, 50) {
		t.Skip("click automation unavailable")
	}
	counts := func() (s, p, c, ty int) {
		mygo.RunOnMain(func() { s, p, c, ty = settings, split, copied, typed })
		return
	}
	if !pressKeys("Control_L", "comma") {
		t.Skip("no X server to press keys with")
	}
	eventually(t, "Ctrl+, to run Settings", func() bool { s, _, _, _ := counts(); return s == 1 })
	pressKeys("Alt_L", "Shift_L", "equal")
	eventually(t, "Alt+Shift+= to run Split", func() bool { _, p, _, _ := counts(); return p == 1 })
	pressKeys("Control_L", "c")
	eventually(t, "the content's Ctrl+C", func() bool { _, _, c, _ := counts(); return c == 1 })
	if _, _, _, ty := counts(); ty != 0 {
		t.Errorf("Ctrl+, reached the content %d times", ty)
	}
}
