//go:build !js

package e2e

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/internal/wait"
)

const fitPage = `<!doctype html><body style="margin:0;background:#fc0"><div id="d" style="width:120px;height:80px"></div></body>`

// waitSize waits for size to return width and height, and says what it
// returned and what the page measured if it never does.
func waitSize(t *testing.T, what string, w *mygo.Window, size func() (int, int), width, height int) {
	t.Helper()
	var gotW, gotH int
	if !wait.Poll(func() bool {
		gotW, gotH = size()
		return gotW == width && gotH == height
	}) {
		t.Fatalf("%s: %dx%d never happened (waited %v): %dx%d; page %v", what, width, height, wait.Guard, gotW, gotH, pageMeasures(w))
	}
}

// pageMeasures returns what a page measures of its viewport and its root
// element, for a failure to say.
func pageMeasures(w *mygo.Window) any {
	v, err := w.Page().Eval(`(() => { const r = document.documentElement, b = r.getBoundingClientRect(); return {inner: [innerWidth, innerHeight], client: [r.clientWidth, r.clientHeight], scroll: [r.scrollWidth, r.scrollHeight], root: [b.width, b.height], zoom: devicePixelRatio} })()`)
	if err != nil {
		return err
	}
	return v
}

// scrollbarHeight returns the height of the page's horizontal scrollbar in
// CSS pixels: 0 where scrollbars overlay the page.
func scrollbarHeight(t *testing.T, w *mygo.Window) float64 {
	t.Helper()
	v, _ := mustEval(t, w, `innerHeight - document.documentElement.clientHeight`).(float64)
	return v
}

// TestFlyoutFitContent sizes a flyout to its page's content, follows the
// content as it changes, within the limits, wraps text at the maximum
// width, and takes the page's zoom into account.
func TestFlyoutFitContent(t *testing.T) {
	work := mygo.Screen.PrimaryDisplay().WorkArea
	parent := newWindow(t, mygo.WindowOptions{Width: 300, Height: 200, X: work.X + 100, Y: work.Y + 100})
	fl := mygo.NewFlyout(mygo.FlyoutOptions{
		Parent:     parent,
		Anchor:     mygo.Rectangle{X: 10, Y: 10},
		Width:      50,
		Height:     50,
		FitContent: true,
		MinWidth:   100,
		MinHeight:  40,
		MaxWidth:   300,
		MaxHeight:  200,
	})
	defer fl.Destroy()
	size := func() (int, int) { b := fl.Bounds(); return b.Width, b.Height }
	fl.Page().LoadHTML(fitPage, "")
	waitSize(t, "the content's size", fl, size, 120, 80)

	mustEval(t, fl, `d.style.width = "180px"; d.style.height = "150px"`)
	waitSize(t, "larger content", fl, size, 180, 150)
	mustEval(t, fl, `d.style.width = "20px"; d.style.height = "10px"`)
	waitSize(t, "content below the minimum", fl, size, 100, 40)
	mustEval(t, fl, `d.style.width = "500px"; d.style.height = "400px"`)
	waitSize(t, "content beyond the maximum", fl, size, 300, 200)

	// Text wraps at the maximum width, the room of a vertical scrollbar
	// left out of it, and the page scrolls.
	mustEval(t, fl, `d.style.width = d.style.height = ""; d.textContent = "word ".repeat(400)`)
	waitSize(t, "wrapped text", fl, size, 300, 200)
	waitFor(t, fl, `document.scrollingElement.scrollWidth <= document.scrollingElement.clientWidth && document.scrollingElement.scrollHeight > 200`)

	// The content shrinks back, and its scrollbar goes.
	mustEval(t, fl, `d.textContent = ""; d.style.width = "150px"; d.style.height = "60px"`)
	waitSize(t, "smaller content", fl, size, 150, 60)

	// A zoomed page's CSS pixels are larger.
	fl.Page().SetZoomFactor(2)
	waitSize(t, "zoomed content", fl, size, 300, 120)
	mustEval(t, fl, `d.style.width = "60px"`)
	waitSize(t, "narrower zoomed content", fl, size, 120, 120)
	// Wider than the maximum, the page scrolls sideways, and the window
	// takes the room of a horizontal scrollbar that doesn't overlay the
	// page, as on Windows.
	mustEval(t, fl, `d.style.width = "400px"`)
	waitFor(t, fl, `document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	waitSize(t, "wider zoomed content", fl, size, 300, 120+int(math.Ceil(2*scrollbarHeight(t, fl))))
}

// TestWindowFitContent sizes a window's page area to its content, and
// keeps a size given to it until the content changes, in the dimension it
// changes in.
func TestWindowFitContent(t *testing.T) {
	w := newWindow(t, mygo.WindowOptions{Width: 500, Height: 400, FitContent: true})
	w.Page().LoadHTML(`<!doctype html><body style="margin:0"><div id="d" style="width:320px;height:180px"></div></body>`, "")
	waitSize(t, "the content's size", w, w.ContentSize, 320, 180)
	mustEval(t, w, `d.style.height = "240px"`)
	waitSize(t, "taller content", w, w.ContentSize, 320, 240)

	w.SetContentSize(400, 300)
	waitSize(t, "the size given to it", w, w.ContentSize, 400, 300)
	mustEval(t, w, `d.style.height = "260px"`)
	waitSize(t, "taller content again", w, w.ContentSize, 400, 260)

	// The content changes right as the window is resized, before its page
	// takes the size, and shows a vertical scrollbar for a moment: the
	// width given to it holds.
	w.SetContentSize(450, 260)
	mustEval(t, w, `d.style.height = "280px"`)
	waitSize(t, "taller content right after a resize", w, w.ContentSize, 450, 280)
}
