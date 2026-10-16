package appui

import (
	"errors"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/streaminfo"

	"github.com/egoist/mygo/ui"
)

// TestUIShots 把正在播放页与浮层离屏渲染成 PNG 定妆照，写进
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

	// 设置 - 关于（产品图标在介绍文案左侧）
	about, aboutTester := sideTestApp(t, false)
	about.settings.tab = aboutTab
	shot(aboutTester, "settings-about.png")

	// 个人中心（侧栏头像的落点：档位、各自的到期时间、退出登录）
	_, profTester, _ := profileApp(t, `{"svip":1,"svip_start":"2024-06-01","svip_end":"2027-01-01",`+
		`"identity":{"vip":1,"vip_end":"2026-10-24","huge_vip":0,"level":12}}`)
	waitText(t, profTester, "到期（还剩")
	shot(profTester, "profile.png")

	// 正在播放页（歌词跟随态）
	base, baseTester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	base.PL.SeekTo(2.0 / 269)
	time.Sleep(450 * time.Millisecond)
	shot(baseTester, "np-lyrics.png")

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

	// 正在播放页上的播放队列浮窗（右侧悬着、盖在整页之上）
	qapp, qtester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	qapp.PL.EnqueueMany([]player.Song{
		{Mid: "m2", Title: "七里香", Artists: "周杰伦", AlbumPmid: "001Qu4I30VtaFH", Interval: 299},
		{Mid: "m3", Title: "夜曲", Artists: "周杰伦", AlbumPmid: "001Qu4I30VtaFH", Interval: 227},
		{Mid: "m4", Title: "搁浅", Artists: "周杰伦", AlbumPmid: "001Qu4I30VtaFH", Interval: 261},
		{Mid: "m5", Title: "以父之名", Artists: "周杰伦", AlbumPmid: "001Qu4I30VtaFH", Interval: 342},
	})
	qapp.queueOpen = true
	time.Sleep(450 * time.Millisecond)
	shot(qtester, "np-queue.png")

	// 侧栏「我创建的歌单」：行首画歌单封面（取不到封面的那首退回♪）
	cov := sideCoverApp(t, false)
	shot(ui.NewTester(cov.View, testWinW, testWinH), "sidebar-covers.png")

	// 收起态：封面仍要以侧栏中线居中
	covSmall := sideCoverApp(t, true)
	shot(ui.NewTester(covSmall.View, testWinW, testWinH), "sidebar-covers-collapsed.png")

	// 设置 - 快捷键：一格处于录制态，绑定态与录制态同框
	hk, _, _ := hotkeyApp(t)
	hk.settings.tab = hotkeysTab
	hk.syncHotkeys()
	hkTester := ui.NewTester(hk.View, 1280, 900)
	hkTester.Frame()
	time.Sleep(500 * time.Millisecond)
	hk.hotkeys.record = &hotkeyTarget{scope: scopeLocal, action: hkNext}
	shot(hkTester, "settings-hotkeys.png")

	// 设置 - 快捷键（问题态）：同键冲突 + 注册被拒，看提示会不会把表格挤歪
	hk2, reg2, _ := hotkeyApp(t)
	hk2.settings.tab = hotkeysTab
	hk2.Conf.Set(hotkeyConfKey(scopeGlobal, "PlayPause"), "Ctrl+Alt+P")
	hk2.Conf.Set(hotkeyConfKey(scopeLocal, "PlayPause"), "Ctrl+Alt+P")
	reg2.fail["Ctrl+Alt+P"] = errors.New("mygo: Ctrl+Alt+P is already a global shortcut of another app")
	hk2.syncHotkeys()
	hk2Tester := ui.NewTester(hk2.View, 1280, 900)
	hk2Tester.Frame()
	time.Sleep(500 * time.Millisecond)
	shot(hk2Tester, "settings-hotkeys-notes.png")
}
