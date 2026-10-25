package appui

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/egoist/mygo/ui"
)

// 本地音乐缓存（弱网优化，数据由内嵌后端加密落盘）。密钥拆成两半：
// 磁盘半种在后端 cache/music/index.quaverlocalcache 的索引头里，
// 前端半种（cacheSeed）只在这里；两者缺一解不开任何缓存数据。
const localCacheSeedKey = "Playing.LocalCacheSeed"

// localCacheSeed 返回缓存前端半种；关闭本地缓存（"Playing.LocalCache"=false，
// 缺省关闭）时返回空串，后端会跳过缓存命中与落盘。
func (a *App) localCacheSeed() string {
	a.cacheSeedOnce.Do(func() {
		seed, ok := a.Conf.Get(localCacheSeedKey, "").(string)
		if !ok || len(seed) < 32 {
			if seed != "" {
				// 旧数据残缺无法参与解密，作废重建。
				seed = ""
			}
			if a.Conf.Bool("Playing.LocalCache", false) {
				raw := make([]byte, 32)
				if _, err := rand.Read(raw); err == nil {
					seed = base64.RawStdEncoding.EncodeToString(raw)
					a.Conf.Set(localCacheSeedKey, seed)
				}
			}
		}
		a.cacheSeed = seed
	})
	return a.cacheSeed
}

// setLocalCacheEnabled 切换本地缓存开关；开启时确保前端半种已生成，
// 关闭时保留种子（重新打开后旧缓存仍可解密，避免无谓的重置）。
func (a *App) setLocalCacheEnabled(v bool) {
	a.Conf.Set("Playing.LocalCache", v)
	if v {
		a.localCacheSeed()
	}
	a.invalidate()
}

func (a *App) localCacheStatusRow(c *ui.Context) {
	a.prefRow(c, "本地音乐缓存", func() bool { return a.Conf.Bool("Playing.LocalCache", false) },
		a.setLocalCacheEnabled)
}
