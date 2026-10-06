package colorprobe

import (
	"image"
	"image/color"
	"testing"
)

func TestDominantFindsHue(t *testing.T) {
	// 一张主色为红色的图
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	red := color.RGBA{200, 30, 30, 255}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, red)
		}
	}
	r, ok := Dominant(img)
	if !ok {
		t.Fatal("no dominant color found")
	}
	if r.H > 30 || r.H > 360-30 && r.H < 330 {
		t.Errorf("hue should be near red (0/360), got %v", r.H)
	}
}

func TestDominantGrayImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	gray := color.RGBA{128, 128, 128, 255}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, gray)
		}
	}
	if _, ok := Dominant(img); ok {
		t.Error("pure gray should not yield an accent")
	}
}

func TestAccentRanges(t *testing.T) {
	r := Result{H: 120, S: 0.8, L: 0.9}
	rr, gg, bb := Accent(r, false)
	if rr > 160 || gg < 100 {
		t.Errorf("light accent too dark/saturated: %d %d %d", rr, gg, bb)
	}
}

func TestBlurredProducesSmallImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 500, 500))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	out := Blurred(img)
	if out.Bounds().Dx() != 48 || out.Bounds().Dy() != 48 {
		t.Errorf("blur size %v", out.Bounds())
	}
}
