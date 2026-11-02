package appui

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
)

// TestLocalCacheSeedRespectsToggle 锁住「关掉缓存后不能再落盘」：
// 种子只解析一次，若 getter 不看开关，关闭后仍会把内存里的种子送给后端。
func TestLocalCacheSeedRespectsToggle(t *testing.T) {
	app := &App{Conf: conf.Open(t.TempDir())}

	// 缺省关闭：不生成种子，后端也拿不到缓存凭证。
	if seed := app.localCacheSeed(); seed != "" {
		t.Fatalf("缓存关闭时种子应为空，实际 %q", seed)
	}

	app.setLocalCacheEnabled(true)
	seed := app.localCacheSeed()
	if len(seed) < 32 {
		t.Fatalf("开启后应生成种子，实际 %q", seed)
	}

	app.setLocalCacheEnabled(false)
	if got := app.localCacheSeed(); got != "" {
		t.Fatalf("关闭后种子应为空（否则后端继续落盘），实际 %q", got)
	}

	// 重新打开沿用同一个种子，旧缓存仍可解密复用。
	app.setLocalCacheEnabled(true)
	if got := app.localCacheSeed(); got != seed {
		t.Fatalf("重新开启后应复用旧种子：want %q, got %q", seed, got)
	}
}
