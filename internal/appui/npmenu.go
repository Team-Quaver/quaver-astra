package appui

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"

	"github.com/Team-Quaver/quaver-astra/internal/player"
	"github.com/Team-Quaver/quaver-astra/internal/streaminfo"

	"github.com/egoist/mygo/ui"
)

// 正在播放页的两个浮层（形态对齐主项目 components/NowPlaying.ts）：
//
//	⋮ 菜单（.np-menu）      —— 同名搜索 / 跳转歌手（多歌手逐项）/ 跳转专辑 /
//	                           翻译开关 / 歌词大小步进。跳转前先收起正在播放页。
//	音质胶囊浮窗（.np-qinfo）—— 只读展示音频流参数（编码/采样率/采样精度/码率/
//	                           声道）+ 档位徽标。它【不是】音质切换器：切档只在
//	                           播放条胶囊上做，两个浮层状态各自独立。
//
// 两个浮层都锚在按钮上方右对齐（主项目 bottom: calc(100% + 8px)），
// 底片走 npPop* 的深玻璃口径。

// npPopPanel 给浮层内容列套上深玻璃外观（主项目 .np-menu / .np-qinfo 共用）。
func npPopPanel(col *ui.Element) *ui.Element {
	return col.Padding(6).Gap(2).Radius(12).Background(npPopBg).Border(1, npPopBorder).
		Shadow(0, 10, 32, 0, ui.RGBA(0, 0, 0, 0.35))
}

// npAttachAbove 把浮层面板改锚到按钮上方、右缘对齐（PopoverBase 默认在下方左对齐）。
func npAttachAbove(panel, anchor *ui.Element) {
	panel.AttachTo(anchor, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, 8, 0)
}

// npJump 跳转前收起正在播放页与两个浮层（主项目 collapseThenRun 的语义）：
// 正在播放页是铺满内容区的常驻浮层，只换路由它仍盖在最上面。
func (a *App) npJump(target string) {
	a.npOpen = false
	a.npMoreOpen = false
	a.npQInfoOpen = false
	a.Router.Push(target)
}

// ===== ⋮ 更多选项菜单 =====

// npMoreMenu 是 ⋮ 按钮的菜单。扁平列表（对齐主项目 openMoreMenu）：
// 无在播曲时只留「未在播放」空态，翻译与歌词大小照旧在。
func (a *App) npMoreMenu(c *ui.Context, anchor *ui.Element) {
	ui.PopoverBase(c, anchor, &a.npMoreOpen, func(panel *ui.Element) {
		npAttachAbove(panel, anchor)
		npPopPanel(ui.Column(c).MinWidth(208)).Children(func() {
			cur, hasCur := a.PL.Current()
			if !hasCur {
				npMenuEmpty(c, "未在播放")
			} else {
				a.npMenuJumps(c, cur)
			}
			a.npMenuTransRow(c)
			a.npMenuSizeRow(c)
		})
	})
}

// npMenuJumps 是三组跳转项：同名搜索、跳转歌手（多歌手逐项）、跳转专辑。
func (a *App) npMenuJumps(c *ui.Context, cur player.Song) {
	name := cur.Name
	if name == "" {
		name = cur.DisplayName()
	}
	npMenuItem(c, "同名搜索", name == "", func() {
		a.npJump("/search?kw=" + url.QueryEscape(name))
	})

	singers := make([]player.Singer, 0, len(cur.Singers))
	for _, g := range cur.Singers {
		if g.Mid != "" {
			singers = append(singers, g)
		}
	}
	if len(singers) == 0 {
		npMenuItem(c, "跳转歌手", true, nil)
	}
	for _, g := range singers {
		label := "跳转歌手"
		if len(singers) > 1 {
			// 多歌手逐项列出，名字区分（主项目跳转歌手：xxx 的形态）。
			label = "跳转歌手：" + g.Name
		}
		g := g
		npMenuItem(c, label, false, func() {
			a.npJump("/singer/" + g.Mid + "?name=" + url.QueryEscape(g.Name))
		})
	}

	amid := cur.AlbumMid
	if amid == "" {
		// 有些条目只有 pmid（形如 004xxx_1），接口吃基名。
		amid = strings.SplitN(cur.AlbumPmid, "_", 2)[0]
	}
	albName := cur.Album
	if albName == "" {
		albName = "专辑"
	}
	npMenuItem(c, "跳转专辑", amid == "", func() {
		a.npJump("/album/" + amid + "?name=" + url.QueryEscape(albName))
	})
}

