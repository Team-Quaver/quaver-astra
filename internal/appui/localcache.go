package appui

import (
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	quaverserver "github.com/team-quaver/typhoeus-go/server"
)

// 本地音乐缓存（弱网优化，数据由内嵌后端加密落盘）。密钥拆成两半：
// 磁盘半种在缓存目录 music/index.quaverlocalcache 的索引头里，
// 前端半种（cacheSeed）只在这里；两者缺一解不开任何缓存数据。
//
// 位置与配置目录分家：默认 Linux $XDG_CACHE_HOME/quaver-music、
// macOS ~/Library/Caches/Quaver Music、Windows %LocalAppData%/Quaver Music/Quaver Cache
// （规则在内嵌后端 server.CacheDir，这里只负责把用户自定义的根目录下传）。
// 旧版落在 <配置目录>/cache/music 的那份，由后端首次打开缓存时整目录搬过来。
const (
	localCacheKey       = "Playing.LocalCache"
	localCacheSeedKey   = "Playing.LocalCacheSeed"
	localCacheLimitKey  = "Playing.LocalCacheLimitMiB"
	localCacheDirKey    = "Playing.LocalCacheDir"
	cacheDefaultLimitMB = 256 // 与后端 cacheMinBytes 一致；0=该默认值
)

// MusicCacheRoot 是启动时交给内嵌后端的缓存根目录：用户在设置页选过的位置，
// 没选过（或路径已不合法）就是空串 = 平台默认。相对路径一律当没设置——
// 后端只接受绝对路径，配置文件被手改坏了也不该让缓存落到进程工作目录里。
func MusicCacheRoot(store *conf.Store) string {
	if store == nil {
		return ""
	}
	root := strings.TrimSpace(store.String(localCacheDirKey, ""))
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	return filepath.Clean(root)
}

// localCacheState 是设置页的缓存面板状态。状态查询走本地 HTTP，不能每帧都发，
// 所以按固定间隔刷新；notice 用于清理/改上限后的即时回执。
type localCacheState struct {
	status backend.MusicCacheStatus
	at     time.Time
	notice string
	// moving 表示正在换位置（搬家可能拷几个 GiB，期间不许再点一次）。
	moving bool
}

// localCacheSeed 返回缓存前端半种；关闭本地缓存（缺省关闭）时返回空串，
// 后端会跳过缓存命中与落盘。
//
// 注意每次都先看开关：cacheSeedOnce 只会解析一次，若只在生成时判断，用户
// 打开后又关掉，内存里的种子仍会被继续送给后端——开关关了却还在写缓存。
func (a *App) localCacheSeed() string {
	if a.Conf == nil || !a.Conf.Bool(localCacheKey, false) {
		return ""
	}
	a.cacheSeedOnce.Do(func() {
		seed, ok := a.Conf.Get(localCacheSeedKey, "").(string)
		if !ok || len(seed) < 32 {
			if seed != "" {
				// 旧数据残缺无法参与解密，作废重建。
				seed = ""
			}
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err == nil {
				seed = base64.RawStdEncoding.EncodeToString(raw)
				a.Conf.Set(localCacheSeedKey, seed)
			}
		}
		a.cacheSeed = seed
	})
	return a.cacheSeed
}

// setLocalCacheEnabled 切换本地缓存开关；开启时确保前端半种已生成。
// 关闭时保留种子与旧缓存（重新打开后仍可解密复用），但 localCacheSeed
// 会立即返回空串，后端不再命中或落盘。
func (a *App) setLocalCacheEnabled(v bool) {
	a.Conf.Set(localCacheKey, v)
	if v {
		a.localCacheSeed()
	}
	a.settings.localCache.at = time.Time{} // 开关影响状态行是否显示，立即重查
	a.invalidate()
}

// localCacheLimitMiB 返回用户设置的缓存上限（MiB）；0 表示使用默认上限。
func (a *App) localCacheLimitMiB() float64 {
	return a.Conf.Float(localCacheLimitKey, 0)
}

// applyLocalCacheLimit 把持久化的上限推给内嵌后端。后端的上限是进程内
// 状态，每次启动都要重推。
func (a *App) applyLocalCacheLimit() {
	if a.API == nil {
		return
	}
	// 0 表示后端默认上限，照样推一次：这条请求会让后端首次打开缓存目录，
	// 顺手清掉上次进程崩溃/强退留下的 .tmp 残片（缓存开关关着也一样清）。
	mib := a.localCacheLimitMiB()
	go func() { _, _ = a.API.SetMusicCacheLimit(int64(mib) << 20) }()
}

