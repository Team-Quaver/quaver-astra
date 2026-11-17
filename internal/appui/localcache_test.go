package appui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
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

// TestMusicCacheRootReadsConfig 启动时下传的缓存根目录：没设过 = 空串
// （平台默认）；相对路径按没设置处理，否则缓存会落到进程工作目录里。
func TestMusicCacheRootReadsConfig(t *testing.T) {
	store := conf.Open(t.TempDir())
	if got := MusicCacheRoot(store); got != "" {
		t.Fatalf("默认应为空串（平台默认位置），实得 %q", got)
	}
	if got := MusicCacheRoot(nil); got != "" {
		t.Fatalf("nil store 应为空串，实得 %q", got)
	}

	store.Set(localCacheDirKey, "relative/cache")
	if got := MusicCacheRoot(store); got != "" {
		t.Fatalf("相对路径必须忽略，实得 %q", got)
	}

	dir := t.TempDir()
	store.Set(localCacheDirKey, "  "+dir+string(filepath.Separator)+".  ")
	if got := MusicCacheRoot(store); got != dir {
		t.Fatalf("自定义位置应去空白并规范化：%q，期望 %q", got, dir)
	}
}

// TestLocalCacheDirLabel 位置行显示实际生效目录；自定义时不再标「默认」。
func TestLocalCacheDirLabel(t *testing.T) {
	live := filepath.Join(t.TempDir(), "music")
	if got := localCacheDirLabel(live, ""); got != live+"（默认）" {
		t.Fatalf("默认位置应标注「默认」，实得 %q", got)
	}
	if got := localCacheDirLabel(live, filepath.Dir(live)); got != live {
		t.Fatalf("自定义位置不应标「默认」，实得 %q", got)
	}
	custom := t.TempDir()
	if got := localCacheDirLabel("", custom); got != filepath.Join(custom, "music") {
		t.Fatalf("状态未就绪时应按配置推算，实得 %q", got)
	}
}

// localCacheDirStub 是只实现缓存三个端点的假后端：记下收到的目录，
// 并按请求回一份状态（搬家结果由后端负责，这里只验主进程这一侧的契约）。
type localCacheDirStub struct {
	mu     sync.Mutex
	gotDir string
	calls  int
	fail   bool
}

func (s *localCacheDirStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cache/music/dir":
			var body struct {
				Dir string `json:"dir"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.gotDir, s.calls = body.Dir, s.calls+1
			fail := s.fail
			s.mu.Unlock()
			if fail {
				w.WriteHeader(422)
				_, _ = w.Write([]byte(`{"code":-1,"msg":"缓存位置不可写"}`))
				return
			}
			dir := body.Dir
			if dir == "" {
				dir = os.TempDir()
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"items":2,"bytes":10,"dir":` +
				quoteTestJSON(filepath.Join(dir, "music")) + `}}`))
		case "/cache/music/status":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"items":0,"bytes":0,"dir":"/tmp/music"}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func quoteTestJSON(v string) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func localCacheApp(t *testing.T, stub *localCacheDirStub) *App {
	t.Helper()
	srv := stub.server(t)
	return localCacheAppAt(t, srv.URL)
}

