//go:build !js

package mygo

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/ui"
)

func TestFitContentDIPs(t *testing.T) {
	f := &fitContent{min: platform.Size{Width: 100, Height: 40}, max: platform.Size{Width: 300, Height: 200}}
	for _, c := range []struct {
		w, h, zoom float64
		want       platform.Size
	}{
		{120, 80, 1, platform.Size{Width: 120, Height: 80}},
		{120.5, 80.25, 1, platform.Size{Width: 121, Height: 81}}, // fractions take a pixel
		{120.004, 80, 1, platform.Size{Width: 120, Height: 80}},  // but not rounding errors
		{60.25, 45, 2, platform.Size{Width: 121, Height: 90}},    // CSS pixels of a zoomed page
		{150, 100, 0, platform.Size{Width: 150, Height: 100}},    // no zoom is 1
		{10, 10, 1, platform.Size{Width: 100, Height: 40}},       // the minimum
		{500, 400, 1, platform.Size{Width: 300, Height: 200}},    // the maximum
		{0, 0, 1, platform.Size{Width: 100, Height: 40}},
	} {
		if got, ok := f.fit(contentSize{Width: c.w, Height: c.h}, c.zoom, 0); !ok || got != c.want {
			t.Errorf("fit(%v, %v, zoom %v) = %v, %v; want %v", c.w, c.h, c.zoom, got, ok, c.want)
		}
	}
	unbounded := &fitContent{}
	if got, _ := unbounded.fit(contentSize{Height: 1e300}, 1, 0); got != (platform.Size{Width: 1, Height: maxFitDIPs}) {
		t.Errorf("unbounded fit of an empty, huge page = %v", got)
	}
	for _, bad := range [][2]float64{{-1, 10}, {10, -0.5}} {
		if got, ok := f.fit(contentSize{Width: bad[0], Height: bad[1]}, 1, 0); ok {
			t.Errorf("fit(%v) = %v, want no size", bad, got)
		}
	}
}

// The room of the scrollbars counts where the content needs them: a
// vertical scrollbar where the content is taller than the window may be,
// not while the window is only shorter than the content for a moment.
func TestFitContentScrollbars(t *testing.T) {
	f := &fitContent{max: platform.Size{Width: 300, Height: 200}}
	for _, c := range []struct {
		what      string
		c         contentSize
		maxHeight int
		want      platform.Size
	}{
		{"a vertical scrollbar for a moment", contentSize{Width: 120, Height: 150, Scrollbar: 17}, 0, platform.Size{Width: 120, Height: 150}},
		{"taller than the maximum", contentSize{Width: 120, Height: 500, Scrollbar: 17}, 0, platform.Size{Width: 137, Height: 200}},
		{"taller than the work area", contentSize{Width: 120, Height: 150, Scrollbar: 17}, 100, platform.Size{Width: 137, Height: 100}},
		{"overlay scrollbars", contentSize{Width: 120, Height: 500}, 0, platform.Size{Width: 120, Height: 200}},
		{"wider than the maximum", contentSize{Width: 300, Height: 60, HorizontalScrollbar: 17}, 0, platform.Size{Width: 300, Height: 77}},
		{"zoomed", contentSize{Width: 150, Height: 60, HorizontalScrollbar: 8.5}, 0, platform.Size{Width: 300, Height: 137}},
		{"no room", contentSize{Width: 120, Height: 500, Scrollbar: -1, HorizontalScrollbar: math.NaN()}, 0, platform.Size{Width: 120, Height: 200}},
	} {
		zoom := 1.0
		if c.what == "zoomed" {
			zoom = 2
		}
		if got, ok := f.fit(c.c, zoom, c.maxHeight); !ok || got != c.want {
			t.Errorf("%s: fit(%+v) = %v, %v; want %v", c.what, c.c, got, ok, c.want)
		}
	}
}

// fitLimitsEvent matches the page event of FitContent's limits.
func fitLimitsEvent(minW, minH, maxW, maxH float64) func(m map[string]any) bool {
	return func(m map[string]any) bool {
		p, _ := m["p"].(map[string]any)
		return m["n"] == "mygo:fit-content" && p["minWidth"] == minW && p["minHeight"] == minH && p["maxWidth"] == maxW && p["maxHeight"] == maxH
	}
}