func (a *App) localCacheStatusRow(c *ui.Context, t *ui.Theme) {
	enabled := a.Conf.Bool(localCacheKey, false)
	a.prefRow(c, "本地音乐缓存", func() bool { return enabled }, a.setLocalCacheEnabled)

	ui.Text(c, "缓存按账号隔离并加密保存；关闭后不再命中或写入缓存，已有缓存可手动清理。").
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)

	if a.API == nil {
		return
	}
	st := &a.settings.localCache
	if st.at.IsZero() || time.Since(st.at) > 3*time.Second {
		// 失败也要记时间，否则后端短暂不可用会让每帧都发一次请求。
		if v, err := a.API.MusicCacheStatus(); err == nil {
			st.status = v
		}
		st.at = time.Now()
	}
	cur := st.status
	if enabled || cur.Items > 0 {
		limitMB := float64(cur.LimitBytes) / (1 << 20)
		if limitMB <= 0 {
			limitMB = cacheDefaultLimitMB
		}
		ui.Textf(c, "已缓存 %d 首，占用 %.0f MiB（上限 %.0f MiB）。", cur.Items,
			float64(cur.Bytes)/(1<<20), limitMB).
			FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
	if st.notice != "" {
		ui.Text(c, st.notice).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
	if enabled {
		a.localCacheLimitRow(c, t)
	}
	a.localCacheDirRow(c, t)
	if cur.Items > 0 {
		plainBtn(c, "清理缓存", func() {
			if res, err := a.API.ClearMusicCache(); err != nil {
				st.notice = "清理失败：" + err.Error()
			} else {
				res.LimitBytes = cur.LimitBytes
				st.notice = "缓存已清理"
				st.status = res
				st.at = time.Now()
			}
			a.invalidate()
		})
	}
}

// localCacheLimitRow 是缓存上限选择。选项与后端允许的 256 MiB–32 GiB 对齐；
// 「默认」不写具体上限，由后端按 256 MiB 处理。
func (a *App) localCacheLimitRow(c *ui.Context, t *ui.Theme) {
	limits := []struct {
		label string
		mib   float64
	}{
		{"默认（256 MiB）", 0},
		{"512 MiB", 512},
		{"1 GiB", 1024},
		{"2 GiB", 2048},
		{"4 GiB", 4096},
		{"8 GiB", 8192},
		{"16 GiB", 16384},
		{"32 GiB", 32768},
	}
	curMib := a.localCacheLimitMiB()
	label := limits[0].label
	for _, l := range limits {
		if l.mib == curMib {
			label = l.label
			break
		}
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "缓存上限").FontSize(fz(13)).Grow(1)
		sel := ui.Select(c, &label, []string{
			limits[0].label, limits[1].label, limits[2].label, limits[3].label,
			limits[4].label, limits[5].label, limits[6].label, limits[7].label,
		}).Width(220)
		if !sel.Changed() {
			return
		}
		for _, l := range limits {
			if l.label == label {
				a.Conf.Set(localCacheLimitKey, l.mib)
				res, err := a.API.SetMusicCacheLimit(int64(l.mib) << 20)
				if err != nil {
					a.settings.localCache.notice = "设置上限失败：" + err.Error()
				} else {
					res.LimitBytes = int64(l.mib) << 20
					a.settings.localCache.status = res
					a.settings.localCache.notice = ""
				}
				a.settings.localCache.at = time.Now()
				break
			}
		}
		a.invalidate()
	})
}

// localCacheDirRow 是缓存位置：默认平台缓存目录，可换成用户自选的目录。
//
// 路径由主进程（本进程）解析后经 POST /cache/music/dir 下传，后端不接受
// 渲染层来源的路径。搬家在后台跑（可能拷几个 GiB），期间按钮禁用并给出回执。
func (a *App) localCacheDirRow(c *ui.Context, t *ui.Theme) {
	st := &a.settings.localCache
	custom := MusicCacheRoot(a.Conf)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "缓存位置").FontSize(fz(13)).Grow(1)
		if st.moving {
			ui.Text(c, "搬家中…").FontSize(fz(12.5)).TextColor(t.TextMuted)
		} else {
			plainBtn(c, "更改位置…", func() { a.pickLocalCacheDir() })
		}
		if custom != "" && !st.moving {
			plainBtn(c, "恢复默认", func() { a.applyLocalCacheDir("") })
		}
	})
	ui.Text(c, localCacheDirLabel(st.status.Dir, custom)).
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	ui.Text(c, "缓存是可再生数据，默认放在系统缓存目录（Linux XDG_CACHE_HOME、macOS 用户资源库 Caches），不随配置目录备份；换位置时已有缓存会一起搬过去。").
		FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(3)
}

