package appui

import (
	"fmt"
	"math"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

// ===== 个人中心 =====
//
// 侧栏头像的落点。以前点头像跳「设置」，于是两件事挤在一个入口里：想看自己
// 账号的人被丢进一堆偏好开关，想退出登录的人反而找不到按钮（它藏在
// 「设置 → 关于」最底下）。这一页只管账号：我是谁、买到什么时候、怎么退出。

// profileState 是「个人中心」页的数据态。
//
// 会员权益不塞进 player.UserInfo：那份状态侧栏每帧都要读，字段只够一个档位
// 标签；到期明细是打开这一页才需要的东西，按其它页面的惯例由页面自己后台取、
// 自己缓存，退出登录时整块作废。
type profileState struct {
	// loaded 表示「这一批取数已经跑完一次」，不管成败。它同时是自动取数的
	// 闸门：失败也置位，否则视图每帧都会觉得「还没取过」而重发请求，
	// 后端一抖就变成自我 DoS。要重来由「刷新」按钮显式触发。
	loaded  bool
	loading bool
	err     string // 整页级失败（连 /user/me 都拿不到）
	me      backend.UserMe
	vip     backend.VIPInfo
	// vipOK 表示 vip 里是刚取到的真数据；失败时 vipErr 给一句话原因，
	// 页面照常渲染身份卡，不把整页划红。
	vipOK   bool
	vipErr  string
	logging bool // 退出登录在途：按钮禁用，避免连点发两次 logout
	// logoutPending 是「logout 已经打成功、但 player 还没把登录态确认下来」的窗口期。
	// 这期间不许自动重取：凭证已经没了，请求只会撞回 401，页面上闪过一条
	// 「登录已过期」——而几百毫秒后登录态就落地，这句话本来就是多余的。
	// 登录态一跳变，New 里注册的 OnNotify 便整块作废本页状态，标志随之清掉。
	logoutPending bool
	// gen 是取数批次号。刷新/退出都会推进它，迟到的旧响应发现自己过期就直接丢弃，
	// 不会把上一位账号的权益写回新状态。
	gen uint64
}

func (a *App) profileView(c *ui.Context) {
	st := &a.profile
	t := c.Theme()
	loggedIn := a.PL.LoggedIn()

	// 首帧取数：未登录不发请求（/user/vip 会 401），测试里 API 可能为 nil，同样跳过。
	if loggedIn && a.API != nil && !st.loaded && !st.loading && !st.logging && !st.logoutPending {
		a.loadProfile(st)
	}

	me := a.PL.Me()
	name, avatar, uid := me.Name, me.Avatar, ""
	tier := me.VipLabel
	if st.loaded && st.me.BaseInfo.Name != "" {
		// 取数失败时 st.me 是空壳，别拿它盖掉侧栏已经有的昵称。
		name, avatar, uid = st.me.BaseInfo.Name, st.me.BaseInfo.Avatar, st.me.BaseInfo.EncryptedUin
	}
	if st.vipOK {
		tier = vipLabel(st.vip)
	}
	if !loggedIn {
		// 退出登录后 player 里的 me 还是上一位账号的缓存，不能继续顶在页面上。
		name, avatar, uid, tier = "", "", "", ""
	}

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 28, 24).Gap(16)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "个人中心").FontSize(fz(24)).FontWeight(800)
			ui.Spacer(c)
			if loggedIn && !st.logoutPending {
				plainBtn(c, "刷新", func() { a.loadProfile(st) })
			}
		})

		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(12.5)).TextColor(t.TextMuted)
		}

		ui.Column(c).FillWidth().Gap(14).MaxWidth(560).Children(func() {
			// —— 身份卡 ——
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
				ui.Avatar(c, name, a.Covers.Get(avatar, a.invalidate)).Size(56, 56)
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, displayName(name, loggedIn)).FontSize(fz(18)).FontWeight(800).
						SingleLine().Ellipsis("…")
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						badge := ui.Badge(c, tierBadgeText(loggedIn, tier))
						if loggedIn && tier != "" {
							badge.Background(t.Accent.Alpha(0.14)).TextColor(t.Accent)
						}
						if uid != "" {
							ui.Text(c, "UID "+uid).FontSize(fz(11.5)).TextColor(t.TextMuted).
								SingleLine().Ellipsis("…")
						}
					})
				})
			})

			ui.Divider(c)
			a.profileVip(c, t, st, loggedIn)
			ui.Divider(c)
			a.profileAccount(c, t, st, loggedIn)
		})
	})
}

// profileVip 是「会员权益」区块：逐档列出各自的到期时间。
//
// 三个档位分开写，不合并成一行「最高档到 X 到期」：超级会员、豪华绿钻、绿钻
// 是彼此独立的权益，到期日不同，合并显示会谎报其中两档的有效期。
func (a *App) profileVip(c *ui.Context, t *ui.Theme, st *profileState, loggedIn bool) {
	ui.Text(c, "会员权益").FontSize(fz(14)).FontWeight(700)
	if !loggedIn {
		ui.Text(c, "登录后查看账号的会员档位与到期时间").FontSize(fz(12.5)).TextColor(t.TextMuted)
		return
	}
	if st.loading && !st.loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "正在获取会员权益…").FontSize(fz(12.5)).TextColor(t.TextMuted)
		})
		return
	}
	if st.vipErr != "" {
		ui.Text(c, st.vipErr).FontSize(fz(12.5)).TextColor(t.TextMuted)
		return
	}
	benefits := st.vip.Benefits()
	if len(benefits) == 0 {
		ui.Text(c, "当前账号没有生效中的会员权益，部分高音质档位可能无法播放").
			FontSize(fz(12.5)).TextColor(t.TextMuted).MaxLines(2)
		return
	}
	now := c.Now()
	for _, b := range benefits {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, b.Label).FontSize(fz(13)).FontWeight(600).Width(90).Shrink(0)
			ui.Text(c, vipExpiryText(b, now)).FontSize(fz(12.5)).TextColor(t.TextMuted).Grow(1)
		})
	}
}

