package appui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/update"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	updaternative "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// 更新相关的配置键。默认跟随稳定版并开启启动自动检查。
const (
	updateChannelKey   = "Update.Channel"
	updateAutoCheckKey = "Update.AutoCheck"
)

// updateState 是「设置 → 关于」里更新区块的只读投影。检查和安装由
// MyGo updater 插件的独立窗口负责，这里只展示它最近一次检查的结果。
type updateState struct {
	form update.InstallForm
	exe  string
	pkg  update.PackageKind

	ready     bool
	autoCheck bool
	available string
	lastCheck time.Time
	installed string
}

// initUpdate 准备更新客户端并识别当前安装形态；同时清理上次 Windows 热替换
// 留下的 .old 文件。
func (a *App) initUpdate() {
	if a.Upd == nil {
		a.Upd = update.NewClient()
	}
	a.upd.form, a.upd.exe = update.Detect(runtime.GOOS)
	if a.upd.form == update.FormSystemPackage {
		a.upd.pkg = update.DetectPackageKind(a.upd.exe)
	}
	update.CleanupStale(a.upd.exe)
	a.upd.autoCheck = a.Conf.Bool(updateAutoCheckKey, true)
}

// EnableUpdater 注册 MyGo 原生 updater 插件。检查与安装仍走 Astra 自己的
// GitHub Release 客户端：AppImage、便携目录与 Windows zip 原地替换，系统包
// 交给 apt/oma、zypper/dnf 或 pacman；插件负责窗口、提醒和下载进度。
func (a *App) EnableUpdater() {
	mygo.Use(updaternative.New(updater.Options{
		Icon:      AppIconPNG(),
		Language:  "zh-Hans",
		ImageBase: "https://raw.githubusercontent.com/" + update.Repo + "/main/",
		Enabled:   a.updaterEnabled,
		Check:     a.updaterCheck,
	}))
	a.upd.ready = true
	updater.SetAutomaticChecks(a.upd.autoCheck)
	updater.OnChange(a.refreshUpdatePreferences)
	updater.OnInstalled(func() {
		a.upd.installed = updater.AvailableVersion()
		a.invalidate()
	})
	a.refreshUpdatePreferences()
}

func (a *App) refreshUpdatePreferences() {
	if !a.upd.ready {
		return
	}
	a.upd.autoCheck = updater.AutomaticChecks()
	a.upd.available = updater.AvailableVersion()
	a.upd.lastCheck = updater.LastCheck()
	a.invalidate()
}

// selfUpdatable 报告当前实例能否自行升级。系统包必须识别出包格式，才能
// 选出对应产物并交给包管理器安装。
func (s *updateState) selfUpdatable() bool {
	if s.form == update.FormSystemPackage {
		return s.pkg != update.PackageNone
	}
	return s.form.SelfUpdatable()
}

// updaterEnabled 只在能够自行升级当前构建时启用插件。开发构建与无法识别
// 包格式的系统包安装不检查，插件窗口会说明原因。
func (a *App) updaterEnabled() bool {
	version := strings.TrimSpace(a.Version)
	return version != "" && version != "dev" && a.upd.selfUpdatable()
}

// updaterCheck 把 Astra 的 GitHub Release 渠道适配成插件的一次检查。
func (a *App) updaterCheck(ctx context.Context) (*updater.Release, error) {
	if a.Upd == nil {
		return nil, errors.New("更新客户端未初始化")
	}
	rel, err := a.Upd.Latest(ctx, a.updateChannel())
	if err != nil {
		return nil, err
	}
	if !update.Relate(a.Version, rel).Available() {
		return nil, nil
	}
	var asset update.Asset
	var ok bool
	if a.upd.form == update.FormSystemPackage {
		asset, ok = rel.SystemAssetFor(runtime.GOARCH, a.upd.pkg)
	} else {
		asset, ok = rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	}
	if !ok {
		return nil, update.ErrNoAsset
	}
	return &updater.Release{
		Version: rel.LabelVersion(),
		Notes:   rel.Notes,
		Install: func(ctx context.Context, progress func(downloaded, total int64)) error {
			return a.installUpdate(ctx, rel, asset, progress)
		},
	}, nil
}

// installUpdate 下载当前平台的发行产物并交给原有安装器原地替换。
func (a *App) installUpdate(ctx context.Context, rel *update.Release, asset update.Asset, progress func(downloaded, total int64)) error {
	if a.Upd == nil {
		return errors.New("更新客户端未初始化")
	}
	dst := filepath.Join(os.TempDir(), "quaver-astra-update-"+filepath.Base(asset.Name))
	_ = os.Remove(dst)
	defer os.Remove(dst)
	if err := a.Upd.Download(ctx, asset, dst, progress); err != nil {
		return err
	}
	return update.ApplyContext(ctx, a.upd.form, a.upd.exe, dst, asset)
}

