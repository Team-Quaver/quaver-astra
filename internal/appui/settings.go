package appui

import (
	"runtime"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"
	mpvaudio "github.com/Team-Quaver/quaver-astra/internal/audio/mpv"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

type settingsState struct {
	tab int

	// 播放后端自检要 dlopen FFmpeg / 连 PipeWire，不能每帧跑；关于页
	// 缓存几秒，重新进入或设备变化时再刷新。
	backendStatus   audio.Status
	backendStatusAt time.Time

	// mpv 定位结果也走同一套节流：ProbeMPV 会碰文件系统，不能每帧跑。
	mpvInfo   mpvaudio.MPVInfo
	mpvInfoAt time.Time

	// 本地音乐缓存面板的状态（占用/上限/回执），见 localcache.go。
	localCache localCacheState

	// 自定义字体输入框的草稿；TextInput 需要跨帧稳定地持有可编辑字符串。
	fontDraft      string
	lyricFontDraft string
	accentDraft    ui.Color
	draftsLoaded   bool
	scroll         ui.ScrollState
	scrollTab      int
}

var themeNames = []struct {
	key  string
	name string
}{{"system", "跟随系统"}, {"light", "浅色"}, {"dark", "深色"}}

var accentModeNames = []struct {
	key  string
	name string
}{
	{"default", "默认青色"},
	{"cover", "封面颜色"},
	{"system", "系统颜色"},
	{"custom", "自定义"},
}

// 设置页四个 tab 的下标。测试与快捷键录制状态判定都按名字取，
// 不要再散一遍魔法数字（多一个 tab 就把「关于」指错页）。
const (
	appearanceTab = iota
	playbackTab
	// hotkeysTab 是「设置 → 快捷键」，见 hotkeys_settings.go。
	hotkeysTab
	aboutTab
)

// mpvStatus 缓存 mpv 定位结果（5 秒），设置页两个 tab 共用。
func (a *App) mpvStatus() mpvaudio.MPVInfo {
	st := &a.settings
	if st.mpvInfoAt.IsZero() || time.Since(st.mpvInfoAt) > 5*time.Second {
		st.mpvInfo = mpvaudio.MPVInfoFor()
		st.mpvInfoAt = time.Now()
	}
	return st.mpvInfo
}

func (a *App) settingsView(c *ui.Context) {
	st := &a.settings
	t := c.Theme()
	if !st.draftsLoaded {
		st.fontDraft = a.Conf.String("Style.FontCustom", "")
		st.lyricFontDraft = a.Conf.String("Style.LyricFontCustom", "")
		st.accentDraft = defaultAccent
		if custom, ok := parseAccentColor(a.Conf.String(accentCustomKey, "")); ok {
			st.accentDraft = custom
		}
		st.draftsLoaded = true
	}

	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Text(c, "设置").FontSize(fz(24)).FontWeight(800)
		ui.Tabs(c, &st.tab, "外观", "播放", "快捷键", "关于")
		if st.tab != st.scrollTab {
			st.scroll = ui.ScrollState{}
			st.scrollTab = st.tab
		}
		ui.Scroll(c).Grow(1).FillWidth().TrackScroll(&st.scroll).Children(func() {
			switch st.tab {
			case appearanceTab:
				a.settingsAppearance(c, t)
			case playbackTab:
				a.settingsPlayback(c, t)
			case hotkeysTab:
				a.hotkeysSettings(c, t)
			default:
				a.settingsAbout(c, t)
			}
		})
	})
}