// profileAccount 是登录态与出口：退出登录、清除本机保存的凭证、去登录。
func (a *App) profileAccount(c *ui.Context, t *ui.Theme, st *profileState, loggedIn bool) {
	ui.Text(c, "登录状态").FontSize(fz(14)).FontWeight(700)
	if loggedIn {
		ui.Text(c, "凭证只驻留在本机后端，退出即清空").FontSize(fz(12.5)).TextColor(t.TextMuted)
	}
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		if loggedIn {
			out := ui.Button(c, "退出登录")
			out.Disabled(st.logging)
			if st.logging {
				ui.Spacer(c)
				ui.Spinner(c)
			}
			if out.Clicked() {
				a.profileLogout(st)
			}
		} else {
			primaryBtn(c, "去登录", func() { a.Router.Push("/login") })
		}
		if a.Vault != nil && a.Vault.HasSaved() {
			plainBtn(c, "清除已保存的凭证", func() {
				if err := a.Vault.Clear(); err != nil {
					a.pendingToast = "清除凭证失败：" + err.Error()
					return
				}
				a.pendingToast = "已清除保存的凭证"
			})
		}
	})
}

// loadProfile 后台取个人信息与会员权益。两个请求分开判错：名字拿不到才整页
// 报错，权益拿不到只在权益区块里说明——身份卡该照常显示。
func (a *App) loadProfile(st *profileState) {
	st.gen++
	gen := st.gen
	st.loading = true
	go func() {
		var me backend.UserMe
		var vip backend.VIPInfo
		var meErr, vipErr error
		a.withAPILimit(func() { me, meErr = a.API.UserMe() })
		a.withAPILimit(func() { vip, vipErr = a.API.UserVIP() })
		a.update(func() {
			if gen != st.gen {
				return
			}
			st.loading = false
			st.loaded = true
			if meErr != nil {
				st.err = "个人信息获取失败：" + profileErrText(meErr) + "（可点右上角刷新重试）"
				return
			}
			st.err = ""
			st.me = me
			if vipErr != nil {
				st.vip, st.vipOK = backend.VIPInfo{}, false
				st.vipErr = "会员权益获取失败：" + profileErrText(vipErr)
				return
			}
			st.vip, st.vipOK, st.vipErr = vip, true, ""
		})
	}()
}

// profileLogout 退出登录：清掉本页缓存，并让 player 重新确认登录态（侧栏、
// 我喜欢、歌单都跟着回到未登录形态）。
func (a *App) profileLogout(st *profileState) {
	if st.logging {
		return
	}
	st.logging = true
	// 批次号 +1 并交给回调比对：在途的取数请求回来后会被判为过期丢弃。
	st.gen++
	gen := st.gen
	go func() {
		var err error
		a.withAPILimit(func() { err = a.API.Logout() })
		a.update(func() {
			if gen != st.gen {
				return
			}
			// 整块作废：未登录的页面不许残留上一位账号的档位与到期时间。
			// gen 归零后本回调之后的旧批次一律失效；重新取数交给 OnNotify 在
			// 登录态落地时接管（那时 logoutPending 也一起被清掉）。
			*st = profileState{}
			if err != nil {
				a.pendingToast = "退出登录失败：" + err.Error()
				return
			}
			st.logoutPending = true
			a.pendingToast = "已退出登录"
			a.PL.RefreshUser()
		})
	}()
}

// profileErrText 把后端错误写成人话；401（凭证过期/已退出）单独措辞，
// 让用户知道该重新扫码，而不是以为「会员功能坏了」。
func profileErrText(err error) string {
	if backend.Unauthorized(err) {
		return "登录已过期，请重新登录"
	}
	return err.Error()
}

// displayName 给未登录/昵称为空的账号一个兜底标题，避免整页只剩一个空徽标。
func displayName(name string, loggedIn bool) string {
	if name != "" {
		return name
	}
	if loggedIn {
		return "QQ 音乐用户"
	}
	return "未登录"
}

// tierBadgeText 是档位徽标的文案；未登录不显示档位。
func tierBadgeText(loggedIn bool, tier string) string {
	if !loggedIn {
		return "未登录"
	}
	if tier == "" {
		return "普通用户"
	}
	return tier
}

// vipZone 是权益时间的时区：上游给的是北京时间墙钟，Typhoeus 也按它解析
// （modules.ParseVipTime 固定在 +08:00）。到期日必须在这个时区里读，换台
// 时区的机器上 .Local() 会把「11-04 到期」显示成 11-03，凭空少一天。
var vipZone = time.FixedZone("CST+8", 8*60*60)

// vipExpiryText 把一项权益的到期时间写成一行中文。
//
// 天数向上取整：上游的日期型到期值算到当日 23:59:59（见 backend.VIPInfo.Benefits），
// 截断会把「今天晚些时候到期」说成「已过期半天」。拿不到到期时间时如实写明，
// 不猜「永久」。
func vipExpiryText(b backend.VipBenefit, now time.Time) string {
	if !b.HasEnd {
		return "未提供到期时间"
	}
	d := b.End.Sub(now)
	date := b.End.In(vipZone).Format("2006-01-02")
	if d <= 0 {
		return date + " 已到期"
	}
	if days := int(math.Ceil(d.Hours() / 24)); days > 1 {
		return fmt.Sprintf("%s 到期（还剩 %d 天）", date, days)
	}
	return date + " 到期（今日到期）"
}
