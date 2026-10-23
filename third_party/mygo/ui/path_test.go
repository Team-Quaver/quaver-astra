package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/internal/raster"
	"github.com/egoist/mygo/internal/scene"
)

// TestPathsLargerThanAMaskDraw fills and strokes paths 2480 DIPs wide at
// twice the pixels, wider than the mask atlas: they draw from end to end,
// and a gradient spans the whole path.
func TestPathsLargerThanAMaskDraw(t *testing.T) {
	blue, red := RGB(20, 60, 230), RGB(230, 40, 20)
	img := coreRender(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			var line, band, under Path
			line.MoveTo(10, 20).LineTo(2490, 20)
			p.StrokePath(&line, 4, blue)
			band.MoveTo(10, 40).LineTo(2490, 40).LineTo(2490, 60).LineTo(10, 60).Close()
			p.FillPath(&band, blue)
			under.MoveTo(10, 70).LineTo(2490, 70).LineTo(2490, 90).LineTo(10, 90).Close()
			p.FillPathGradient(&under, LinearGradient{Angle: 90, From: red, To: blue})
		})
	}, 2500, 100, 2)
	isBlue := func(c color.RGBA) bool { return c.B > 180 && c.R < 120 && c.G < 120 }
	isRed := func(c color.RGBA) bool { return c.R > 180 && c.B < 120 && c.G < 120 }
	for _, x := range []int{30, 2500, 4970} {
		if c := img.RGBAAt(x, 40); !isBlue(c) {
			t.Errorf("the stroke shows %v at (%d, 40)", c, x)
		}
		if c := img.RGBAAt(x, 100); !isBlue(c) {
			t.Errorf("the fill shows %v at (%d, 100)", c, x)
		}
	}
	if c := img.RGBAAt(30, 160); !isRed(c) {
		t.Errorf("the gradient starts with %v", c)
	}
	if c := img.RGBAAt(4970, 160); !isBlue(c) {
		t.Errorf("the gradient ends with %v", c)
	}
}

// TestTiledPathsMatchOneMask draws paths from one mask each, and again in
// tiles of 17 pixels: a fill whose subpaths overlap and wind around a
// hole (non-zero winding), a curved stroke crossing itself, a closed
// stroke, and fills and strokes in linear and conic gradients across their
// whole bounds. The pixels are the same, within a level of 255 where the
// rasterizer starts an edge at a tile's top or left rather than above or
// left of it.
func TestTiledPathsMatchOneMask(t *testing.T) {
	blue, red := RGB(20, 60, 230), RGB(230, 40, 20)
	view := func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			var star, ring, wave, frame Path
			star.MoveTo(60, 8).LineTo(92, 110).LineTo(8, 46).LineTo(112, 46).LineTo(28, 110).Close()
			p.FillPath(&star, blue)
			ring.Circle(180, 60, 50).Circle(180, 60, 25).Circle(205.3, 60.2, 20)
			p.FillPath(&ring, red)
			wave.MoveTo(240, 100).CubeTo(270, -40, 300, 200, 330, 20).QuadTo(380, 140, 250, 30)
			p.StrokePath(&wave, 7.5, blue)
			frame.MoveTo(20.3, 130.6).LineTo(150.1, 140.2).LineTo(110.7, 200.9).Close()
			p.StrokePath(&frame, 3, red)
			var lens Path
			lens.Circle(220, 170, 40)
			p.FillPathGradient(&lens, LinearGradient{Angle: 135, From: red, To: blue})
			p.StrokePathGradient(&wave, 2, LinearGradient{Angle: 90, From: blue, To: red})
			var disc Path
			disc.Circle(320, 170, 35)
			p.FillPathConicGradient(&disc, ConicGradient{Stops: []GradientStop{{red, 0}, {blue, 0.5}, {red, 1}}})
			p.StrokePathConicGradient(&frame, 1.5, ConicGradient{Stops: []GradientStop{{blue, 0}, {red, 1}}})
		})
	}
	// render draws the view at 2x with the coverage of masks as they are
	// rasterized: on Windows, renderers correct it for paths as for text,
	// as Direct2D does, which can make a level of difference two.
	render := func() *image.RGBA {
		tt := coreNewTester(view, 380, 220)
		tt.SetScale(2)
		s := tt.h.last
		s.Text = scene.TextParams{}
		var r raster.Renderer
		r.Render(s)
		return &image.RGBA{Pix: r.Image.RGBA(), Stride: 4 * r.Image.W, Rect: image.Rect(0, 0, r.Image.W, r.Image.H)}
	}
	whole := render()
	defer func(most, tile int) { pathMaskMax, pathTile = most, tile }(pathMaskMax, pathTile)
	pathMaskMax, pathTile = 0, 17
	tiled := render()
	worst := 0
	for i := range whole.Pix {
		d := int(whole.Pix[i]) - int(tiled.Pix[i])
		worst = max(worst, d, -d)
	}
	if worst > 1 {
		t.Errorf("tiled paths differ by up to %d levels from paths of one mask", worst)
	}
}