func (a *App) settingsAppearance(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(16).MaxWidth(560).Children(func() {
		ui.Text(c, "主题").FontSize(fz(14)).FontWeight(700)
		cur := a.Conf.String("Style.Theme", "system")
		ui.Row(c).Gap(8).Children(func() {
			for _, th := range themeNames {
				btn := ui.ButtonBase(c).Padding(8, 14).Radius(8).Border(1, t.Border).Transition(hoverFade)
				btn.Children(func() { ui.Text(c, th.name).FontSize(fz(13)) })
				if cur == th.key {
					btn.Background(t.Accent.Alpha(0.14))
					btn.TextColor(t.Accent)
				} else if btn.Hovered() {
					btn.Background(t.SurfaceHover)
				}
				if btn.Clicked() {
					a.setTheme(c, th.key)
				}
			}
		})

		ui.Text(c, "高亮配色").FontSize(fz(14)).FontWeight(700)
		a.accentSetting(c, t)

		ui.Text(c, "字体").FontSize(fz(14)).FontWeight(700)

		// 字体模式与自定义 family list。system 映射到 Fontconfig 的默认
		// sans-serif，避免 MyGo 的 system-ui 展开导致 CJK fallback 错选日文字体。
		a.fontSettingRow(c, t, "界面字体", "Style.FontMode", "Style.FontCustom", &a.settings.fontDraft)
		a.fontSettingRow(c, t, "歌词字体", "Style.LyricFontMode", "Style.LyricFontCustom", &a.settings.lyricFontDraft)

		ui.Text(c, "自定义字体填写字体 family 名，可用逗号写多个候选，例如「Noto Sans CJK SC, sans-serif」。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

		// 歌词辅助行三档：翻译优先 / 音译优先 / 关闭。翻译与音译共用歌词下方
		// 那一行，互斥显示；本行缺首选那一路时自动回落另一路。
		auxIDs := []string{"off", "translation", "romanization"}
		auxLabels := []string{"关闭", "翻译优先", "音译优先"}
		curAux := a.PL.LyricAuxMode()
		curAuxLabel := auxLabels[0]
		for i, id := range auxIDs {
			if id == curAux {
				curAuxLabel = auxLabels[i]
			}
		}
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, "歌词辅助行").FontSize(fz(12.5)).TextColor(t.TextMuted)
			sel := ui.Select(c, &curAuxLabel, auxLabels).Width(280)
			if sel.Changed() {
				for i, label := range auxLabels {
					if label == curAuxLabel {
						a.PL.SetLyricAuxMode(auxIDs[i])
						break
					}
				}
			}
			ui.Text(c, "翻译优先时缺翻译回落音译；音译优先时缺音译回落翻译。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
		})

		ui.Divider(c)

		ui.Text(c, "关闭窗口行为").FontSize(fz(14)).FontWeight(700)
		closeActions := []struct{ key, label string }{
			{"tray", "关闭窗口"},
			{"quit", "退出应用"},
		}
		closeLabels := []string{closeActions[0].label, closeActions[1].label}
		closeKey := a.Conf.String("Window.CloseAction", "tray")
		closeLabel := closeActions[0].label
		for _, action := range closeActions {
			if action.key == closeKey {
				closeLabel = action.label
				break
			}
		}
		cs := ui.Select(c, &closeLabel, closeLabels).Width(280)
		if cs.Changed() {
			for _, action := range closeActions {
				if action.label == closeLabel {
					a.Conf.Set("Window.CloseAction", action.key)
					break
				}
			}
		}
		ui.Text(c, "「关闭窗口」收进托盘，音乐继续播放；「退出应用」关闭窗口并退出 Quaver Astra。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	})
}

// accentSetting 提供默认青色、封面取色、GTK/系统色与自定义色四种来源。
func (a *App) accentSetting(c *ui.Context, t *ui.Theme) {
	mode := a.accentMode()
	ui.Row(c).Gap(8).Children(func() {
		for _, option := range accentModeNames {
			btn := ui.ButtonBase(c).Padding(8, 12).Radius(8).Border(1, t.Border).Transition(hoverFade)
			btn.Children(func() { ui.Text(c, option.name).FontSize(fz(12.5)) })
			if mode == option.key {
				btn.Background(t.Accent.Alpha(0.14))
				btn.TextColor(t.Accent)
			} else if btn.Hovered() {
				btn.Background(t.SurfaceHover)
			}
			if btn.Clicked() {
				a.Conf.Set(accentModeKey, option.key)
				a.invalidate()
			}
		}
	})

	switch mode {
	case "system":
		system := c.Preferences().Accent
		if system.A == 0 {
			ui.Text(c, "当前系统/GTK 主题未提供高亮色，已回退到默认青色。").
				FontSize(fz(11.5)).TextColor(t.TextMuted)
		} else {
			system.A = 255
			ui.Text(c, "已读取系统/GTK 高亮色 "+accentHex(system)).
				FontSize(fz(11.5)).TextColor(t.TextMuted)
		}
	case "custom":
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "自定义颜色").FontSize(fz(12.5)).TextColor(t.TextMuted)
			well := ui.ColorWell(c, &a.settings.accentDraft).Label("自定义高亮颜色")
			if well.Changed() {
				a.settings.accentDraft.A = 255
				a.Conf.Set(accentCustomKey, accentHex(a.settings.accentDraft))
				a.invalidate()
			}
			ui.Text(c, accentHex(a.settings.accentDraft)).FontSize(fz(11.5)).TextColor(t.TextMuted)
		})
	}
}

