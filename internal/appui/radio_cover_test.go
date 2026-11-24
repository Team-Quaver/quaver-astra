package appui

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRadioClosedCoverIsThemedGradient 钉住无限电台关闭态的封面：不再借用
// 推荐歌单的图片，而是主题强调色在浅色模式落入纸白、深色模式落入近黑，
// 中间保留无限图标。打开后的曲目封面走另一条分支，不在这里断言。
func TestRadioClosedCoverIsThemedGradient(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		dark      bool
		corner    func(float64) bool
		cornerMsg string
	}{
		{
			name: "浅色模式落到白底", mode: "light", dark: true,
			corner:    func(l float64) bool { return l > 0.75 },
			cornerMsg: "浅色模式的渐变外缘应是亮白",
		},
		{
			name: "深色模式落到黑底", mode: "dark", dark: false,
			corner:    func(l float64) bool { return l < 0.05 },
			cornerMsg: "深色模式的渐变外缘应是近黑",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := homeNavApp(t)
			app.Conf.Set("Style.Theme", tc.mode)
			tester := ui.NewTester(app.View, 1280, 900)
			// 桌面外观故意与强制主题相反，确认封面认的是应用设置。
			tester.SetDark(tc.dark)
			tester.Frame()

			r, ok := tester.Find("无限电台封面")
			if !ok {
				t.Fatalf("没有找到关闭态封面；当前帧文本：%v", tester.Texts())
			}
			img := tester.Image()
			corner := img.RGBAAt(int(r.X)+10, int(r.Y)+10)
			if l := relativeLuminance(toUIColor(corner)); !tc.corner(l) {
				t.Errorf("%s：外缘亮度 %.3f，采样色 %v", tc.cornerMsg, l, corner)
			}

			// 图标正中会盖住渐变，取图标右侧的环带：仍应保留强调色的青蓝。
			ring := img.RGBAAt(int(r.X+r.W*0.5)+38, int(r.Y+r.H*0.5))
			if int(ring.B) < int(ring.R)+24 || int(ring.G) < int(ring.R)+24 {
				t.Errorf("封面内圈没有主题强调色，采样色 %v", ring)
			}
		})
	}
}

func toUIColor(c color.RGBA) ui.Color { return ui.RGB(c.R, c.G, c.B) }
