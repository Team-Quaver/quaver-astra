package appui

import (
	"bytes"
	"image"
	_ "image/png"
	"testing"
)

func TestAppIconAssetsAreSquare(t *testing.T) {
	if w, h := appIconSVG.Size(); w != 512 || h != 512 {
		t.Fatalf("产品 SVG 尺寸为 %v×%v，期望 512×512", w, h)
	}
	png, format, err := image.Decode(bytes.NewReader(AppIconPNG()))
	if err != nil {
		t.Fatalf("产品 PNG 无法解码：%v", err)
	}
	if format != "png" {
		t.Fatalf("系统图标格式为 %q，期望 png", format)
	}
	if got, want := png.Bounds().Size(), (image.Point{X: 512, Y: 512}); got != want {
		t.Fatalf("系统图标尺寸为 %v，期望 %v", got, want)
	}
}

// TestSettingsAboutShowsAppIconLeftOfCopy 锁住“关于”页品牌布局：产品图标
// 必须位于标题/简介的左侧，而不是只显示两行文字。
func TestSettingsAboutShowsAppIconLeftOfCopy(t *testing.T) {
	app, tester := sideTestApp(t, false)
	app.settings.tab = 2
	tester.Frame()

	title := mustFind(t, tester, "Quaver Astra")
	desc := mustFind(t, tester, "现代，流畅的 Q 音第三方客户端，现已轻装上阵")
	if title.X != desc.X {
		t.Fatalf("标题 X=%v 应与简介同列，简介 X=%v", title.X, desc.X)
	}

	// 图标区在标题左边，约 56×56 DIP。品牌 SVG 的奶油底、蓝/绿/棕图形
	// 都是彩色像素；若布局退回纯文字，这一区域只剩窗口底色。
	x0, x1 := int(title.X)-70, int(title.X)-8
	y0, y1 := int(title.Y)-18, int(desc.Y+desc.H)+8
	img := tester.Image()
	colored := 0
	for y := max(y0, 0); y <= min(y1, img.Bounds().Dy()-1); y++ {
		for x := max(x0, 0); x <= min(x1, img.Bounds().Dx()-1); x++ {
			c := at(img, x, y)
			lo, hi := min(min(int(c.R), int(c.G)), int(c.B)), max(max(int(c.R), int(c.G)), int(c.B))
			if hi-lo > 18 {
				colored++
			}
		}
	}
	if colored < 80 {
		t.Fatalf("标题左侧图标区只有 %d 个彩色像素（扫描 x=%d..%d, y=%d..%d）", colored, x0, x1, y0, y1)
	}
}
