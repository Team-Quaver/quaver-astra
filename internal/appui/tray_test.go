package appui

import (
	"image/png"
	"strings"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
)

func TestTrayTitleLine(t *testing.T) {
	cases := []struct {
		name string
		song player.Song
		want string
	}{
		{"无曲目", player.Song{}, trayIdleLabel},
		{"只有空标题", player.Song{Name: "  ", Title: "\n"}, trayIdleLabel},
		{"单歌手", player.Song{Name: "夜曲", Singers: []player.Singer{{Name: "周杰伦"}}}, "夜曲 - 周杰伦"},
		{"多歌手", player.Song{
			Title:    "以父之名",
			Singers:  []player.Singer{{Name: "周杰伦"}, {Name: "费玉清"}},
			Subtitle: "《无间道》插曲",
		}, "以父之名 - 周杰伦 / 费玉清"},
		{"无歌手只留歌名", player.Song{Name: "纯音乐"}, "纯音乐"},
		{"Artists 兜底", player.Song{Name: "夜曲", Artists: "周杰伦/方文山"}, "夜曲 - 周杰伦 / 方文山"},
		{"压成一行", player.Song{Name: "夜\n曲  ", Singers: []player.Singer{{Name: " 周  杰伦 "}}}, "夜 曲 - 周 杰伦"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trayTitleLine(tc.song); got != tc.want {
				t.Fatalf("trayTitleLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrayTitleClampsByDisplayWidth(t *testing.T) {
	long := trayTitleLine(player.Song{
		Name:    "夜的第七章（Live at 台北小巨蛋 Concert Version）",
		Singers: []player.Singer{{Name: "周杰伦"}, {Name: "温岚"}},
	})
	if displayCols(long) > trayTitleMaxCols || !strings.HasSuffix(long, trayTitleEllipsis) {
		t.Fatalf("长标题未按列宽截断: %d cols %q", displayCols(long), long)
	}
	if !strings.HasPrefix(long, "夜的第七章") {
		t.Fatalf("截断后应保留曲名开头: %q", long)
	}
	if got := clampDisplayCols("夜曲 - 甲乙丙丁", 6); strings.Contains(got, "-") || !strings.HasSuffix(got, trayTitleEllipsis) {
		t.Fatalf("悬空分隔符未清理: %q", got)
	}
	if got := clampDisplayCols(strings.Repeat("🎵", 30), 12); displayCols(got) > 12 {
		t.Fatalf("emoji 列宽计算错误: %q (%d)", got, displayCols(got))
	}
}

func TestTrayIconIsSmallPNG(t *testing.T) {
	for _, dark := range []bool{false, true} {
		data, err := trayIcon(dark)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != 32 || cfg.Height != 32 {
			t.Fatalf("托盘图标 = %dx%d, want 32x32", cfg.Width, cfg.Height)
		}
	}
}

func TestTrayMenuMatchesQuaverAstra(t *testing.T) {
	p := player.New(nil, nilEngine{}, &nullPrefs{})
	a := &App{PL: p}
	root, items := a.buildTrayMenu()
	got := root.Items()
	if len(got) != 9 {
		t.Fatalf("托盘根菜单 = %d 项, want 9", len(got))
	}
	wantLabels := []string{
		trayIdleLabel, "上一曲", "播放", "下一曲", "循环模式",
		"", trayWindowToggleLabel, "", "退出",
	}
	for i, want := range wantLabels {
		if got[i].Label != want {
			t.Errorf("第 %d 项 = %q, want %q", i+1, got[i].Label, want)
		}
	}
	if !items.track.Disabled {
		t.Error("曲目行应为纯展示项")
	}
	loop := got[4].Submenu
	if len(loop) != 5 {
		t.Fatalf("循环模式 = %d 项, want 5", len(loop))
	}
	for i, want := range []struct {
		label string
		typ   mygo.MenuItemType
	}{
		{"顺序播放", mygo.MenuItemRadio},
		{"列表循环", mygo.MenuItemRadio},
		{"单曲循环", mygo.MenuItemRadio},
		{"", mygo.MenuItemSeparator},
		{"随机播放", mygo.MenuItemCheckbox},
	} {
		if loop[i].Label != want.label || loop[i].Type != want.typ {
			t.Errorf("循环第 %d 项 = %q/%s, want %q/%s", i+1, loop[i].Label, loop[i].Type, want.label, want.typ)
		}
	}
}
