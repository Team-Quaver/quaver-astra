//go:build linux && (amd64 || arm64)

package linux

import (
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// TestShapeRegion checks the region that shapes a surface's area with
// web views placed: the area but their clips, save the covers.
func TestShapeRegion(t *testing.T) {
	lib, err := open("libcairo.so.2")
	if err != nil {
		t.Skip("no cairo:", err)
	}
	var contains func(region ptr, x, y int32) bool
	for _, b := range []struct {
		fn   any
		name string
	}{
		{&cairoRegionCreateRectangle, "cairo_region_create_rectangle"},
		{&cairoRegionSubtractRectangle, "cairo_region_subtract_rectangle"},
		{&cairoRegionUnion, "cairo_region_union"},
		{&cairoRegionDestroy, "cairo_region_destroy"},
		{&contains, "cairo_region_contains_point"},
	} {
		if !bind(lib, b.fn, b.name) {
			t.Skip("no " + b.name)
		}
	}
	if r := shapeRegion(100, 80, nil); r != 0 {
		cairoRegionDestroy(r)
		t.Fatal("shapeRegion without web views = a region, want none (no shape)")
	}
	placed := []platform.WebViewPlacement{{
		Frame: platform.RectF{X: 10, Y: 20, W: 60, H: 40},
		// Partly scrolled out: the clip is what shows of the frame.
		Clip:   platform.RectF{X: 10.5, Y: 20, W: 59.5, H: 40},
		Covers: []platform.RectF{{X: 30.5, Y: 30, W: 10, H: 5}},
	}}
	r := shapeRegion(100, 80, placed)
	if r == 0 {
		t.Fatal("shapeRegion with a web view = none")
	}
	defer cairoRegionDestroy(r)
	for _, c := range []struct {
		x, y int32
		want bool
	}{
		{0, 0, true},     // the content around the page
		{99, 79, true},   // the area's last pixel
		{100, 40, false}, // past the area
		{9, 30, true},    // left of the page
		{10, 30, true},   // the clip covers it in part: the content's
		{11, 30, false},  // the page
		{69, 59, false},  // the page's last pixel
		{70, 59, true},   // right of the page
		{30, 30, true},   // a cover over it in part
		{35, 34, true},   // a cover over it
		{41, 34, false},  // past the cover
		{35, 35, false},  // below the cover
	} {
		if got := contains(r, c.x, c.y); got != c.want {
			t.Errorf("region contains %d, %d = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}
