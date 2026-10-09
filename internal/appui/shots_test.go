package appui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/streaminfo"

	"github.com/egoist/mygo/ui"
)

// TestUIShots 把正在播放页的浮层离屏渲染成 PNG 定妆照，写进
// $QUAVER_SHOTS_DIR。无显示服务的环境（CI/无头容器）没有别的目视验证手段，
// 改完 UI 想看实际效果就靠它：
//
//	QUAVER_SHOTS_DIR=.workbuddy/shots go test ./internal/appui/ -run TestUIShots
//
// 未设环境变量时跳过，不拖慢常规测试。
func TestUIShots(t *testing.T) {
	dir := os.Getenv("QUAVER_SHOTS_DIR")
	if dir == "" {
		t.Skip("设置 QUAVER_SHOTS_DIR 以导出 UI 定妆照")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(tester *ui.Tester, name string) {
		tester.Frame()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tester.Image()); err != nil {
			t.Fatal(err)
		}
	}

	// ⋮ 菜单
	_, _, tester := npMenuApp(t, testQRCXML)
	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	shot(tester, "np-menu.png")

	// 音质胶囊浮窗（流信息已就位的形态）
	app2, _, tester2 := npMenuApp(t, testQRCXML)
	app2.npQInfo.seed("test://stream", &streaminfo.Info{
		Codec: "FLAC", SampleRate: 48000, BitDepth: 24, Bitrate: 1411, Channels: 2,
	})
	if err := tester2.Click("音质"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	shot(tester2, "np-qinfo.png")
}
