package glass

import (
	"image"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// pixelAt returns the color of img at (x, y).
func pixelAt(img *image.RGBA, x, y int) [3]int {
	p := img.RGBAAt(x, y)
	return [3]int{int(p.R), int(p.G), int(p.B)}
}

func TestGlassPaintsOverWhatIsBehind(t *testing.T) {
	g := Glass{}
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(20, 120, 220)).Children(func() {
			ui.Row(c).Size(200, 44).Radius(22).Material(g).Children(func() {
				ui.Text(c, "On glass")
			})
		})
	}
	tt := ui.NewTester(view, 300, 100)
	tt.SetScale(2)
	if !tt.HasText("On glass") {
		t.Error("the glass's children did not paint")
	}
	// The glass lightens the blue behind it, which shows through.
	light := pixelAt(tt.Image(), 300, 70)
	if light[0] < 100 || light[2] < light[0] {
		t.Errorf("light glass over blue is %v", light)
	}
	tt.SetDark(true)
	if dark := pixelAt(tt.Image(), 300, 70); dark[2] >= light[2] || dark[0] >= light[0] {
		t.Errorf("dark glass over blue is %v, not darker than light glass's %v", dark, light)
	}
	tt.SetDark(false)
	g.Style = Clear
	tt.Frame()
	if clear := pixelAt(tt.Image(), 300, 70); clear[0] >= light[0] {
		t.Errorf("clear glass over blue is %v, not clearer than regular glass's %v", clear, light)
	}
}

func TestInteractiveGlassGrowsWhilePressed(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
			ui.Box(c).Absolute().Left(50).Top(50).Size(200, 100).Radius(20).Material(Glass{Interactive: true})
		})
	}
	tt := ui.NewTester(view, 300, 200)
	tt.SetScale(2)
	// It grows at once.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	// The pixel left of the glass's left edge, in the middle.
	outside := func() int { return pixelAt(tt.Image(), 99, 200)[0] }
	before := outside()
	tt.Press(150, 100)
	tt.Frame()
	if grown := outside(); grown <= before+50 {
		t.Errorf("pressed, the pixel left of the glass is %d, as before (%d)", grown, before)
	}
	tt.Release(150, 100)
	tt.Frame()
	if after := outside(); after != before {
		t.Errorf("after the release, the pixel left of the glass is %d, not %d", after, before)
	}
}

func TestPaintInDrawing(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
			Paint(p, ui.Rect{X: 20, Y: 20, W: 100, H: 60}, 20, Glass{})
		})
	}
	tt := ui.NewTester(view, 200, 120)
	// Light glass over black is mid gray.
	if g := pixelAt(tt.Image(), 70, 50); g[0] < 100 || g[0] > 180 {
		t.Errorf("light glass over black is %v", g)
	}
}

func TestString(t *testing.T) {
	if s := (Glass{Style: Clear, Tint: ui.RGB(1, 2, 3), Interactive: true}).String(); s != "glass clear tint(#010203ff) interactive" {
		t.Errorf("String: %q", s)
	}
}

