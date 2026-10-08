package appui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// fmtTime 把秒数格式化成 m:ss。
func fmtTime(s float64) string {
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d", int(s)/60, int(s)%60)
}

// songListState 是“一列歌”的通用状态：数据 + 分页 + 虚拟列表。
type songListState struct {
	songs    []player.Song
	page     int
	hasMore  bool
	loading  bool
	loaded   bool
	err      string
	fetching bool // 防并发重复拉
	list     ui.ListState
}

// songList 渲染歌曲列表（虚拟化 + 滚到尾部自动翻页）。
// loadMore(page) 由调用方实现（在 goroutine 里拉数据后回填）。
func (a *App) songList(c *ui.Context, st *songListState, loadMore func(page int), doubleClick func(i int)) {
	t := c.Theme()
	st.list.Label = func(i int) string { return st.songs[i].DisplayName() }
	lst := ui.List(c, &st.list, len(st.songs), func(i int) {
		a.songRow(c, st, i, doubleClick)
	}).Grow(1)

	if len(st.songs) == 0 {
		lst.Children(func() {
			msg := "什么都没有"
			if st.loading {
				msg = "加载中…"
			} else if st.err != "" {
				msg = st.err
			}
			ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
				if st.loading {
					ui.Spinner(c)
				}
				ui.Text(c, msg).FontSize(fz(13)).TextColor(t.TextMuted)
			})
		})
	}

	// 接近尾部时翻页
	if st.hasMore && loadMore != nil && !st.loading && !st.fetching && len(st.songs) > 0 {
		if _, last := st.list.Visible(); last >= len(st.songs)-6 {
			st.fetching = true
			st.page++
			go loadMore(st.page)
		}
	}
}

