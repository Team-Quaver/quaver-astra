package appui

import (
	"encoding/base64"
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
	ui.Column(c).Fill().Center().Gap(14).Children(func() {
		ui.Text(c, "登录 QQ 音乐").FontSize(fz(22)).FontWeight(800)
		ui.Text(c, "扫码后凭证只驻留在本机后端内存中").FontSize(fz(12.5)).TextColor(t.TextMuted)

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

		a.loginQRBox(c, st)

		if st.status != "" {
			ui.Text(c, st.status).FontSize(fz(12.5)).TextColor(t.TextMuted)
		}
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
	card := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Padding(12, 16).
		Radius(12).Background(t.SurfaceHover).Border(1, t.Border)
	card.Children(func() {
		if bmp := a.Covers.Get(st.quick.Avatar, a.invalidate); bmp != nil {
			ui.Image(c, bmp).Size(40, 40).Fit(ui.Cover).Radius(20)
		} else {
			ui.Box(c).Size(40, 40).Radius(20).Background(t.Border).Center().Children(func() {
				ui.Text(c, st.quick.Nickname[:1]).FontSize(fz(15)).FontWeight(700)
			})
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "检测到本机 QQ：").FontSize(fz(12)).TextColor(t.TextMuted)
			ui.Text(c, st.quick.Nickname).FontSize(fz(14)).FontWeight(700).SingleLine()
		})
		if st.quickTrying {
			ui.Spinner(c)
		} else {
			primaryBtn(c, "一键登录", func() { a.startQuickLogin(st) })
		}
	})
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