// Panes up to 100 DIPs, as controls, keep the measured material,
// whatever the clarity; from 200, as sidebars, theirs is the clarity's;
// between, it eases from one to the other.
func TestPaneMaterial(t *testing.T) {
	near := func(a, b float32) bool { return a-b < 1e-5 && b-a < 1e-5 }
	for _, k := range []float32{0, 0.25, 0.5, 1} {
		for _, m := range []float32{20, 44, 72, 100} {
			for _, s := range []float32{1, 2} {
				bezel := min(36, m/2)
				blur := min(max(m*0.035, 1), 10) * s
				light := paneMaterial(m, s, k, false, false)
				if light.blur != blur || light.bezel != bezel*s || light.refraction != 1.6*bezel*s || light.rim != 0.35 ||
					light.low != 0.541 || light.high != 1 {
					t.Errorf("light %v DIPs at %vx, clarity %v: %+v", m, s, k, light)
				}
				dark := paneMaterial(m, s, k, false, true)
				if dark.blur != blur || dark.rim != 0.35 || dark.low != 0.15 || dark.high != 0.51 {
					t.Errorf("dark %v DIPs at %vx, clarity %v: %+v", m, s, k, dark)
				}
			}
		}
	}
	for _, m := range []float32{200, 300, 900} {
		light, dark := paneMaterial(m, 2, 0.5, false, false), paneMaterial(m, 2, 0.5, false, true)
		if !near(light.blur, 10) || !near(light.bezel, 96) || !near(light.rim, 0.5) || !near(light.low, 0.35) || light.high != 1 {
			t.Errorf("light %v DIPs: %+v", m, light)
		}
		if !near(dark.blur, 10) || !near(dark.high, 0.6) || dark.low != 0.15 || !near(dark.rim, 0.5) {
			t.Errorf("dark %v DIPs: %+v", m, dark)
		}
	}
	// Between, every parameter lies between the two and moves one way.
	prev := paneMaterial(100, 1, 0.5, false, false)
	for m := float32(110); m <= 200; m += 10 {
		mat := paneMaterial(m, 1, 0.5, false, false)
		if mat.low > prev.low || mat.rim < prev.rim || mat.bezel < prev.bezel || mat.low < 0.35 || mat.rim > 0.5 {
			t.Errorf("%v DIPs: %+v after %+v", m, mat, prev)
		}
		prev = mat
	}
	if mid := paneMaterial(150, 1, 0.5, false, false); !near(mid.low, (0.541+0.35)/2) || !near(mid.rim, 0.425) {
		t.Errorf("150 DIPs, half way: %+v", mid)
	}
	// Clear glass's tone is the same at any size.
	if small, large := paneMaterial(60, 1, 0.5, true, false), paneMaterial(400, 1, 0.5, true, false); small.low != large.low || small.high != large.high || small.blur != large.blur {
		t.Errorf("clear: %+v and %+v", small, large)
	}
}

// A large pane's clarity runs from the measured material, at 0, through
// a clearer one at 0.5, to one nearly clear at 1, every parameter moving
// one way; out of range it is the nearest end.
func TestPaneClarity(t *testing.T) {
	near := func(a, b float32) bool { return a-b < 1e-5 && b-a < 1e-5 }
	type tone struct{ blur, light, darkLow, darkHigh, rim, bezel float32 }
	at := func(k float32) tone {
		l, d := paneMaterial(400, 1, k, false, false), paneMaterial(400, 1, k, false, true)
		if l.blur != d.blur || l.rim != d.rim || l.bezel != d.bezel {
			t.Errorf("clarity %v: light %+v and dark %+v differ", k, l, d)
		}
		return tone{l.blur, l.low, d.low, d.high, l.rim, l.bezel}
	}
	same := func(a, b tone) bool {
		return near(a.blur, b.blur) && near(a.light, b.light) && near(a.darkLow, b.darkLow) &&
			near(a.darkHigh, b.darkHigh) && near(a.rim, b.rim) && near(a.bezel, b.bezel)
	}
	for _, c := range []struct {
		k    float32
		want tone
	}{
		{0, tone{10, 0.541, 0.15, 0.51, 0.35, 36}},
		{0.25, tone{7.5, 0.4455, 0.15, 0.555, 0.425, 42}},
		{0.5, tone{5, 0.35, 0.15, 0.6, 0.5, 48}},
		{0.75, tone{3.5, 0.275, 0.125, 0.725, 0.5, 48}},
		{1, tone{2, 0.2, 0.1, 0.85, 0.5, 48}},
		{-1, tone{10, 0.541, 0.15, 0.51, 0.35, 36}},
		{2, tone{2, 0.2, 0.1, 0.85, 0.5, 48}},
	} {
		if got := at(c.k); !same(got, c.want) {
			t.Errorf("clarity %v: %+v, want %+v", c.k, got, c.want)
		}
	}
	// Clearer, it blurs less, lightens and darkens less, its rim brighter
	// and its bezel wider, or as before.
	prev := at(0)
	for k := float32(0.05); k <= 1.001; k += 0.05 {
		cur := at(k)
		if cur.blur > prev.blur || cur.light > prev.light || cur.darkLow > prev.darkLow || cur.darkHigh < prev.darkHigh ||
			cur.rim < prev.rim || cur.bezel < prev.bezel {
			t.Errorf("clarity %v: %+v after %+v", k, cur, prev)
		}
		prev = cur
	}
	// A pane between a control's size and a sidebar's eases toward it.
	for _, k := range []float32{0, 0.5, 1} {
		small, mid, large := paneMaterial(100, 1, k, false, false), paneMaterial(150, 1, k, false, false), paneMaterial(200, 1, k, false, false)
		if !near(mid.low, (small.low+large.low)/2) || !near(mid.rim, (small.rim+large.rim)/2) {
			t.Errorf("clarity %v, 150 DIPs: %+v, not half way between %+v and %+v", k, mid, small, large)
		}
	}
}

