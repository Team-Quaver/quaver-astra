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
	gen      uint64 // 生成批次号，防止旧轮询写新状态
	pollStop bool
}

var loginChannels = []string{"mobile", "qq", "wx"}

func (a *App) loginView(c *ui.Context) {
	st := &a.login
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(14).Children(func() {
		ui.Text(c, "登录 QQ 音乐").FontSize(22).FontWeight(800)
		ui.Text(c, "扫码后凭证只驻留在本机后端内存中").FontSize(12.5).TextColor(t.TextMuted)

		seg := ui.Row(c).Gap(6).Padding(4).Radius(10).Background(t.SurfaceHover)
		seg.Children(func() {
			for i, ch := range []string{"手机", "QQ", "微信"} {
				idx := i
				btn := ui.ButtonBase(c).Padding(6, 16).Radius(8)
				btn.Children(func() {
					ui.Text(c, ch).FontSize(13)
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

		a.loginQRBox(c, st)

		if st.status != "" {
			ui.Text(c, st.status).FontSize(12.5).TextColor(t.TextMuted)
		}
	})
}

func (a *App) loginQRBox(c *ui.Context, st *loginState) {
	t := c.Theme()
	switch {
	case st.qr == nil && !st.expired:
		ui.Box(c).Size(220, 220).Radius(12).Background(t.SurfaceHover).Border(1, t.Border).Center().Children(func() {
			if ui.Button(c, "生成二维码").Clicked() {
				a.genQR(st)
			}
		}).Fill()
	case st.expired:
		ui.Box(c).Size(220, 220).Radius(12).Background(t.SurfaceHover).Border(1, t.Border).Center().Children(func() {
			ui.Column(c).Center().Gap(8).Children(func() {
				ui.Text(c, "二维码已过期").FontSize(13).TextColor(t.TextMuted).AlignSelf(ui.Center)
				if ui.Button(c, "重新生成").Clicked() {
					st.expired = false
					a.genQR(st)
				}
			})
		}).Fill()
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
	gen := st.gen
	ch := loginChannels[st.channel]
	st.status = "正在获取二维码…"
	go func() {
		qr, err := a.API.LoginQR(ch)
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
		status, err := a.API.LoginQRStatus(ch, identifier)
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