// A flyout with FitContent takes the size its page tells, within its
// limits, and its page hears the limits in its CSS pixels.
func TestFlyoutFitContent(t *testing.T) {
	parent, _ := testWindow(t, WindowOptions{X: 100, Y: 100, Width: 400, Height: 300})
	fl, fw := testFlyout(t, FlyoutOptions{
		Parent:     parent,
		Width:      150,
		Height:     120,
		FitContent: true,
		MinWidth:   100,
		MinHeight:  40,
		MaxWidth:   300,
		MaxHeight:  200,
	})
	if s := fw.Opts.UserScripts[0].Source; !strings.Contains(s, `"fit":{"minWidth":100,"minHeight":40,"maxWidth":300,"maxHeight":200}`) {
		t.Errorf("the bridge's configuration has no limits: %s", s[strings.LastIndex(s, "})("):])
	}
	if o := fw.Opts; o.MinSize != (platform.Size{}) || o.MaxSize != (platform.Size{}) || o.Width != 150 || o.Height != 120 {
		t.Errorf("flyout window options: size %dx%d, min %v, max %v", o.Width, o.Height, o.MinSize, o.MaxSize)
	}
	size := func(what string, w, h int) {
		t.Helper()
		if b := fl.Bounds(); b.Width != w || b.Height != h {
			t.Errorf("%s: flyout %dx%d, want %dx%d", what, b.Width, b.Height, w, h)
		}
	}
	size("before the page told its size", 150, 120)
	page(fw, `{"t":"content-size","w":120.5,"h":80}`)
	size("the content's size", 121, 80)
	page(fw, `{"t":"content-size","w":500,"h":400}`)
	size("too large", 300, 200)
	page(fw, `{"t":"content-size","w":20,"h":10}`)
	size("too small", 100, 40)
	page(fw, `{"t":"content-size","w":-1,"h":10}`)
	size("no size", 100, 40)

	// SetSize holds until the content's size changes.
	fl.SetSize(160, 90)
	page(fw, `{"t":"content-size","w":20,"h":10}`)
	size("the same content after SetSize", 160, 90)
	page(fw, `{"t":"content-size","w":200,"h":150}`)
	size("other content after SetSize", 200, 150)

	// Zoom changes the CSS pixels of the limits and of the content.
	page(fw, `{"t":"dom-ready"}`)
	fl.Page().SetZoomFactor(2)
	received(t, fw, fitLimitsEvent(50, 20, 150, 100))
	if s := fw.Opts.UserScripts[0].Source; !strings.Contains(s, `"fit":{"minWidth":50,"minHeight":20,"maxWidth":150,"maxHeight":100}`) {
		t.Errorf("the next pages' limits after a zoom: %s", s[strings.LastIndex(s, "})("):])
	}
	size("the content zoomed", 300, 200) // 200x150 CSS pixels, bounded
	page(fw, `{"t":"content-size","w":60,"h":30}`)
	size("zoomed content", 120, 60)

	// Native UI does not fit; nor does a flyout without FitContent.
	native, nfw := testFlyout(t, FlyoutOptions{Parent: parent, FitContent: true, Content: ui.View(func(c *ui.Context) {})})
	plain, pfw := testFlyout(t, FlyoutOptions{Parent: parent})
	onMain(func() {
		if native.fit != nil || plain.fit != nil {
			t.Errorf("fit of native UI %v, of a flyout without FitContent %v", native.fit, plain.fit)
		}
	})
	if strings.Contains(pfw.Opts.UserScripts[0].Source, `"fit"`) || len(nfw.Opts.UserScripts) > 0 && strings.Contains(nfw.Opts.UserScripts[0].Source, `"fit"`) {
		t.Error("limits in the bridge of a flyout that does not fit")
	}
	page(pfw, `{"t":"content-size","w":50,"h":50}`)
	if b := plain.Bounds(); b.Width != 200 || b.Height != 200 {
		t.Errorf("a flyout without FitContent took its content's size: %+v", b)
	}
}

// A window with FitContent sizes its page area to its content, in the
// dimension it changes in, within its limits and the work area.
func TestWindowFitContent(t *testing.T) {
	w, fw := testWindow(t, WindowOptions{X: 100, Y: 100, Width: 400, Height: 300, FitContent: true, MinHeight: 100, MaxWidth: 600})
	if s := fw.Opts.UserScripts[0].Source; !strings.Contains(s, `"fit":{"minWidth":0,"minHeight":100,"maxWidth":600,"maxHeight":0}`) {
		t.Errorf("the bridge's configuration has no limits: %s", s[strings.LastIndex(s, "})("):])
	}
	size := func(what string, width, height int) {
		t.Helper()
		if gw, gh := w.ContentSize(); gw != width || gh != height {
			t.Errorf("%s: page area %dx%d, want %dx%d", what, gw, gh, width, height)
		}
	}
	page(fw, `{"t":"content-size","w":250,"h":150}`)
	size("the content's size", 250, 150)
	if x, y := w.Position(); x != 100 || y != 100 {
		t.Errorf("the window moved to %d,%d", x, y)
	}

	// The user resizes it: that holds until the content changes, in the
	// dimension it changes in.
	w.SetContentSize(320, 200)
	page(fw, `{"t":"content-size","w":250,"h":150}`)
	size("the same content after a resize", 320, 200)
	page(fw, `{"t":"content-size","w":250,"h":180}`)
	size("taller content", 320, 180)
	page(fw, `{"t":"content-size","w":260,"h":180}`)
	size("wider content", 260, 180)

	// The user resizes it, and the content grows before the web view takes
	// the size: the page shows a vertical scrollbar for a moment, which
	// changes none of the content's width.
	w.SetContentSize(400, 200)
	page(fw, `{"t":"content-size","w":260,"h":220,"sw":17}`)
	size("taller content right after a resize", 400, 220)
	page(fw, `{"t":"content-size","w":260,"h":220}`)
	size("the scrollbar gone", 400, 220)

	// Its limits, and the fake display's work area (1440x875).
	page(fw, `{"t":"content-size","w":900,"h":5000}`)
	size("huge content", 600, 875)
	page(fw, `{"t":"content-size","w":200,"h":10}`)
	size("short content", 200, 100)

	// Not while maximized.
	w.Maximize()
	page(fw, `{"t":"content-size","w":300,"h":300}`)
	size("content of a maximized window", 200, 100)

	// Without FitContent, the content's size changes nothing.
	plain, pfw := testWindow(t, WindowOptions{Width: 400, Height: 300})
	if strings.Contains(pfw.Opts.UserScripts[0].Source, `"fit"`) {
		t.Error("limits in the bridge of a window that does not fit")
	}
	page(pfw, `{"t":"content-size","w":250,"h":150}`)
	if gw, gh := plain.ContentSize(); gw != 400 || gh != 300 {
		t.Errorf("a window without FitContent took its content's size: %dx%d", gw, gh)
	}
}
