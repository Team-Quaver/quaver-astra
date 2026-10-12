package appui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/update"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 更新相关的配置键。默认跟随稳定版并开启启动自动检查。
const (
	updateChannelKey   = "Update.Channel"
	updateAutoCheckKey = "Update.AutoCheck"
)

// updateState 是设置页「关于」里更新区块的状态。写入都发生在 UI 线程
// （update() 回投），后台 goroutine 只负责网络与磁盘。
type updateState struct {
	form update.InstallForm
	exe  string

	checking   bool
	installing bool
	release    *update.Release
	relation   update.Relation
	err        string
	checkedAt  time.Time
	progress   float64 // 0..1；<0 表示长度未知
	progressTx string

	// autoStarted 保证一次运行只做一次启动检查；notifiedTag 避免同一版本
	// 反复弹提示。
	autoStarted bool
	notifiedTag string
	installed   string
}

// initUpdate 准备更新客户端并识别当前安装形态；同时清理上次 Windows 热替换
// 留下的 .old 文件。
func (a *App) initUpdate() {
	if a.Upd == nil {
		a.Upd = update.NewClient()
	}
	a.upd.form, a.upd.exe = update.Detect(runtime.GOOS)
	update.CleanupStale(a.upd.exe)
	a.upd.installed = ""
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

// maybeAutoCheckUpdate 在启动后延迟检查一次（尊重配置）。只在一次运行里触发。
func (a *App) maybeAutoCheckUpdate() {
	if a.upd.autoStarted {
		return
	}
	a.upd.autoStarted = true
	if !a.Conf.Bool(updateAutoCheckKey, true) {
		return
	}
	go func() {
		// 让启动流程先跑完：窗口、后端、登录态都稳了再占用网络。
		time.Sleep(5 * time.Second)
		// checkForUpdate 会在主线程同步写 a.upd，必须回投到 UI 线程执行。
		a.update(func() { a.checkForUpdate(false) })
	}()
}

// checkForUpdate 后台读取当前渠道的最新发行版本并回填状态。
// manual 为 true 时（用户点了按钮）会额外用 toast 反馈结果。
func (a *App) checkForUpdate(manual bool) {
	if a.Upd == nil {
		return
	}
	if a.upd.checking || a.upd.installing {
		return
	}
	a.upd.checking = true
	a.upd.err = ""
	a.invalidate()

	ch := a.updateChannel()
	version := a.Version
	client := a.Upd
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := client.Latest(ctx, ch)
		a.update(func() {
			a.upd.checking = false
			a.upd.checkedAt = time.Now()
			if err != nil {
				a.upd.release = nil
				a.upd.relation = update.RelationUnknown
				a.upd.err = err.Error()
				if manual {
					a.pendingToast = "检查更新失败：" + err.Error()
				}
				a.invalidate()
				return
			}
			a.upd.release = rel
			a.upd.relation = update.Relate(version, rel)
			a.upd.err = ""
			if manual {
				if a.upd.relation.Available() {
					a.pendingToast = "发现新版本 " + rel.LabelVersion()
				} else {
					a.pendingToast = "已是最新版本（" + rel.LabelVersion() + "）"
				}
			} else if a.upd.relation.Available() && a.upd.notifiedTag != rel.Tag {
				a.upd.notifiedTag = rel.Tag
				a.pendingToast = "发现新版本 " + rel.LabelVersion() + "，可在设置 → 关于中更新"
			}
			a.invalidate()
		})
	}()
}

// startUpdate 处理「下载并安装」：能原地升级就下载后热替换，否则打开发行页。
func (a *App) startUpdate() {
	rel := a.upd.release
	if rel == nil || a.upd.installing {
		return
	}
	if !a.upd.form.SelfUpdatable() {
		a.openReleasePage(rel)
		return
	}
	asset, ok := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		a.pendingToast = "该版本没有当前平台的安装包，已打开发行页"
		a.openReleasePage(rel)
		return
	}
	a.upd.installing = true
	a.upd.progress = -1
	a.upd.progressTx = "准备下载…"
	a.upd.err = ""
	a.invalidate()
	go a.downloadAndInstall(rel, asset)
}

// downloadAndInstall 在后台完成下载与替换；所有状态回投到 UI 线程。
func (a *App) downloadAndInstall(rel *update.Release, asset update.Asset) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	dst := filepath.Join(os.TempDir(), "quaver-astra-update-"+filepath.Base(asset.Name))
	_ = os.Remove(dst)
	lastPush := time.Time{}
	err := a.Upd.Download(ctx, asset, dst, func(done, total int64) {
		if !lastPush.IsZero() && time.Since(lastPush) < 120*time.Millisecond && (total <= 0 || done < total) {
			return
		}
		lastPush = time.Now()
		a.update(func() {
			if total > 0 {
				a.upd.progress = float64(done) / float64(total)
				a.upd.progressTx = fmt.Sprintf("下载中 %s / %s", humanBytes(done), humanBytes(total))
			} else {
				a.upd.progress = -1
				a.upd.progressTx = "下载中 " + humanBytes(done)
			}
			a.invalidate()
		})
	})
	if err != nil {
		_ = os.Remove(dst)
		a.update(func() {
			a.upd.installing = false
			a.upd.progress = 0
			a.upd.progressTx = ""
			a.upd.err = "下载失败：" + err.Error()
			a.pendingToast = "更新下载失败：" + err.Error()
			a.invalidate()
		})
		return
	}

	a.update(func() {
		a.upd.progress = -1
		a.upd.progressTx = "正在安装…"
		a.invalidate()
	})
	err = update.Apply(a.upd.form, a.upd.exe, dst, asset)
	_ = os.Remove(dst)
	if err != nil {
		a.update(func() {
			a.upd.installing = false
			a.upd.progress = 0
			a.upd.progressTx = ""
			a.upd.err = "安装失败：" + err.Error()
			a.pendingToast = "更新安装失败：" + err.Error()
			a.invalidate()
		})
		return
	}
	a.update(func() {
		a.upd.installing = false
		a.upd.progress = 0
		a.upd.progressTx = ""
		a.upd.err = ""
		a.upd.installed = rel.LabelVersion()
		a.upd.relation = update.RelationSame
		a.pendingToast = "已更新到 " + rel.LabelVersion() + "，重启 Quaver Astra 后生效"
		a.invalidate()
	})
}

