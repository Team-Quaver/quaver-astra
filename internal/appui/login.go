package appui

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

type loginState struct {
	channel  int // 0 mobile 1 qq 2 wx
	qr       *backend.QRCode
	status   string
	expired  bool
	auto     bool
	gen      uint64 // 生成批次号，防止旧轮询写新状态
	pollStop bool
	// 快速登录（本机 QQ 客户端 SSO）：available=false 是「没检测到」而非错误。
	quick        *backend.QQQuickAccount
	quickChecked bool
	quickTrying  bool
}

var loginChannels = []string{"mobile", "qq", "wx"}

func (a *App) loginView(c *ui.Context) {
	st := &a.login
	t := c.Theme()
	if !st.auto {
		st.auto = true
		a.detectQQQuick(st)
		a.genQR(st)
	}
	// 入场走路由页口径（motion.go）：整页淡入上浮，块间 40ms 错峰。
	a.pageEnter(c, ui.Column(c).Fill().Center().Gap(14)).Children(func() {
		head := ui.Column(c).FillWidth().Center().Gap(3)
		head.Children(func() {
			ui.Text(c, "登录 QQ 音乐").FontSize(fz(22)).FontWeight(800)
			ui.Text(c, "扫码后凭证只驻留在本机后端内存中").FontSize(fz(12.5)).TextColor(t.TextMuted)
		})
		a.enterBlock(c, 0, head)

		mid := ui.Column(c).FillWidth().Center().Gap(14)
		mid.Children(func() {
			seg := ui.Row(c).Gap(6).Padding(4).Radius(10).Background(t.SurfaceHover)
			seg.Children(func() {
				for i, ch := range []string{"手机", "QQ", "微信"} {
					idx := i
					btn := ui.ButtonBase(c).Padding(6, 16).Radius(8)
					btn.Children(func() {
						ui.Text(c, ch).FontSize(fz(13))
					})
					if idx == st.channel {
						btn.Background(t.Surface).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.1))
					}
					if btn.Clicked() {
						if st.channel != idx {
							st.channel = idx
							st.qr = nil
							st.expired = false
							st.gen++
						}
					}
				}
			})

			a.quickLoginCard(c, st)
		})
		a.enterBlock(c, 1, mid)

		tail := ui.Column(c).FillWidth().Center().Gap(10)
		tail.Children(func() {
			a.loginQRBox(c, st)
			if st.status != "" {
				ui.Text(c, st.status).FontSize(fz(12.5)).TextColor(t.TextMuted)
			}
		})
		a.enterBlock(c, 2, tail)
	})
}

// detectQQQuick 后台探测本机 QQ 客户端；失败或不可用都静默降级二维码。
func (a *App) detectQQQuick(st *loginState) {
	go func() {
		var acc backend.QQQuickAvailability
		var err error
		a.withAPILimit(func() { acc, err = a.API.LoginQQQuickDetect() })
		a.update(func() {
			if st.quickChecked {
				return
			}
			st.quickChecked = true
			if err == nil && acc.Available {
				st.quick = &backend.QQQuickAccount{UIN: acc.UIN, Nickname: acc.Nickname, Avatar: acc.Avatar}
			}
		})
	}()
}

// quickLoginCard 登录页顶部的「一键登录」入口：展示本机 QQ 账号，
// 点击即用 clientkey 兑换 QQ 音乐凭证，全程不打开登录页。
func (a *App) quickLoginCard(c *ui.Context, st *loginState) {
	t := c.Theme()
	if !st.quickChecked {
		return
	}
	if st.quick == nil {
		return
	}
	// 昵称可空（后端只保证 available=false 才不算错误），空值回退通用名；
	// 头像走 ui.Avatar，首字符兜底按字符取，不踩中文昵称的字节切片。
	name := st.quick.Nickname
	if name == "" {
		name = "QQ 用户"
	}
	// 身份卡对齐主项目 .quick-card：整卡可点，居中栅格，64px 圆头像。
	card := ui.ButtonBase(c).FillWidth().MaxWidth(460).AlignItems(ui.Center).Gap(14).
		Padding(14, 16).Radius(12).Transition(hoverFade)
	card.Children(func() {
		ui.Avatar(c, name, a.Covers.Get(st.quick.Avatar, a.invalidate)).Size(64, 64)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, name).FontSize(fz(14)).FontWeight(700).SingleLine().Ellipsis("…")
			uin := "本机已登录 QQ"
			if st.quick.UIN != 0 {
				uin = fmt.Sprintf("QQ 号 %d", st.quick.UIN)
			}
			ui.Text(c, uin).FontSize(fz(12.5)).TextColor(t.TextMuted).SingleLine()
		})
		if st.quickTrying {
			ui.Spinner(c)
		}
	})
	if card.Hovered() {
		card.Background(t.SurfaceHover)
	}
	if card.Clicked() {
		a.startQuickLogin(st)
	}
}