// TestLargePathsDrawOnlyWhatShows draws paths tens of thousands of pixels
// across in a small window: a horizontal line, a diagonal one and a square
// around the window. They draw only the tiles that show and that they
// reach, and the square's tiles inside it share their masks.
func TestLargePathsDrawOnlyWhatShows(t *testing.T) {
	blue := RGB(20, 60, 230)
	tt := coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			var square, line, diagonal Path
			square.MoveTo(-9000, -9000).LineTo(9000, -9000).LineTo(9000, 9000).LineTo(-9000, 9000).Close()
			p.FillPath(&square, RGB(230, 230, 230))
			line.MoveTo(-20000, 100).LineTo(20000, 100)
			p.StrokePath(&line, 4, blue)
			diagonal.MoveTo(-10000, -10000).LineTo(10000, 10000)
			p.StrokePath(&diagonal, 4, blue)
		})
	}, 400, 200)
	tt.SetScale(2)
	img := tt.Image()
	for _, pt := range [][2]int{{4, 200}, {796, 200}, {300, 300}, {10, 10}} {
		if c := img.RGBAAt(pt[0], pt[1]); c.B < 180 || c.R > 120 {
			t.Errorf("(%d, %d) is %v, not the lines' blue", pt[0], pt[1], c)
		}
	}
	if c := img.RGBAAt(700, 50); c != (color.RGBA{230, 230, 230, 255}) {
		t.Errorf("the square shows %v", c)
	}
	// 800×400 pixels show, in two by two tiles of the square's mask and
	// the diagonal's (511 pixels, from their masks' corners), one by two of
	// the line's. The square has no edge there: its tiles share one mask.
	tiles, masks := map[uint8]int{}, map[[2]uint16]bool{}
	for _, g := range tt.rt.scene.Glyphs {
		tiles[g.Color.G]++
		masks[[2]uint16{g.U, g.V}] = true
	}
	if tiles[230] != 4 || tiles[60] > 2+4 || len(masks) > 1+2+4 {
		t.Errorf("the square draws %d tiles, the lines %d, from %d masks", tiles[230], tiles[60], len(masks))
	}
}

// TestPathsTheAtlasHoldsDrawFromOneMask draws a path 3000 pixels wide, as
// it is and under a transform off the pixel grid: the atlas holds its
// mask, so it draws from one, as before tiles.
func TestPathsTheAtlasHoldsDrawFromOneMask(t *testing.T) {
	for _, moved := range []bool{false, true} {
		tt := coreNewTester(func(c *context) {
			coreBox(c).Fill().Draw(func(p *Painter, r Rect) {
				draw := func() {
					var band Path
					band.MoveTo(0, 10).LineTo(1500, 10).LineTo(1500, 60).LineTo(0, 60).Close()
					p.FillPath(&band, RGB(20, 60, 230))
				}
				if moved {
					p.Transform(Translation(0.3, 0), 0, 0, draw)
				} else {
					draw()
				}
			})
		}, 1600, 100)
		tt.SetScale(2)
		if n := len(tt.rt.scene.Glyphs); n != 1 {
			t.Errorf("transformed %v, the path draws %d masks", moved, n)
		}
	}
}