// localCacheDirLabel 是位置行显示的目录。后端状态里的 dir 是实际生效值，
// 优先用它；状态还没取到时按配置推算（自定义根目录，否则平台默认）。
func localCacheDirLabel(live, custom string) string {
	if live != "" {
		if custom == "" {
			return live + "（默认）"
		}
		return live
	}
	root := custom
	if root == "" {
		root = quaverserver.CacheDir()
	}
	return filepath.Join(root, "music")
}

// pickLocalCacheDir 打开系统目录选择器。选择器阻塞到用户关闭，必须在
// 后台 goroutine 里调（主线程要接着画界面）。
func (a *App) pickLocalCacheDir() {
	if a.API == nil || a.settings.localCache.moving {
		return
	}
	start := MusicCacheRoot(a.Conf)
	if start == "" {
		start = quaverserver.CacheDir()
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:             "选择音乐缓存位置",
			DefaultPath:       start,
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil {
			a.update(func() {
				a.settings.localCache.notice = "打开目录选择器失败：" + err.Error()
			})
			return
		}
		if len(paths) == 0 {
			return // 用户取消
		}
		// 面板状态只在主线程改：选择器在后台 goroutine 里返回，直接调
		// applyLocalCacheDir 会与正在画的那一帧抢 moving/notice。
		a.update(func() { a.applyLocalCacheDir(paths[0]) })
	}()
}

// applyLocalCacheDir 换缓存根目录（空串 = 回到平台默认）。
//
// 只在主线程调用（按钮回调，或后台回调经 update 切回来）。
//
// 配置只在后端真的搬完后才落盘：失败时后端仍在旧目录，若这时把新位置写进
// 配置，下次启动就会按一个从未生效的位置去建一份空缓存，旧缓存被晾在原地。
func (a *App) applyLocalCacheDir(root string) {
	if a.API == nil {
		return
	}
	st := &a.settings.localCache
	if st.moving {
		return
	}
	root = strings.TrimSpace(root)
	if root != "" {
		if !filepath.IsAbs(root) {
			st.notice = "缓存位置必须是绝对路径"
			a.invalidate()
			return
		}
		root = filepath.Clean(root)
	}
	if root == MusicCacheRoot(a.Conf) {
		return
	}
	if !localCacheMoveAllowed(a.Conf.Dir(), root) {
		st.notice = "缓存位置不能放在配置目录里：缓存是可再生数据，混进配置会被备份/同步一起搬走"
		a.invalidate()
		return
	}
	st.moving = true
	st.notice = "正在搬家，缓存较多时可能需要一会儿…"
	a.invalidate()
	go func() {
		res, err := a.API.SetMusicCacheDir(root)
		a.update(func() {
			st.moving = false
			if err != nil {
				st.notice = "换位置失败：" + err.Error()
				a.invalidate()
				return
			}
			a.Conf.Set(localCacheDirKey, root)
			if res.LimitBytes == 0 {
				res.LimitBytes = int64(a.localCacheLimitMiB()) << 20
			}
			st.status = res
			st.at = time.Now()
			if root == "" {
				st.notice = "缓存已搬回默认位置"
			} else {
				st.notice = "缓存位置已更新"
			}
			a.invalidate()
		})
	}()
}

// localCacheMoveAllowed 报告目标根目录是否可用。配置目录本身与它的子目录
// 一律拒绝：缓存与配置分家是这次改位置的前提，放回去等于把几个 GiB 的
// 可再生字节重新塞进会被备份/同步的目录，也会让升级搬家来回打转。
func localCacheMoveAllowed(confDir, root string) bool {
	if root == "" || confDir == "" {
		return true
	}
	confDir = filepath.Clean(confDir)
	rel, err := filepath.Rel(confDir, root)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