// Glass of a clarity paints a large pane clearer: black under it
// lighter in light mode the less clear, the same for small panes.
func TestClarityPaints(t *testing.T) {
	gray := func(k float32, w, h float32) int {
		view := func(c *ui.Context) {
			ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
				Paint(p, ui.Rect{X: 10, Y: 10, W: w, H: h}, 20, Glass{Clarity: k})
			})
		}
		tt := ui.NewTester(view, 420, 320)
		return pixelAt(tt.Image(), int(10+w/2), int(10+h/2))[0]
	}
	frost, mid, clear := gray(0, 400, 300), gray(0.5, 400, 300), gray(1, 400, 300)
	if !(frost > mid && mid > clear) {
		t.Errorf("black under a large pane at clarity 0, 0.5, 1: %d, %d, %d", frost, mid, clear)
	}
	if a, b := gray(0, 100, 60), gray(1, 100, 60); a != b {
		t.Errorf("black under a small pane at clarity 0 and 1: %d and %d", a, b)
	}
}

func TestStringClarity(t *testing.T) {
	if s := (Glass{Clarity: 0.5}).String(); s != "glass clarity(0.5)" {
		t.Errorf("String: %q", s)
	}
}

// At clarity 0 every pane, of any size and style, is exactly the
// material it was before clarity: the measured one.
func TestClarityZero(t *testing.T) {
	for _, m := range []float32{1, 20, 72, 100, 150, 199, 200, 260, 900, 4000} {
		for _, s := range []float32{1, 1.5, 2} {
			bezel := min(36, m/2)
			want := material{bezel: bezel * s, refraction: 1.6 * bezel * s, rim: 0.35, rimWidth: 1, light: math.Pi / 2}
			for _, c := range []struct {
				clear, dark                        bool
				blur, low, high, curve, saturation float32
			}{
				{true, false, s, 0.125, 1.082, 1, 1},
				{true, true, s, 0.125, 1.082, 1, 1},
				{false, true, min(max(m*0.035, 1), 10) * s, 0.15, 0.51, 2, 2.2},
				{false, false, min(max(m*0.035, 1), 10) * s, 0.541, 1, 1.2, 1},
			} {
				w := want
				w.blur, w.low, w.high, w.curve, w.saturation = c.blur, c.low, c.high, c.curve, c.saturation
				if got := paneMaterial(m, s, 0, c.clear, c.dark); got != w {
					t.Errorf("%v DIPs at %vx, clear %v, dark %v: %+v, want %+v", m, s, c.clear, c.dark, got, w)
				}
			}
		}
	}
}

// On clear glass, clarity leaves the tone as it is and only lights the
// rim and curves the bezel.
func TestClarityClear(t *testing.T) {
	frost, clear := paneMaterial(400, 1, 0, true, false), paneMaterial(400, 1, 1, true, false)
	if frost.blur != clear.blur || frost.low != clear.low || frost.high != clear.high {
		t.Errorf("tone: %+v and %+v", frost, clear)
	}
	if clear.rim != 0.5 || clear.bezel != 48 {
		t.Errorf("rim and bezel: %+v", clear)
	}
}
