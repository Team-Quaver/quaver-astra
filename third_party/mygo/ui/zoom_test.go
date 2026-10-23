package ui

import "testing"

// A chart in a scroll container zooms with a mouse wheel and pinches, and
// pans with a touchpad's scrolling, with where the pointer is over it:
// the container scrolls with neither.
func TestChartZoomsAndPans(t *testing.T) {
	var page ScrollState
	scale, panX := float32(1), float32(0)
	var at [2]float32
	tt := NewTester(func(c *Context) {
		Scroll(c).Fill().TrackScroll(&page).Children(func() {
			Box(c).Height(100).Shrink(0)
			Box(c).Size(200, 100).Shrink(0).HandleInput(func(ev InputEvent) bool {
				switch {
				case ev.Kind == InputZoom:
					scale *= ev.Scale
				case ev.Kind == InputScroll && !ev.Precise:
					if ev.DY < 0 {
						scale *= 1.25
					} else if ev.DY > 0 {
						scale *= 0.8
					}
				case ev.Kind == InputScroll:
					panX += ev.DX
				default:
					return false
				}
				at = [2]float32{ev.X, ev.Y}
				return true
			})
			Box(c).Height(1000).Shrink(0)
		})
	}, 200, 200)
	defer tt.rt.close()
	tt.Frame()

	tt.Wheel(50, 130, 0, -40)
	tt.Advance(wheelAnimation.Duration)
	if scale != 1.25 || page.Y != 0 {
		t.Errorf("a notch up zoomed to %v and scrolled the page to %v, want 1.25 and 0", scale, page.Y)
	}
	if at != [2]float32{50, 30} {
		t.Errorf("the wheel was at %v of the chart, want [50 30]", at)
	}
	tt.Pinch(150, 160, 2)
	if scale != 2.5 || at != [2]float32{150, 60} {
		t.Errorf("a pinch zoomed to %v at %v, want 2.5 at [150 60]", scale, at)
	}
	tt.Scroll(60, 150, 30, 5)
	if panX != 30 || page.Y != 0 {
		t.Errorf("the touchpad panned %v and scrolled the page to %v, want 30 and 0", panX, page.Y)
	}

	// Over the rest of the page, the touchpad scrolls the page, and a
	// pinch zooms nothing.
	tt.Scroll(50, 50, 0, 40)
	tt.Pinch(50, 20, 3)
	tt.Frame()
	if page.Y != 40 || scale != 2.5 {
		t.Errorf("over the page: scrolled to %v, zoomed to %v; want 40 and 2.5", page.Y, scale)
	}
}

// An element that leaves scrolling to the scroll container still gets
// the pinches it takes, and a pinch that zooms by 1 comes to no element.
func TestZoomLeftToContainer(t *testing.T) {
	var page ScrollState
	var zooms []float32
	tt := NewTester(func(c *Context) {
		Scroll(c).Fill().TrackScroll(&page).Children(func() {
			Box(c).Size(200, 100).Shrink(0).HandleInput(func(ev InputEvent) bool {
				if ev.Kind == InputZoom {
					zooms = append(zooms, ev.Scale)
					return true
				}
				return false
			})
			Box(c).Height(1000).Shrink(0)
		})
	}, 200, 200)
	defer tt.rt.close()
	tt.Frame()
	tt.Scroll(50, 50, 0, 30)
	tt.Pinch(50, 20, 0.5)
	tt.Pinch(50, 20, 1) // no zoom
	tt.Frame()
	if page.Y != 30 || len(zooms) != 1 || zooms[0] != 0.5 {
		t.Errorf("page scrolled to %v, zooms %v; want 30 and [0.5]", page.Y, zooms)
	}
}
