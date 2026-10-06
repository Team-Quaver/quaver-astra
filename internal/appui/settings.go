package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type settingsState struct {
	tab int
}

var themeNames = []struct {
	key  string
	name string
}{{"system", "跟随系统"}, {"light", "浅色"}, {"dark", "深色"}}

func (a *App) settingsView(c *ui.Context) {
	st := &a.settings
	t := c.Theme()

	ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14).Children(func() {
		ui.Text(c, "设置").FontSize(24).FontWeight(800)
		ui.Tabs(c, &st.tab, "外观", "播放", "关于")
		switch st.tab {
		case 0:
			a.settingsAppearance(c, t)
		case 1:
			a.settingsPlayback(c, t)
		default:
			a.settingsAbout(c, t)
		}
	})
}

func (a *App) settingsAppearance(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(16).MaxWidth(560).Children(func() {
		ui.Text(c, "主题").FontSize(14).FontWeight(700)
		cur := a.Conf.String("Style.Theme", "system")
		ui.Row(c).Gap(8).Children(func() {
			for _, th := range themeNames {
				btn := ui.ButtonBase(c).Padding(8, 14).Radius(8).Border(1, t.Border)
				btn.Children(func() { ui.Text(c, th.name).FontSize(13) })
				if cur == th.key {
					btn.Background(t.Accent.Alpha(0.14))
					btn.TextColor(t.Accent)
				} else if btn.Hovered() {
					btn.Background(t.SurfaceHover)
				}
				if btn.Clicked() {
					a.setTheme(th.key)
				}
			}
		})

		a.prefRow(c, "显示歌词翻译", func() bool { return a.PL.ShowTranslation() }, func(v bool) { a.PL.SetShowTranslation(v) })

		ui.Divider(c)

		ui.Text(c, "侧栏").FontSize(14).FontWeight(700)
		a.prefRow(c, "收起侧栏", func() bool { return a.sbCollapsed }, func(v bool) {
			a.sbCollapsed = v
			a.Conf.Set("Window.SidebarCollapsed", v)
		})
	})
}

func (a *App) settingsPlayback(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(16).MaxWidth(560).Children(func() {
		// 默认音质：id 与展示标签平行数组
		ids := []string{"auto"}
		labels := []string{"自动（最高可播）"}
		if tt := a.PL.TierTable(); tt != nil {
			for _, tier := range tt.Tiers {
				ids = append(ids, tier.ID)
				label := tier.ID + " " + tier.Label
				if tier.Locked {
					label += " 🔒"
				}
				labels = append(labels, label)
			}
		} else {
			ids = append(ids, "flac", "320", "128")
			labels = append(labels, "flac FLAC 无损", "320 320K MP3", "128 128K MP3")
		}
		cur := a.Conf.String("Quality.DefaultQuality", "auto")
		curLabel := labels[0]
		for i, id := range ids {
			if id == cur {
				curLabel = labels[i]
			}
		}
		sel := ui.Select(c, &curLabel, labels).Width(280)
		if sel.Changed() {
			for i, l := range labels {
				if l == curLabel {
					a.Conf.Set("Quality.DefaultQuality", ids[i])
					break
				}
			}
		}

		a.prefRow(c, "回退到全景声（不推荐）", func() bool { return a.Conf.Bool("Quality.FallbackToQMAtmos", false) },
			func(v bool) { a.Conf.Set("Quality.FallbackToQMAtmos", v) })

		ui.Divider(c)

		// 上一首按钮
		ui.Text(c, "上一首按钮").FontSize(14).FontWeight(700)
		prevKeys := []string{"replay", "previous"}
		prevLabels := []string{"重放当前曲", "跳到上一首"}
		curPrev := a.Conf.String("Playing.PrevReplay", "replay")
		curPrevLabel := prevLabels[0]
		for i, k := range prevKeys {
			if k == curPrev {
				curPrevLabel = prevLabels[i]
			}
		}
		ps := ui.Select(c, &curPrevLabel, prevLabels).Width(280)
		if ps.Changed() {
			for i, l := range prevLabels {
				if l == curPrevLabel {
					a.Conf.Set("Playing.PrevReplay", prevKeys[i])
					break
				}
			}
		}

		// 音量
		ui.Text(c, "音量").FontSize(14).FontWeight(700)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			vol := a.PL.Volume()
			sl := ui.Slider(c, &vol, 0, 1).Grow(1)
			if sl.Changed() {
				a.PL.SetVolume(vol)
			}
			ui.Textf(c, "%d%%", int(vol*100)).FontSize(12.5).TextColor(t.TextMuted).Width(40)
		})
	})
}

func (a *App) settingsAbout(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(10).MaxWidth(560).Children(func() {
		ui.Text(c, "Quaver Astra").FontSize(16).FontWeight(800)
		ui.Text(c, "现代，流畅的 Q 音第三方客户端，现已轻装上阵")
		// ui.Text(c, "AGPL-3.0 License").FontSize(12.5).TextColor(t.TextMuted)
		ui.Divider(c)
		// ui.Textf(c, "登录状态：%s", logged).FontSize(13)
		// ui.Textf(c, "凭证交接：%s（external 模式，登录/自动刷新/登出实时交接）", mode).FontSize(13)
		// ui.Textf(c, "本地凭证：%s", saved).FontSize(13)
		// ui.Textf(c, "后端：%s", a.API.Base).FontSize(13)
		ui.Row(c).Gap(8).Children(func() {
			if a.PL.LoggedIn() {
				if ui.Button(c, "退出登录").Clicked() {
					go func() {
						_ = a.API.Logout()
						a.update(func() { a.PL.RefreshUser() })
					}()
				}
			}
			if a.Vault != nil && a.Vault.HasSaved() {
				if ui.Button(c, "清除已保存的凭证").Clicked() {
					_ = a.Vault.Clear()
				}
			}
		})
	})
}

func (a *App) setTheme(key string) {
	a.Conf.Set("Style.Theme", key)
	switch key {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
	// 明暗切换后取色结果需要重新映射
	if res, ok := a.tint.result(); ok {
		a.tint.set(res, mygo.Theme.IsDark())
	}
}

// prefRow 是一行“标签 + 开关”。
func (a *App) prefRow(c *ui.Context, label string, get func() bool, set func(bool)) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, label).FontSize(13).Grow(1)
		v := get()
		if ui.Switch(c, &v).Changed() {
			set(v)
		}
	})
}

// qualityTier 保留给质量菜单复用（playerbar 的档位弹出）。
func tierLabels(tt *player.TierTable) ([]string, []string) {
	ids := []string{"auto"}
	labels := []string{"自动（最高可播）"}
	if tt != nil {
		for _, tier := range tt.Tiers {
			ids = append(ids, tier.ID)
			label := tier.ID + " " + tier.Label
			if tier.Locked {
				label += " 🔒"
			}
			labels = append(labels, label)
		}
	}
	return ids, labels
}