// fontSettingRow 是一组字体模式选择；custom 模式展开 family list 输入框。
func (a *App) fontSettingRow(c *ui.Context, t *ui.Theme, title, modeKey, customKey string, draft *string) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, title).FontSize(fz(12.5)).TextColor(t.TextMuted)
		mode := a.Conf.String(modeKey, "system")
		label := "系统"
		options := make([]string, 0, len(fontModeNames))
		for _, option := range fontModeNames {
			options = append(options, option.label)
			if option.key == mode {
				label = option.label
			}
		}
		sel := ui.Select(c, &label, options).Width(280)
		if sel.Changed() {
			for _, option := range fontModeNames {
				if option.label == label {
					a.Conf.Set(modeKey, option.key)
					a.invalidate()
					break
				}
			}
		}
		if a.Conf.String(modeKey, "system") == "custom" {
			in := ui.TextInput(c, draft).Label("自定义字体 family").Placeholder("例如：Noto Sans CJK SC").Width(280)
			if in.Changed() {
				a.Conf.Set(customKey, *draft)
				a.invalidate()
			}
		}
	})
}

func (a *App) settingsPlayback(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(16).MaxWidth(560).Children(func() {
		// 播放后端：默认 Liino 进程内原生播放；MPV 作为兼容性兜底，
		// 切换立即生效（正在播放时从当前进度重新取流）。
		ui.Text(c, "播放后端").FontSize(fz(14)).FontWeight(700)
		a.prefRow(c, "使用 MPV 作为播放后端", func() bool {
			return a.mpvBackendEnabled()
		}, a.setMPVBackend)
		if liinoExperimental() {
			ui.Text(c, "macOS 默认使用 mpv 子进程；Liino 在 macOS 上属于实验性后端，"+
				"关闭开关后会切回 Liino，遇到播放异常可重新打开。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
		} else {
			ui.Text(c, "默认由 Liino 在进程内解码并直连系统音频；打开后改用 mpv 子进程，"+
				"适合系统 FFmpeg/PipeWire 缺失或需要 mpv 缓存与重连能力的场景。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
		}
		if a.mpvBackendEnabled() {
			info := a.mpvStatus()
			if info.OK {
				ui.Text(c, "已检测到 mpv："+info.Path+"（"+info.Detail+"）").
					FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			} else {
				ui.Text(c, info.Detail).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			}
		}

		ui.Divider(c)

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

		a.prefRow(c, "回退到全景声（含 7.1 / 5.1，不推荐）", func() bool { return a.Conf.Bool("Quality.FallbackToQMAtmos", false) },
			func(v bool) { a.Conf.Set("Quality.FallbackToQMAtmos", v) })
		ui.Text(c, "关闭后自动选档与降档都跳过全景声（7.1 / 5.1），不影响臻品音质 2.0；手动选择仍然生效。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

		a.prefRow(c, "上报每日听歌音响力", func() bool {
			return a.Conf.Bool("Playing.ReportListenTime", true)
		}, func(v bool) { a.Conf.Set("Playing.ReportListenTime", v) })
		ui.Text(c, "只统计真实出声时间：整曲累计，切歌/停止/退出时一次提交（分片上报会被记成多条听歌记录）；关闭后立即停止累计。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

		// 本地音乐缓存：播放过的整曲加密落盘（<配置目录>/cache/music/），
		// 弱网重播免回源。密钥拆成磁盘半种 + 前端半种，只拿磁盘解不开。
		a.localCacheStatusRow(c, t)

		ui.Divider(c)

		// 上一首按钮
		ui.Text(c, "上一首按钮").FontSize(fz(14)).FontWeight(700)
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

		ui.Divider(c)

		// 播放期间阻止系统睡眠；屏幕熄灭与锁屏策略不受影响。
		ui.Text(c, "播放音频时睡眠禁止").FontSize(fz(14)).FontWeight(700)
		a.prefRow(c, "播放期间阻止系统睡眠", func() bool {
			return a.Conf.Bool(sleepInhibitKey, true)
		}, func(v bool) {
			a.Conf.Set(sleepInhibitKey, v)
			a.syncSleep()
		})
		ui.Text(c, "暂停或停止即恢复正常睡眠策略；屏幕仍会按系统电源计划熄灭。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	})
}

func (a *App) settingsAbout(c *ui.Context, t *ui.Theme) {
	ui.Column(c).FillWidth().Gap(10).MaxWidth(560).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
			ui.Image(c, appIconSVG).Size(56, 56).Fit(ui.Contain)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "Quaver Astra").FontSize(fz(16)).FontWeight(800)
					ui.Text(c, "（"+a.versionLabel()+"）").FontSize(fz(12.5)).TextColor(t.TextMuted)
				})
				ui.Text(c, "现代，流畅的 Q 音第三方客户端，现已轻装上阵")
			})
		})

		ui.Divider(c)

		// 播放后端：默认 Liino 在进程内解码、直连系统音频；用户在设置 →
		// 播放里打开 MPV 后，这里改为展示 mpv 子进程的定位结果。
		ui.Text(c, "播放后端").FontSize(fz(14)).FontWeight(700)
		if a.mpvBackendEnabled() {
			info := a.mpvStatus()
			label := "mpv"
			if ver := a.PL.EngineVersion(); ver != "" {
				label = ver
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				dot := ui.Box(c).Size(8, 8).Radius(4)
				if info.OK {
					dot.Background(ui.Hex("#2ea043"))
				} else {
					dot.Background(ui.Hex("#e81123"))
				}
				ui.Text(c, label).FontSize(fz(13))
				ui.Text(c, "MPV 子进程模式").FontSize(fz(12)).TextColor(t.TextMuted)
				ui.Spacer(c)
				ui.Text(c, "mpv 解码 · 独立进程").
					FontSize(fz(11.5)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			if info.OK {
				ui.Text(c, info.Path).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(2)
				ui.Text(c, info.Detail).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			} else {
				ui.Text(c, info.Detail).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(4)
			}
			if liinoExperimental() {
				ui.Text(c, "可在设置 → 播放中随时切回 Liino（macOS 上为实验性后端）。").
					FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			} else {
				ui.Text(c, "可在设置 → 播放中随时切回 Liino。").
					FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			}
		} else {
			if a.settings.backendStatusAt.IsZero() || time.Since(a.settings.backendStatusAt) > 5*time.Second {
				a.settings.backendStatus = audio.NewStatus()
				a.settings.backendStatusAt = time.Now()
			}
			st := a.settings.backendStatus
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				dot := ui.Box(c).Size(8, 8).Radius(4)
				if st.OK {
					dot.Background(ui.Hex("#2ea043"))
				} else {
					dot.Background(ui.Hex("#e81123"))
				}
				ui.Text(c, "Liino "+st.Version).FontSize(fz(13))
				ui.Text(c, "Native 原生模式").FontSize(fz(12)).TextColor(t.TextMuted)
				if liinoExperimental() {
					badge := ui.Badge(c, "实验性")
					badge.Background(ui.Hex("#d29922").Alpha(0.16)).TextColor(ui.Hex("#d29922"))
				}
				ui.Spacer(c)
				ui.Text(c, "进程内解码 · 直连系统音频").
					FontSize(fz(11.5)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			if st.OK {
				ui.Text(c, "解码："+st.Decoder).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(2)
				ui.Text(c, "输出："+st.Driver).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(2)
			} else {
				ui.Text(c, st.Detail).FontSize(fz(12)).TextColor(t.TextMuted).MaxLines(4)
				for _, line := range append(st.DecoderAll, st.DriverAll...) {
					ui.Text(c, line).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
				}
			}
			if runtime.GOOS == "linux" {
				ui.Text(c, "Linux 需要系统安装 FFmpeg 运行库与 PipeWire 才能播放。").
					FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
			}
		}

		ui.Divider(c)

		a.settingsUpdate(c, t)

		// 账号相关动作（退出登录、清除凭证）不在这里：它们属于「个人中心」，
		// 从顶栏头像进去。关于页只讲这个应用本身与更新。
	})
}

// setTheme 写回外观设置。
//
// 关键：应用自己的明暗由 App.appearance（读 Style.Theme）决定，不走
// mygo.Theme.IsDark()。根因是 mygo 的 Linux 后端：
//
//	SetSource("light") 只清了 GTK 的 gtk-application-prefer-dark-theme，
//	紧接着 IsDark() 又去问 XDG portal 的 color-scheme——深色桌面上 portal
//	说是深色，于是「强制浅色」被 portal 覆盖，浅色模式完全无法生效
//	（darwin / windows 后端没有这个问题）。
//
// 所以这里对桌面说的 SetSource 只是「尽力而为」（让原生窗口底色/标题栏
// 尽量跟上），真正的明暗一律看 a.appearance / a.dark。
func (a *App) setTheme(c *ui.Context, key string) {
	a.Conf.Set("Style.Theme", key)
	a.ApplyThemeSource()
	// 明暗切换后封面取色结果要按新明暗重新映射。
	if res, ok := a.tint.result(); ok {
		a.tint.set(res, a.appearance(c))
	}
	_ = a.ApplyWindowBackground()
	a.invalidate()
}

// tierLabels 把档位表摊平成「id 列表 + 展示标签列表」，供选择器与
// 档位弹出菜单共用。
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

// prefRow 是一行“标签 + 开关”。
func (a *App) prefRow(c *ui.Context, label string, get func() bool, set func(bool)) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, label).FontSize(fz(13)).Grow(1)
		v := get()
		if ui.Switch(c, &v).Changed() {
			set(v)
		}
	})
}
