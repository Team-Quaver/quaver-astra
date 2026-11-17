package backend

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	quaverserver "github.com/team-quaver/typhoeus-go/server"
)

// TestStartUsesPlatformCacheDir 缓存与配置分家：没给自定义位置时，音乐缓存
// 必须落在平台缓存目录（Linux XDG_CACHE_HOME / macOS ~/Library/Caches），
// 而不是配置目录下的 cache/music。
func TestStartUsesPlatformCacheDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("QUAVER_CONFIG_DIR", cfg)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("QUAVER_CACHE_DIR", "")

	s, err := Start(cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	st, err := NewClient(s.BaseURL()).MusicCacheStatus()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(quaverserver.CacheDir(), "music")
	if st.Dir != want {
		t.Fatalf("缓存目录 = %q，期望平台默认 %q", st.Dir, want)
	}
	if filepath.Dir(filepath.Dir(st.Dir)) == cfg {
		t.Fatalf("缓存仍在配置目录里：%q", st.Dir)
	}
}

// TestStartHonorsCustomCacheRoot 启动时就落在用户自定义的位置（不是先写默认
// 目录再搬一次）。
func TestStartHonorsCustomCacheRoot(t *testing.T) {
	cfg := t.TempDir()
	root := t.TempDir()
	t.Setenv("QUAVER_CONFIG_DIR", cfg)

	s, err := Start(cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	st, err := NewClient(s.BaseURL()).MusicCacheStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Dir != filepath.Join(root, "music") {
		t.Fatalf("缓存目录 = %q，期望 %q", st.Dir, filepath.Join(root, "music"))
	}
}

// TestCustomCacheRootStillRestoresDefault 自定义位置启动后，「恢复默认」必须
// 回到真正的平台缓存目录。若把自定义位置写成 QUAVER_CACHE_DIR，后端会把它
// 当成平台默认，恢复默认等于原地不动——用户永远回不去。
func TestCustomCacheRootStillRestoresDefault(t *testing.T) {
	cfg := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("QUAVER_CONFIG_DIR", cfg)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("QUAVER_CACHE_DIR", "")

	s, err := Start(cfg, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	c := NewClient(s.BaseURL())
	st, err := c.SetMusicCacheDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cacheHome, "quaver-music", "music")
	if st.Dir != want {
		t.Fatalf("恢复默认后 = %q，期望平台缓存目录 %q", st.Dir, want)
	}
}

// TestStartRejectsRelativeCacheRoot 相对路径直接拒绝：缓存落到进程工作目录
// 会随启动方式漂移，用户根本找不到。
func TestStartRejectsRelativeCacheRoot(t *testing.T) {
	t.Setenv("QUAVER_CONFIG_DIR", t.TempDir())
	if _, err := Start(t.TempDir(), "relative/cache", nil); err == nil {
		t.Fatal("相对路径应当被拒绝")
	}
}

// TestStartMigratesLegacyIntoCustomRoot 老用户升级时配置里已经有自定义位置：
// 旧缓存要搬进那个位置，而不是先搬去平台默认目录、再搬第二次。
func TestStartMigratesLegacyIntoCustomRoot(t *testing.T) {
	cfg := t.TempDir()
	custom := t.TempDir()
	t.Setenv("QUAVER_CONFIG_DIR", cfg)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("QUAVER_CACHE_DIR", "")

	legacy := filepath.Join(cfg, "cache", "music")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLegacyMusicCache(t, legacy)

	s, err := Start(cfg, custom, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	st, err := NewClient(s.BaseURL()).MusicCacheStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Dir != filepath.Join(custom, "music") {
		t.Fatalf("缓存目录 = %q，期望自定义位置", st.Dir)
	}
	if st.Items != 1 {
		t.Fatalf("旧缓存没有搬进自定义位置：items = %d", st.Items)
	}
	if _, err := os.Stat(filepath.Join(legacy, "index.quaverlocalcache")); !os.IsNotExist(err) {
		t.Fatalf("旧索引应当已搬走，stat err = %v", err)
	}
}

// TestStartMigratesLegacyMusicCache 从旧版升级：缓存原先落在
// <配置目录>/cache/music，新版首次打开缓存时整目录搬到平台缓存目录，
// 旧目录不再压着字节（用户不需要重新缓冲，也不需要手动清理）。
func TestStartMigratesLegacyMusicCache(t *testing.T) {
	cfg := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("QUAVER_CONFIG_DIR", cfg)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("QUAVER_CACHE_DIR", "")

	legacy := filepath.Join(cfg, "cache", "music")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	// 后端只搬「有索引的一份完整缓存」，这里按磁盘格式造一份旧缓存。
	writeLegacyMusicCache(t, legacy)

	s, err := Start(cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	st, err := NewClient(s.BaseURL()).MusicCacheStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Items != 1 {
		t.Fatalf("搬家后条目数 = %d，期望 1（旧缓存必须跟着搬）", st.Items)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, legacyTrackID+".quaverlocalcache")); err != nil {
		t.Fatalf("分片没有搬到新目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "index.quaverlocalcache")); !os.IsNotExist(err) {
		t.Fatalf("旧索引应当已搬走，stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, legacyTrackID+".quaverlocalcache")); !os.IsNotExist(err) {
		t.Fatalf("旧分片应当已搬走，stat err = %v", err)
	}
}

// legacyTrackID 是旧缓存里那首歌的分片名（随机 32 hex，与后端一致）。
const legacyTrackID = "0123456789abcdef0123456789abcdef"

// writeLegacyMusicCache 按内嵌后端的磁盘格式造一份旧版缓存：
// 索引 = 64 字节头（16..48 是磁盘种子）+ AES-GCM 密文，分片是随机名文件。
// 格式与 server/music_cache.go 的 saveLocked 一致，改一边必须改另一边。
func writeLegacyMusicCache(t *testing.T, dir string) {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	seed := base64.RawStdEncoding.EncodeToString(raw)
	body, err := json.Marshal(map[string]any{
		"v": 1, "s": seed, "b": 5,
		"i": []map[string]any{{
			"id": legacyTrackID, "uid": -1, "tier": "128", "mime": "audio/mpeg",
			"size": 5, "chunks": 1, "duration_ms": 1000, "played": 1,
			"at": "2026-01-01T00:00:00Z",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.New()
	_, _ = sum.Write([]byte("quaver-localcache-v1\x00index\x00"))
	_, _ = sum.Write([]byte(seed))
	_, _ = sum.Write([]byte(seed))
	block, err := aes.NewCipher(sum.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 64)
	if _, err := rand.Read(header); err != nil {
		t.Fatal(err)
	}
	copy(header[16:48], raw)
	out := append(header, gcm.Seal(nonce, nonce, body, nil)...)
	if err := os.WriteFile(filepath.Join(dir, "index.quaverlocalcache"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyTrackID+".quaverlocalcache"), []byte("track"), 0o600); err != nil {
		t.Fatal(err)
	}
}
