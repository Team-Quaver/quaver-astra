package appui

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

// 本地音乐缓存（弱网优化，数据由内嵌后端加密落盘）。密钥拆成两半：
// 磁盘半种在后端 cache/music/index.quaverlocalcache 的索引头里，
// 前端半种（cacheSeed）只在这里；两者缺一解不开任何缓存数据。
const (
	localCacheKey       = "Playing.LocalCache"
	localCacheSeedKey   = "Playing.LocalCacheSeed"
	localCacheLimitKey  = "Playing.LocalCacheLimitMiB"
	cacheDefaultLimitMB = 256 // 与后端 cacheMinBytes 一致；0=该默认值
)

// localCacheState 是设置页的缓存面板状态。状态查询走本地 HTTP，不能每帧都发，
// 所以按固定间隔刷新；notice 用于清理/改上限后的即时回执。
type localCacheState struct {
	status backend.MusicCacheStatus
	at     time.Time
	notice string
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
	mib := a.localCacheLimitMiB()
	if mib <= 0 {
		return
	}
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