// localCacheAppAt 造一个只跑设置页的 App。PL 用假后端：设置页会异步取音质表，
// nil api 会 panic，而缓存面板不该依赖真的登录态。
func localCacheAppAt(t *testing.T, base string) *App {
	t.Helper()
	return &App{
		API:           backend.NewClient(base),
		Conf:          conf.Open(t.TempDir()),
		PL:            player.New(loggedBackend{}, nilEngine{}, &nullPrefs{}),
		Router:        ui.NewRouter("/settings"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
}

// waitCacheNotice 等异步搬家回填（applyLocalCacheDir 在后台 goroutine 里发请求）。
func waitCacheNotice(t *testing.T, app *App, substr string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if !app.settings.localCache.moving && strings.Contains(app.settings.localCache.notice, substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等不到回执 %q；当前 notice=%q moving=%v", substr,
		app.settings.localCache.notice, app.settings.localCache.moving)
}

// TestApplyLocalCacheDirPersistsAfterMove 换位置：目录下传给后端，配置只在
// 搬成功后才落盘，状态行改用新位置。
func TestApplyLocalCacheDirPersistsAfterMove(t *testing.T) {
	stub := &localCacheDirStub{}
	app := localCacheApp(t, stub)
	target := t.TempDir()

	app.applyLocalCacheDir(target)
	waitCacheNotice(t, app, "缓存位置已更新")

	stub.mu.Lock()
	got := stub.gotDir
	stub.mu.Unlock()
	if got != target {
		t.Fatalf("下传的目录 = %q，期望 %q", got, target)
	}
	if got := MusicCacheRoot(app.Conf); got != target {
		t.Fatalf("配置未记住新位置：%q", got)
	}
	if app.settings.localCache.status.Dir != filepath.Join(target, "music") {
		t.Fatalf("状态行未改用新位置：%q", app.settings.localCache.status.Dir)
	}

	// 恢复默认：下传空串，配置里的自定义位置随之清掉。
	app.applyLocalCacheDir("")
	waitCacheNotice(t, app, "搬回默认位置")
	stub.mu.Lock()
	got = stub.gotDir
	stub.mu.Unlock()
	if got != "" {
		t.Fatalf("恢复默认应下传空串，实得 %q", got)
	}
	if got := MusicCacheRoot(app.Conf); got != "" {
		t.Fatalf("恢复默认后配置应清空，实得 %q", got)
	}
}

// TestApplyLocalCacheDirKeepsConfigOnFailure 搬家失败（目标只读/盘满）时
// 配置不许改：否则下次启动会按一个从未生效的位置去建空缓存，旧缓存被晾在原地。
func TestApplyLocalCacheDirKeepsConfigOnFailure(t *testing.T) {
	stub := &localCacheDirStub{fail: true}
	app := localCacheApp(t, stub)
	before := t.TempDir()
	app.Conf.Set(localCacheDirKey, before)

	app.applyLocalCacheDir(t.TempDir())
	waitCacheNotice(t, app, "换位置失败")

	if got := MusicCacheRoot(app.Conf); got != before {
		t.Fatalf("失败后配置必须保持原位置：%q，期望 %q", got, before)
	}
}

// TestApplyLocalCacheDirRejectsRelative 相对路径不下传（后端只接受绝对路径）。
func TestApplyLocalCacheDirRejectsRelative(t *testing.T) {
	stub := &localCacheDirStub{}
	app := localCacheApp(t, stub)

	app.applyLocalCacheDir("relative/cache")
	if !strings.Contains(app.settings.localCache.notice, "绝对路径") {
		t.Fatalf("应提示绝对路径，实得 %q", app.settings.localCache.notice)
	}
	stub.mu.Lock()
	calls := stub.calls
	stub.mu.Unlock()
	if calls != 0 {
		t.Fatalf("相对路径不该发给后端，实际发了 %d 次", calls)
	}
}

// TestLocalCacheMoveAllowed 缓存不许搬回配置目录（含子目录）：那正是这次
// 分家要摆脱的位置。配置目录之外、以及它的父目录都放行。
func TestLocalCacheMoveAllowed(t *testing.T) {
	confDir := filepath.Join(t.TempDir(), "quaver-astra")
	cases := []struct {
		root string
		want bool
	}{
		{"", true},
		{confDir, false},
		{filepath.Join(confDir, "cache"), false},
		{filepath.Join(confDir, "cache", "music"), false},
		{filepath.Dir(confDir), true},
		{filepath.Join(filepath.Dir(confDir), "quaver-astra-cache"), true},
	}
	for _, tc := range cases {
		if got := localCacheMoveAllowed(confDir, tc.root); got != tc.want {
			t.Errorf("localCacheMoveAllowed(%q, %q) = %v，期望 %v", confDir, tc.root, got, tc.want)
		}
	}
}

// TestApplyLocalCacheDirRejectsConfigDir 选到配置目录里时不发请求，也不改配置。
func TestApplyLocalCacheDirRejectsConfigDir(t *testing.T) {
	stub := &localCacheDirStub{}
	app := localCacheApp(t, stub)

	app.applyLocalCacheDir(filepath.Join(app.Conf.Dir(), "cache"))
	if !strings.Contains(app.settings.localCache.notice, "配置目录") {
		t.Fatalf("应提示不能放在配置目录，实得 %q", app.settings.localCache.notice)
	}
	stub.mu.Lock()
	calls := stub.calls
	stub.mu.Unlock()
	if calls != 0 {
		t.Fatalf("配置目录内的位置不该发给后端，实际发了 %d 次", calls)
	}
	if got := MusicCacheRoot(app.Conf); got != "" {
		t.Fatalf("被拒绝的位置不该写进配置，实得 %q", got)
	}
}