// npMenuTransRow 是翻译开关行：整行可点（点击目标是行内文字时也算），
// 右侧 Switch 是同一件事的无障碍入口（读屏、空格键）。
func (a *App) npMenuTransRow(c *ui.Context) {
	v := a.PL.ShowTranslation()
	row := ui.Row(c).FillWidth().Padding(7, 10).Radius(8).AlignItems(ui.Center).Gap(10).Transition(hoverFade)
	var sw *ui.Element
	row.Children(func() {
		ui.Text(c, "翻译").FontSize(fz(13)).TextColor(npPopDim).Grow(1)
		sw = ui.Switch(c, &v)
	})
	if row.Hovered() {
		row.Background(npPopHover)
	}
	// 行点击与 Switch 各自只触发一条路径（点开关时命中的是开关，不是行）。
	if sw.Changed() {
		a.PL.SetShowTranslation(v)
	}
	if row.Clicked() {
		v = !v
		a.PL.SetShowTranslation(v)
	}
}

// npMenuSizeRow 是歌词大小步进行（写 Style.LyricScale，百分数存储）。
//
// 主项目在逐字模式藏这行是因为 AMLL 有自己的排版变量；Astra 的逐字行同样吃
// LyricScale（karaokeLine 的字号就是缩放后的 size），所以这里行级/逐字都给，
// 否则逐字模式下没有别的入口能调字号。无歌词时不出现（调了也没东西可调）。
func (a *App) npMenuSizeRow(c *ui.Context) {
	lines, state := a.PL.LyricLines()
	if state != "ok" || len(lines) == 0 {
		return
	}
	val := a.Conf.Float("Style.LyricScale", 100) / 100
	row := ui.Row(c).FillWidth().Padding(5, 10).Radius(8).AlignItems(ui.Center).Gap(10).Transition(hoverFade)
	row.Children(func() {
		ui.Text(c, "歌词大小").FontSize(fz(13)).TextColor(npPopDim).Grow(1)
		ui.Text(c, fmt.Sprintf("%.0f%%", math.Round(val*100))).
			FontSize(fz(11.5)).TextColor(npPopDim).SingleLine()
		st := ui.Stepper(c, &val, 0.7, 1.5, 0.1).Label("歌词大小调节")
		if st.Changed() {
			// 存整数百分比：1.1*100 这类浮点尾巴不能进配置，
			// 否则 lyricLine 每帧读出来的缩放系数都带噪声。
			a.Conf.Set("Style.LyricScale", math.Round(val*100))
		}
	})
	if row.Hovered() {
		row.Background(npPopHover)
	}
}

// npMenuItem 是菜单里的一条跳转项。禁用项不响应点击（对齐主项目 disabled 按钮）。
func npMenuItem(c *ui.Context, label string, disabled bool, run func()) {
	item := ui.ButtonBase(c).FillWidth().Padding(7, 10).Radius(8).Transition(hoverFade)
	// 文字必须包进 item.Children：MyGo 的元素挂给当前构建栈的父节点，
	// 不包就是按钮的兄弟，点文字命不中按钮。
	var txt *ui.Element
	item.Children(func() {
		// Grow(1) 把文字撑满行宽：ButtonBase 自带 .Center()，不撑的话
		// 标签会在行里居中，跟主项目 .np-menu-item 的左对齐不符。
		txt = ui.Text(c, label).FontSize(fz(13)).TextColor(npPopDim).Grow(1).
			SingleLine().Ellipsis("…")
	})
	if disabled {
		item.Disabled(true).Opacity(0.45)
		return
	}
	if item.Hovered() {
		item.Background(npPopHover)
		txt.TextColor(npPopText)
	}
	if item.Clicked() {
		run()
	}
}

// npMenuEmpty 是菜单的空态行（主项目 .np-menu-empty）。
func npMenuEmpty(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(fz(12)).TextColor(npPopText2).SingleLine()
}

// ===== 音质胶囊：只读音频流信息浮窗 =====

// npQualityInfo 是音质胶囊的浮窗：展示当前播放流的参数，不改任何设置。
func (a *App) npQualityInfo(c *ui.Context, anchor *ui.Element) {
	ui.PopoverBase(c, anchor, &a.npQInfoOpen, func(panel *ui.Element) {
		npAttachAbove(panel, anchor)
		npPopPanel(ui.Column(c).MinWidth(228).Padding(10, 12)).Children(func() {
			a.npQInfoBody(c)
		})
	})
}