func (a *App) updateChannel() update.Channel {
	return update.ParseChannel(a.Conf.String(updateChannelKey, string(update.Stable)))
}

// versionLabel 是「关于」页展示用的版本号；空值回落到 dev。
func (a *App) versionLabel() string {
	if v := strings.TrimSpace(a.Version); v != "" {
		return v
	}
	return "dev"
}

func (a *App) openReleasePage() {
	url := "https://github.com/" + update.Repo + "/releases"
	if err := mygo.Shell.OpenExternal(url); err != nil {
		a.pendingToast = "打开发行页失败：" + err.Error()
	}
}

func (a *App) updateStatusText() string {
	return updateStatusText(a.upd.available, a.upd.lastCheck, a.upd.installed)
}

func updateStatusText(available string, lastCheck time.Time, installed string) string {
	const ts = "2006-01-02 15:04"
	switch {
	case installed != "":
		return "已更新到 " + installed + "，重启后生效"
	case available != "":
		return "发现新版本 " + available + "，点「查看更新」打开更新窗口"
	case lastCheck.IsZero():
		return "尚未检查"
	default:
		return "已是最新版本（" + lastCheck.Format(ts) + "）"
	}
}

// settingsUpdate 是「关于」里的更新区块：渠道、自动检查、状态与入口。
func (a *App) settingsUpdate(c *ui.Context, t *ui.Theme) {
	st := &a.upd
	ui.Text(c, "更新").FontSize(fz(14)).FontWeight(700)

	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "当前版本").FontSize(fz(13)).TextColor(t.TextMuted)
		ui.Text(c, a.Version).FontSize(fz(13)).FontWeight(600)
		ui.Spacer(c)
		ui.Text(c, "安装形态："+st.form.String()).FontSize(fz(11.5)).TextColor(t.TextMuted)
	})

	// 渠道切换：稳定版 / 快照版。切换后立即按新渠道重新检查。
	ch := a.updateChannel()
	ui.Text(c, "更新渠道").FontSize(fz(13)).TextColor(t.TextMuted)
	ui.Row(c).Gap(8).Children(func() {
		for _, opt := range []update.Channel{update.Stable, update.Snapshot} {
			btn := ui.ButtonBase(c).Padding(8, 14).Radius(8).Border(1, t.Border).Transition(hoverFade)
			btn.Children(func() { ui.Text(c, opt.Label()).FontSize(fz(12.5)) })
			if ch == opt {
				btn.Background(t.Accent.Alpha(0.14))
				btn.TextColor(t.Accent)
			} else if btn.Hovered() {
				btn.Background(t.SurfaceHover)
			}
			if btn.Clicked() && ch != opt {
				a.Conf.Set(updateChannelKey, string(opt))
				st.available = ""
				st.lastCheck = time.Time{}
				if st.ready {
					updater.CheckForUpdates()
				}
				a.invalidate()
			}
		}
	})
	ui.Text(c, "稳定版取 GitHub 最新正式 Release；快照版取最近一次 push 构建的预发布版本，可能不稳定。").
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

	a.prefRow(c, "启动时自动检查更新", func() bool {
		return st.autoCheck
	}, func(v bool) {
		a.Conf.Set(updateAutoCheckKey, v)
		st.autoCheck = v
		if st.ready {
			updater.SetAutomaticChecks(v)
		}
		a.invalidate()
	})

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		check := ui.Button(c, "检查更新")
		if check.Clicked() && st.ready {
			updater.CheckForUpdates()
		}
		if st.available != "" {
			btn := ui.PrimaryButton(c, "查看更新")
			if btn.Clicked() && st.ready {
				updater.CheckForUpdates()
			}
		}
		if !st.selfUpdatable() {
			if ui.Button(c, "打开发行页").Clicked() {
				a.openReleasePage()
			}
		}
	})

	ui.Text(c, a.updateStatusText()).FontSize(fz(12)).TextColor(a.updateStatusColor(t)).MaxLines(3)

	if st.available != "" {
		ui.Text(c, "更新窗口会显示 Release Notes；Markdown 与图片会按原格式渲染。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
	if st.form == update.FormSystemPackage && st.pkg != update.PackageNone {
		ui.Text(c, "系统包更新会通过 pkexec 提权，使用 "+st.pkg.ManagerHint()+" 安装下载的更新包。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
	if !st.selfUpdatable() {
		ui.Text(c, "当前为"+st.form.String()+"安装，请通过包管理器或手动下载更新。").
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
}

func (a *App) updateStatusColor(t *ui.Theme) ui.Color {
	st := &a.upd
	switch {
	case st.installed != "":
		return ui.Hex("#2ea043")
	case st.available != "":
		return t.Accent
	default:
		return t.TextMuted
	}
}
