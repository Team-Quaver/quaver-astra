//go:build linux && (amd64 || arm64)

package e2e

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestCursorAfterLeaveThroughResizeEdge drives a frameless window whose
// bottom strip sets ew-resize, as a player's seek bar does. While a resize
// cursor of the edges shows, the surface puts the page's cursor requests
// off, and leaving the window through the edge made it put off the reset
// to the default: coming back elsewhere then brought the strip's stale
// cursor back, which the view, believing the default set, never corrected.
func TestCursorAfterLeaveThroughResizeEdge(t *testing.T) {
	var frames atomic.Int32
	view := func(c *ui.Context) {
		frames.Add(1)
		ui.Column(c).Fill().Children(func() {
			ui.Box(c).Grow(1)                              // the page
			ui.Box(c).Height(60).Cursor(ui.CursorResizeEW) // the seek bar
		})
	}
	w := newWindow(t, mygo.WindowOptions{Frameless: true, X: 60, Y: 60, Width: 320, Height: 240, Content: ui.View(view)})
	eventually(t, "a frame", func() bool { return frames.Load() > 0 })
	if _, ok := resizeCursor(w); !ok {
		t.Skip("frameless windows keep native resize borders on this platform")
	}
	page := func(want string) {
		t.Helper()
		eventually(t, fmt.Sprintf("the page's cursor %q", want), func() bool { c, _ := pageCursor(w); return c == want })
	}

	b := w.Bounds()
	if !movePointer(b.X+b.Width/2, b.Y+b.Height-30) {
		t.Skip("moving the pointer needs an X server")
	}
	page("ew-resize") // over the strip, away from the edge

	// On the outer pixels of the window, the edge's resize cursor shows,
	// and the strip's is saved to come back.
	movePointer(b.X+b.Width/2, b.Y+b.Height-2)
	eventually(t, "the bottom edge's cursor", func() bool {
		c, _ := resizeCursor(w)
		return c == "s-resize"
	})

	// Out of the window: the view sets the default back, which the edge's
	// cursor holds off.
	movePointer(b.X+b.Width/2, b.Y+b.Height+40)

	// Back elsewhere, the page's own cursor comes back: the default here,
	// not the strip's ew-resize the edge's cursor replaced.
	movePointer(b.X+b.Width/2, b.Y+80)
	page("default")
}