// startQuickLogin 用本机 clientkey 换 QQ 音乐凭证；clientkey 是一次性短寿命
// 凭据，失败直接提示重试，不做服务端缓存。
func (a *App) startQuickLogin(st *loginState) {
	if st.quickTrying {
		return
	}
	st.quickTrying = true
	uin := st.quick.UIN
	gen := st.gen
	go func() {
		var err error
		a.withAPILimit(func() { err = a.API.LoginQQQuick(uin) })
		a.update(func() {
			st.quickTrying = false
			if gen != st.gen {
				return
			}
			if err != nil {
				st.status = "快速登录失败：" + err.Error()
				return
			}
			st.status = "登录成功"
			st.pollStop = true
			a.PL.RefreshUser()
			if a.Router.CanGoBack() {
				a.Router.Back()
			} else {
				a.Router.Push("/")
			}
		})
	}()
}

func (a *App) loginQRBox(c *ui.Context, st *loginState) {
	t := c.Theme()
	switch {
	case st.qr == nil && !st.expired:
		ui.Box(c).Size(220, 220).Radius(12).Background(t.SurfaceHover).Border(1, t.Border).Center().Children(func() {
			if ui.Button(c, "生成二维码").Clicked() {
				a.genQR(st)
			}
		})
	case st.expired:
		ui.Box(c).Size(220, 220).Radius(12).Background(t.SurfaceHover).Border(1, t.Border).Center().Children(func() {
			ui.Column(c).Center().Gap(8).Children(func() {
				ui.Text(c, "二维码已过期").FontSize(fz(13)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				if ui.Button(c, "重新生成").Clicked() {
					st.expired = false
					a.genQR(st)
				}
			})
		})
	default:
		box := ui.Box(c).Size(220, 220).Radius(12).Border(1, t.Border).Clip().Background(t.Surface)
		bmp := a.loginBmp(st)
		box.Children(func() {
			if bmp != nil {
				ui.Image(c, bmp).Fill().Fit(ui.Contain).Padding(8)
			} else {
				ui.Box(c).Fill().Center().Children(func() { ui.Spinner(c) })
			}
		})
	}
}

func (a *App) loginBmp(st *loginState) *ui.Bitmap {
	if st.qr == nil || len(st.qr.Data) == 0 {
		return nil
	}
	// genQR 已把解码后的位图写进 Covers 缓存
	return a.Covers.Get("login-qr-"+st.qr.Identifier, a.invalidate)
}

// genQR 请求二维码并启动轮询。
func (a *App) genQR(st *loginState) {
	st.gen++
	st.pollStop = false
	gen := st.gen
	ch := loginChannels[st.channel]
	st.status = "正在获取二维码…"
	go func() {
		var qr backend.QRCode
		var err error
		a.withAPILimit(func() { qr, err = a.API.LoginQR(ch) })
		a.update(func() {
			if gen != st.gen {
				return
			}
			if err != nil {
				st.status = "获取二维码失败：" + err.Error()
				return
			}
			st.qr = &qr
			st.status = "请使用手机扫码"
			// 二维码位图直接进 Covers 缓存
			if raw, derr := base64.StdEncoding.DecodeString(qr.Data); derr == nil {
				a.Covers.PutRaw("login-qr-"+qr.Identifier, raw)
			}
		})
		if err != nil {
			return
		}
		a.pollQR(st, ch, qr.Identifier, gen)
	}()
}

func (a *App) pollQR(st *loginState, ch, identifier string, gen uint64) {
	tk := time.NewTicker(2 * time.Second)
	defer tk.Stop()
	for range tk.C {
		done := false
		a.update(func() { done = gen != st.gen || st.pollStop })
		if done {
			return
		}
		var status backend.QRStatus
		var err error
		a.withAPILimit(func() { status, err = a.API.LoginQRStatus(ch, identifier) })
		a.update(func() {
			if gen != st.gen || st.pollStop {
				return
			}
			if err != nil {
				return
			}
			switch status.Event {
			case 1:
				st.status = "已扫码，请在手机上确认"
			case 2:
				st.status = "等待确认…"
			case 3:
				st.expired = true
				st.status = ""
			case 4:
				st.expired = true
				st.status = "已拒绝登录，请重新生成"
			}
			if status.Done && status.Event == 0 {
				st.status = "登录成功"
				st.pollStop = true
				a.PL.RefreshUser()
				if a.Router.CanGoBack() {
					a.Router.Back()
				} else {
					a.Router.Push("/")
				}
			}
		})
		if err == nil && status.Done {
			return
		}
	}
}
