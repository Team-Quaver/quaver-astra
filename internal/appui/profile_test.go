package appui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/vault"

	"github.com/egoist/mygo/ui"
)

// profileStub 是「个人中心」依赖的假后端。
//
// 页面的会员明细走 HTTP（profileView 自己调 a.API），登录态却由 player.Backend
// 的桩件提供 —— 两条路都指向同一个 logged 标志，logout 之后侧栏与页面才会一起
// 回到未登录形态，这正是退出登录后最容易留下残影的地方。
type profileStub struct {
	t       *testing.T
	logged  atomic.Bool
	meData  string
	vipData string
	// vipStatus / meStatus 非 0 时对应端点直接回该状态码，用来演「接口挂了」。
	vipStatus  int
	meStatus   int
	logoutHits atomic.Int32
	// meHits 数「本页自己发起的取数」，用来证明退出登录后不会拿没了凭证的
	// 会话再打一遍接口（那会在页面上闪一条 401）。
	meHits atomic.Int32
}

func (s *profileStub) server() *httptest.Server {
	s.t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data string
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user/me":
			s.meHits.Add(1)
			if s.meStatus != 0 {
				w.WriteHeader(s.meStatus)
				_, _ = w.Write([]byte(`{"code":-1,"msg":"账号接口炸了"}`))
				return
			}
			data = s.meData
		case r.Method == http.MethodGet && r.URL.Path == "/user/vip":
			if s.vipStatus != 0 {
				w.WriteHeader(s.vipStatus)
				_, _ = w.Write([]byte(`{"code":-1,"msg":"会员接口炸了"}`))
				return
			}
			data = s.vipData
		case r.Method == http.MethodPost && r.URL.Path == "/login/logout":
			s.logged.Store(false)
			s.logoutHits.Add(1)
			data = `{}`
		default:
			// 注意：只能在 goroutine 里 Errorf（handler 就是），不许 Fatal。
			s.t.Errorf("未预期的请求 %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":-1,"msg":"unexpected"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"msg":"ok","data":%s}`, data)
	}))
}

// profileBackend 是 player.Backend 的最小桩件，只喂登录态与昵称。
type profileBackend struct{ stub *profileStub }

func (b profileBackend) LoginStatus() (bool, error) { return b.stub.logged.Load(), nil }
func (b profileBackend) UserInfo() (player.UserInfo, error) {
	return player.UserInfo{Name: "Ne0W0r1d新界", VipLabel: "超级会员"}, nil
}
func (b profileBackend) LikedPage(int, int) ([]player.Song, int64, bool, error) {
	return nil, 0, false, nil
}
func (b profileBackend) Playlists() ([]player.PlaylistRef, error) { return nil, nil }
func (b profileBackend) GuessSongs(int) ([]player.Song, error)    { return nil, nil }
func (b profileBackend) Tiers() (*player.TierTable, error)        { return nil, nil }
func (b profileBackend) Resolve(string, string, int64, string, []string, int64) (*player.StreamInfo, error) {
	return nil, nil
}
func (b profileBackend) FetchLyric(string, bool, bool) (string, string, string, error) {
	return "", "", "", nil
}
func (b profileBackend) LikeSong(int64, int64, bool) error { return nil }

// profileApp 造一个停在 /profile、已登录的 App。
func profileApp(t *testing.T, vipData string) (*App, *ui.Tester, *profileStub) {
	t.Helper()
	return profileAppWith(t, &profileStub{
		t:       t,
		meData:  `{"base_info":{"name":"Ne0W0r1d新界","avatar":"","encrypted_uin":"7gGmYq0Q"}}`,
		vipData: vipData,
	})
}

func profileAppWith(t *testing.T, stub *profileStub) (*App, *ui.Tester, *profileStub) {
	t.Helper()
	srv := stub.server()
	t.Cleanup(srv.Close)

	stub.logged.Store(true)
	p := player.New(profileBackend{stub}, nilEngine{}, &nullPrefs{})
	p.RefreshUser()
	for i := 0; i < 200 && p.Me().Name == ""; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	app := &App{
		PL:            p,
		API:           backend.NewClient(srv.URL),
		Conf:          conf.Open(t.TempDir()),
		Vault:         vault.Open(t.TempDir()),
		Router:        ui.NewRouter("/profile"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
	}
	return app, ui.NewTester(app.View, testWinW, testWinH), stub
}

// waitText 等后台取数回填并重绘，直到页面上出现某段文字。
// 页面的会员明细是异步的，一帧断言必然偶发失败。
func waitText(t *testing.T, tester *ui.Tester, substr string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		tester.Frame()
		if tester.HasText(substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等不到 %q；当前帧文本：%v", substr, tester.Texts())
}

// TestSidebarAvatarOpensProfile 头像点击的落点：个人中心，不是设置。
func TestSidebarAvatarOpensProfile(t *testing.T) {
	app := sideLoggedApp(t, false)
	tester := ui.NewTester(app.View, testWinW, testWinH)
	if err := tester.Click("个人中心"); err != nil {
		t.Fatalf("侧栏用户区应当可点（锚点 = 个人中心）：%v", err)
	}
	if got := app.Router.Path(); got != "/profile" {
		t.Errorf("头像应跳 /profile，实得 %q", got)
	}
}

// TestProfileShowsMembershipExpiry 「在这个界面显示会员过期时间」：
// 生效中的档位逐条列出各自的到期日，失效档位不许出现。
func TestProfileShowsMembershipExpiry(t *testing.T) {
	const vip = `{"svip":1,"svip_start":"2025-01-01","svip_end":"2099-01-01",` +
		`"identity":{"vip":1,"vip_end":"2099-03-15","huge_vip":0,"huge_vip_end":"2099-05-05","level":12}}`
	_, tester, _ := profileApp(t, vip)

	waitText(t, tester, "超级会员")
	waitText(t, tester, "2099-01-01 到期（还剩")
	waitText(t, tester, "2099-03-15 到期（还剩")
	if tester.HasText("豪华绿钻") {
		t.Errorf("huge_vip=0 的档位不该出现在权益列表里；当前帧：%v", tester.Texts())
	}
	// UID 来自页面自己拉的 /user/me，不是侧栏那份缓存。
	waitText(t, tester, "UID 7gGmYq0Q")
}

// TestProfileShowsNoMembership 非会员要说明白，而不是留一片空白。
func TestProfileShowsNoMembership(t *testing.T) {
	_, tester, _ := profileApp(t, `{"svip":0,"identity":{"vip":0,"huge_vip":0,"level":8}}`)
	waitText(t, tester, "没有生效中的会员权益")
	waitText(t, tester, "普通用户")
}

// TestProfileVipErrorKeepsIdentity 权益接口挂了不能把整页拖成错误页：
// 身份卡照常，权益区块一句话说明。
func TestProfileVipErrorKeepsIdentity(t *testing.T) {
	app, tester, _ := profileAppWith(t, &profileStub{
		t:         t,
		meData:    `{"base_info":{"name":"Ne0W0r1d新界","encrypted_uin":"7gGmYq0Q"}}`,
		vipStatus: http.StatusInternalServerError,
	})
	waitText(t, tester, "会员权益获取失败")
	waitText(t, tester, "Ne0W0r1d新界")
	if app.profile.err != "" {
		t.Errorf("权益失败不该写进整页错误：%q", app.profile.err)
	}
}

// TestProfileLogout 退出登录：打后端、作废本页缓存、页面回到未登录形态。
func TestProfileLogout(t *testing.T) {
	const vip = `{"svip":1,"svip_end":"2099-01-01","identity":{"vip":0,"huge_vip":0}}`
	app, tester, stub := profileApp(t, vip)
	waitText(t, tester, "2099-01-01 到期")

	// 已保存的凭证与登录态是两回事：退出登录不该顺手删掉它。
	if err := app.Vault.Save([]byte(`{"uin":"1"}`)); err != nil {
		t.Fatal(err)
	}
	meHits := stub.meHits.Load()
	if meHits == 0 {
		t.Fatal("页面应当自己取过一次 /user/me")
	}
	if err := tester.Click("退出登录"); err != nil {
		t.Fatal(err)
	}
	waitText(t, tester, "未登录")
	if got := stub.logoutHits.Load(); got != 1 {
		t.Errorf("logout 应命中 1 次，实得 %d", got)
	}
	time.Sleep(80 * time.Millisecond)
	if got := stub.meHits.Load(); got != meHits {
		t.Errorf("退出登录后不该再拿没了凭证的会话取数：%d → %d", meHits, got)
	}
	if app.profile.loaded || app.profile.vipOK {
		t.Errorf("退出后本页缓存应作废：loaded=%v vipOK=%v", app.profile.loaded, app.profile.vipOK)
	}
	if !app.Vault.HasSaved() {
		t.Error("退出登录不该清除已保存的凭证")
	}

	// 「清除已保存的凭证」是独立入口。
	if err := tester.Click("清除已保存的凭证"); err != nil {
		t.Fatalf("未登录时也要能清掉保存的凭证：%v", err)
	}
	tester.Frame()
	if app.Vault.HasSaved() {
		t.Error("凭证没被清除")
	}
}

// TestProfileLoadFailureDoesNotRetry 取数失败也要「试过就作罢」：loaded 是
// 闸门，失败若不置位，视图会每帧重发一次请求，后端一抖就是自我 DoS。
func TestProfileLoadFailureDoesNotRetry(t *testing.T) {
	_, tester, stub := profileAppWith(t, &profileStub{
		t:        t,
		meData:   `{}`,
		vipData:  `{"svip":0}`,
		meStatus: http.StatusInternalServerError,
	})
	waitText(t, tester, "个人信息获取失败")
	hits := stub.meHits.Load()
	for i := 0; i < 20; i++ {
		tester.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if got := stub.meHits.Load(); got != hits {
		t.Errorf("失败后不该自动重试：%d → %d 次 /user/me", hits, got)
	}
}

// TestVIPExpiryText 到期文案：天数向上取整、今天到期不含糊、缺时间如实说。
func TestVIPExpiryText(t *testing.T) {
	zone := time.FixedZone("CST+8", 8*60*60)
	now := time.Date(2026, 10, 13, 14, 0, 0, 0, zone)
	cases := []struct {
		name string
		b    backend.VipBenefit
		want string
	}{
		{"无到期时间", backend.VipBenefit{Label: "绿钻"}, "未提供到期时间"},
		{
			"当天末尾到期算今日",
			backend.VipBenefit{Label: "绿钻", End: time.Date(2026, 10, 13, 23, 59, 59, 0, zone), HasEnd: true},
			"2026-10-13 到期（今日到期）",
		},
		{
			"跨天向上取整",
			backend.VipBenefit{Label: "绿钻", End: time.Date(2026, 10, 14, 23, 59, 59, 0, zone), HasEnd: true},
			"2026-10-14 到期（还剩 2 天）",
		},
		{
			"已到期",
			backend.VipBenefit{Label: "绿钻", End: time.Date(2026, 10, 12, 23, 59, 59, 0, zone), HasEnd: true},
			"2026-10-12 已到期",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vipExpiryText(tc.b, now); got != tc.want {
				t.Errorf("vipExpiryText = %q，期望 %q", got, tc.want)
			}
		})
	}
}