func (a *App) openReleasePage(rel *update.Release) {
	url := "https://github.com/" + update.Repo + "/releases"
	if rel != nil && rel.HTMLURL != "" {
		url = rel.HTMLURL
	}
	if err := mygo.Shell.OpenExternal(url); err != nil {
		a.pendingToast = "打开发行页失败：" + err.Error()
	}
}

// updateStatusText 把关系与状态渲染成一行人类可读文案。
func (a *App) updateStatusText() string {
	st := &a.upd
	switch {
	case st.installed != "":
		return "已更新到 " + st.installed + "，重启后生效"
	case st.installing:
		if st.progressTx != "" {
			return st.progressTx
		}
		return "正在安装…"
	case st.checking:
		return "正在检查更新…"
	case st.err != "":
		return st.err
	case st.release == nil:
		return "尚未检查"
	}
	const ts = "2006-01-02 15:04"
	var when string
	if !st.checkedAt.IsZero() {
		when = "（" + st.checkedAt.Format(ts) + "）"
	}
	switch st.relation {
	case update.RelationSame:
		return "已是最新版本 " + st.release.LabelVersion() + when
	case update.RelationNewer:
		return "发现新版本 " + st.release.LabelVersion()
	case update.RelationSwitch:
		return "可切换到 " + st.release.LabelVersion()
	case update.RelationOlder:
		return "当前渠道最新为 " + st.release.LabelVersion() + "（低于当前版本）"
	default:
		return "最新版本 " + st.release.LabelVersion() + when
	}
}

// settingsUpdate 是「关于」里的更新区块：渠道、开关、检查与安装。
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
			if btn.Clicked() && ch != opt && !st.installing {
				a.Conf.Set(updateChannelKey, string(opt))
				a.upd.release = nil
				a.upd.relation = update.RelationUnknown
				a.upd.err = ""
				a.upd.checkedAt = time.Time{}
				a.checkForUpdate(true)
			}
		}
	})
	ui.Text(c, "稳定版取 GitHub 最新正式 Release；快照版取最近一次 push 构建的预发布版本，可能不稳定。").
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

	a.prefRow(c, "启动时自动检查更新", func() bool {
		return a.Conf.Bool(updateAutoCheckKey, true)
	}, func(v bool) {
		a.Conf.Set(updateAutoCheckKey, v)
	})

	// 状态 + 操作。
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		check := ui.Button(c, "检查更新").Disabled(st.checking || st.installing)
		if check.Clicked() {
			a.checkForUpdate(true)
		}

		rel := st.release
		available := rel != nil && st.relation.Available()
		if available && !st.installing {
			label := "下载并安装"
			if !st.form.SelfUpdatable() {
				label = "打开发行页"
			}
			btn := ui.PrimaryButton(c, label)
			if btn.Clicked() {
				a.startUpdate()
			}
		}
		if rel != nil {
			ui.Link(c, "发行说明", rel.HTMLURL)
		}
		if st.checking || st.installing {
			ui.Spinner(c)
		}
	})

	ui.Text(c, a.updateStatusText()).FontSize(fz(12)).TextColor(a.updateStatusColor(t)).MaxLines(3)

	if st.installing && st.progressTx != "" {
		ui.Progress(c, st.progress).FillWidth()
	}

	if rel := st.release; rel != nil && st.relation.Available() && !st.installing {
		ui.Text(c, rel.LabelVersion()+" · "+rel.Channel.Label()+" · 发布于 "+rel.PublishedAt.Local().Format("2006-01-02 15:04")).
			FontSize(fz(11.5)).TextColor(t.TextMuted)
		if !st.form.SelfUpdatable() {
			ui.Text(c, "当前为"+st.form.String()+"安装，请通过包管理器或手动下载更新。").
				FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
		}
	}
}

func (a *App) updateStatusColor(t *ui.Theme) ui.Color {
	st := &a.upd
	switch {
	case st.err != "":
		return ui.Hex("#e81123")
	case st.installed != "":
		return ui.Hex("#2ea043")
	case st.release != nil && st.relation.Available():
		return t.Accent
	default:
		return t.TextMuted
	}
}

// humanBytes 把字节数格式化成便于阅读的大小。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, u := range units {
		value /= unit
		if value < unit {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0") + " " + u
		}
	}
	return fmt.Sprintf("%.1f PiB", value/unit)
}