// songRow 单行歌（54px）：序号/♪、封面、标题、歌手·专辑、红心、时长。
func (a *App) songRow(c *ui.Context, st *songListState, i int, doubleClick func(i int)) {
	t := c.Theme()
	s := st.songs[i]
	cur, _ := a.PL.Current()
	isCur := cur.Mid == s.Mid
	playing := a.PL.Playing() && isCur
	loved := a.PL.IsLoved(s.Mid)

	row := ui.Row(c).FillWidth().Height(54).PaddingX(10).Gap(12).AlignItems(ui.Center).Radius(8).
		Transition(hoverFade)
	row.Children(func() {
		// 序号列
		ui.Box(c).Width(26).Center().Children(func() {
			if playing {
				ui.Icon(c, Icons["note"]).FontSize(fz(15)).TextColor(t.Accent).AlignSelf(ui.Center)
			} else {
				ui.Textf(c, "%02d", i+1).FontSize(fz(12)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			}
		})
		// 封面
		art := a.Covers.Get(coverURL(s.AlbumPmid, s.AlbumMid, 300), a.invalidate)
		if art != nil {
			ui.Image(c, art).Size(44, 44).Fit(ui.Cover).Radius(6)
		} else {
			ui.Box(c).Size(44, 44).Radius(6).Background(t.SurfaceHover).Center().Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(fz(18)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			})
		}
		// 标题 + 歌手
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, s.DisplayName()).FontSize(fz(14)).SingleLine().Ellipsis("…")
			sub := s.Artists
			if s.Album != "" {
				sub += " · " + s.Album
			}
			ui.Text(c, sub).FontSize(fz(12)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
		// 红心
		if loved || row.Hovered() {
			hb := ui.ButtonBase(c).Size(28, 28).Radius(14).Center().Transition(hoverFade)
			hb.Children(func() {
				// 配色统一走 heartStyle：未收藏用正文色（原来写死 palLight.heart
				// —— 浅色主题的硬编码红，深色底上对比度不够，也没区分已收藏/
				// 未收藏），已收藏是实心红心。
				name, col := heartStyle(loved, false, t)
				ui.Icon(c, Icons[name]).FontSize(fz(16)).TextColor(col).AlignSelf(ui.Center)
			})
			if hb.Hovered() {
				hb.Background(t.SurfaceHover)
			}
			if hb.Clicked() {
				a.PL.ToggleLove(s)
			}
		} else {
			ui.Box(c).Width(28)
		}
		// 时长
		ui.Text(c, fmtTime(s.Interval)).FontSize(fz(12)).TextColor(t.TextMuted).Width(44).TextAlign(ui.End)
	})

	if row.Hovered() && !isCur {
		row.Background(t.SurfaceHover)
	}
	if isCur {
		row.Background(a.rowTint(t))
	}

	if row.DoubleClicked() && doubleClick != nil {
		doubleClick(i)
	}
	row.ContextMenu(func(m *ui.Menu) {
		// 下一首播放：排到当前曲之后等着，不打断当前播放（主项目
		// enqueueNext 的语义）。队列还空着时 EnqueueNext 会直接起播，
		// toast 文案跟着分叉，否则「什么都没发生」。
		if m.Item("下一首播放").Chosen() {
			name := s.DisplayName()
			if name == "" {
				name = "这首歌"
			}
			_, playing := a.PL.Current()
			a.PL.EnqueueNext(s)
			if playing {
				c.Toast("已插队：" + name + "（下一首播放）")
			} else {
				c.Toast("开始播放：" + name)
			}
		}
		if m.Item("添加到队列").Chosen() {
			a.PL.EnqueueMany([]player.Song{s})
			c.Toast("已加入队列")
		}
		m.Separator()
		label := "收藏"
		if loved {
			label = "取消收藏"
		}
		if m.Item(label).Chosen() {
			a.PL.ToggleLove(s)
		}
		m.Separator()
		m.Submenu("跳转至", func(m *ui.Menu) {
			m.Submenu("歌手", func(m *ui.Menu) {
				shown := false
				for _, g := range s.Singers {
					if g.Mid == "" {
						continue
					}
					shown = true
					g := g
					if m.Item(g.Name).Chosen() {
						a.Router.Push("/singer/" + g.Mid + "?name=" + url.QueryEscape(g.Name))
					}
				}
				if !shown {
					m.Item("没有歌手信息").Disabled(true)
				}
			})
			m.Submenu("专辑", func(m *ui.Menu) {
				amid := s.AlbumMid
				if amid == "" {
					// 有些条目只有 pmid（形如 004xxx_1），接口吃基名。
					amid = strings.SplitN(s.AlbumPmid, "_", 2)[0]
				}
				name := s.Album
				if name == "" {
					name = "专辑"
				}
				if m.Item(name).Disabled(amid == "").Chosen() {
					a.Router.Push("/album/" + amid + "?name=" + url.QueryEscape(name))
				}
			})
			kw := s.Name
			if kw == "" {
				kw = s.DisplayName()
			}
			if m.Item("同名搜索").Disabled(kw == "").Chosen() {
				a.Router.Push("/search?kw=" + url.QueryEscape(kw))
			}
		})
		m.Submenu("更多操作", func(m *ui.Menu) {
			if m.Item("复制歌曲链接").Disabled(s.Mid == "").Chosen() {
				c.WriteClipboard("https://y.qq.com/n/ryqq/songDetail/" + s.Mid)
				c.Toast("已复制歌曲链接")
			}
			if m.Item("复制歌曲名称").Chosen() {
				text := s.DisplayName()
				if s.Subtitle != "" {
					text += " " + s.Subtitle
				}
				c.WriteClipboard(text)
				c.Toast("已复制：" + s.DisplayName())
			}
		})
	})
}

// rowTint 当前播放行的底色。
func (a *App) rowTint(t *ui.Theme) ui.Color {
	accent, _, ok := a.tint.get()
	if !ok {
		accent = t.Accent
	}
	return accent.Alpha(0.1)
}

// playListNow 用列表替换队列并播放第 i 首。
func (a *App) playListNow(songs []player.Song, i int) {
	if len(songs) == 0 {
		return
	}
	a.PL.PlayList(songs, i)
}

// sectionHeader 是区块标题（右侧可选动作）。
func sectionHeader(c *ui.Context, title string, action func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(fz(16)).FontWeight(800)
		ui.Spacer(c)
		if action != nil {
			action()
		}
	})
}

// linkButton 是文字按钮。
func linkButton(c *ui.Context, label string, onClick func()) *ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Padding(4, 8).Radius(6).Children(func() {
		ui.Text(c, label).FontSize(fz(12.5)).TextColor(t.TextMuted)
	})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
		b.TextColor(t.Text)
	}
	if b.Clicked() {
		onClick()
	}
	return b
}

// primaryBtn 主按钮。
func primaryBtn(c *ui.Context, label string, onClick func()) {
	if ui.PrimaryButton(c, label).Clicked() {
		onClick()
	}
}

// plainBtn 次按钮。
func plainBtn(c *ui.Context, label string, onClick func()) {
	if ui.Button(c, label).Clicked() {
		onClick()
	}
}