// npQInfoBody 浮窗内容：标题 + 档位徽标 + 参数行（或空态）。
// 流参数不占 resolve 协商的关键路径——浮窗开着才探测，回来前先给「探测中…」。
func (a *App) npQInfoBody(c *ui.Context) {
	_, hasCur := a.PL.Current()
	st := a.PL.Stream()

	head := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14)
	head.Children(func() {
		ui.Text(c, "音频流信息").FontSize(fz(11)).TextColor(npPopText2).Grow(1)
		if hasCur && st != nil {
			// 档位徽标（主项目 qi-tier）：回退了要写明，不然对不上播放条的 ↓ 前缀。
			label := st.TierLabel
			if label == "" {
				label = qualityLabel(st.Tier, false)
			}
			if st.Degraded {
				label += "（已回退）"
			}
			ui.Text(c, label).FontSize(fz(11)).FontWeight(600).TextColor(npPopDim)
		}
	})
	ui.Box(c).FillWidth().Height(1).Background(npPopLine)

	switch {
	case !hasCur:
		npQIEmpty(c, "未在播放")
	case st == nil || st.URL == "":
		npQIEmpty(c, "等待播放流…")
	default:
		state, info := a.npQInfo.lookup(st.URL, st.Size, a.PL.Duration(), a.invalidate)
		if state != qinfoDone {
			npQIEmpty(c, "探测中…")
			return
		}
		if info == nil {
			npQIEmpty(c, "流信息不可用")
			return
		}
		npQIRow(c, "编码格式", info.Codec)
		npQIRow(c, "采样率", info.FmtSampleRate())
		npQIRow(c, "采样精度", info.FmtBitDepth())
		npQIRow(c, "码率", info.FmtBitrate())
		npQIRow(c, "声道", info.FmtChannels())
	}
}

// npQIRow 是参数行：左标签、右数值（主项目 qi-row 的两栏形态）。
func npQIRow(c *ui.Context, label, value string) {
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Padding(3, 2)
	row.Children(func() {
		ui.Text(c, label).FontSize(fz(12.5)).TextColor(npPopText2).SingleLine()
		ui.Spacer(c)
		ui.Text(c, value).FontSize(fz(12.5)).FontWeight(500).TextColor(npPopText).
			SingleLine().Ellipsis("…")
	})
}

func npQIEmpty(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(fz(12)).TextColor(npPopText2).SingleLine()
}

// ===== 流参数探测缓存 =====

const (
	qinfoIdle = iota
	qinfoPending
	qinfoDone
)

// streamFetch 是流参数探测入口，测试可替换。
var streamFetch = streaminfo.Fetch

// qinfoProbe 缓存一条流的探测结果：探测在后台跑（HTTP Range 取文件头），
// 回来后 invalidate 重绘。UI 线程只在锁内读写元数据，探测结果一次落定。
type qinfoProbe struct {
	mu    sync.Mutex
	url   string
	state int
	info  *streaminfo.Info
}

// lookup 返回 url 对应流的探测状态；没有缓存就挂一个后台探测（只挂一次）。
func (q *qinfoProbe) lookup(url string, size int64, dur float64, redraw func()) (int, *streaminfo.Info) {
	q.mu.Lock()
	if q.url == url {
		state, info := q.state, q.info
		q.mu.Unlock()
		return state, info
	}
	q.url, q.state, q.info = url, qinfoPending, nil
	q.mu.Unlock()

	fn := streamFetch
	go func() {
		info := fn(url, size, dur)
		q.mu.Lock()
		if q.url == url {
			q.info, q.state = info, qinfoDone
		}
		q.mu.Unlock()
		if redraw != nil {
			redraw()
		}
	}()
	return qinfoPending, nil
}

// seed 预置一条流的探测结果（测试用）。
func (q *qinfoProbe) seed(url string, info *streaminfo.Info) {
	q.mu.Lock()
	q.url, q.info, q.state = url, info, qinfoDone
	q.mu.Unlock()
}

// reset 清空缓存（测试换流时用；不能整结构体覆盖，里面有锁）。
func (q *qinfoProbe) reset() {
	q.mu.Lock()
	q.url, q.info, q.state = "", nil, qinfoIdle
	q.mu.Unlock()
}
