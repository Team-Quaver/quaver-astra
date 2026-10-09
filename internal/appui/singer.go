package appui

import (
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// 歌手页与专辑页（对齐主项目 views.ts 的 singerView / albumView）：
//
//	歌手页 = 信息头 + 分类标签（热歌 / 新歌 / 专辑）；三块内容进页一次拉齐，
//	标签只决定显示哪一块，切换不重新请求、不重置滚动位置。
//	专辑页 = 信息头 + 歌曲列表，是歌手页「专辑」标签卡的落点。

// joinParts 用 " · " 连接非空元信息片段。
func joinParts(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += p
	}
	return out
}

// singerPicURL 歌手立绘：上游 pic 优先（http: 升级为 https:，同主项目 upPic），
// 没有立绘时兜底 T001R 头像地址（专辑封面走 T002R，两套前缀不能混）。
func singerPicURL(mid, pic string) string {
	pic = strings.TrimSpace(pic)
	if strings.HasPrefix(pic, "http:") {
		pic = "https:" + strings.TrimPrefix(pic, "http:")
	}
	if pic != "" {
		return pic
	}
	if mid == "" {
		return ""
	}
	return "https://y.gtimg.cn/music/photo_new/T001R300x300M000" + mid + ".jpg"
}

// ===== 歌手页 =====

type singerState struct {
	loaded   bool
	loading  bool
	err      string
	name     string // URL 带来的占位名（info/desc 就绪前显示）
	info     backend.SingerBase
	profile  backend.SingerProfile
	hot      []player.Song
	hotTotal int64 // 热歌总数（接口给的总量，比拉到的 50 条大）
	news     []player.Song
	albums   []backend.SingerAlbum
	albumN   int64
	tab      int // 0 热歌 1 新歌 2 专辑
	expand   bool
	hotList  songListState
	newList  songListState
}

// singerStateAt 取（或建）歌手页状态；每 mid 一份，往返保留数据。
func (a *App) singerStateAt(mid, name string) *singerState {
	if a.singers == nil {
		a.singers = map[string]*singerState{}
	}
	return getOrCreatePage(a.singers, &a.singerRec, mid, func() *singerState {
		return &singerState{name: name}
	})
}

// displayName 真名优先：info → desc → URL 占位名。
func (st *singerState) displayName() string {
	switch {
	case st.info.Name != "":
		return st.info.Name
	case st.profile.Name != "":
		return st.profile.Name
	case st.name != "":
		return st.name
	}
	return "歌手"
}

func (a *App) singerView(c *ui.Context, mid, name string) {
	st := a.singerStateAt(mid, name)
	a.ensureSinger(st, mid)
	t := c.Theme()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 24, 24).Gap(16)).Children(func() {
		if st.err != "" {
			ui.Column(c).FillWidth().Center().Gap(8).Padding(48).Children(func() {
				ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "重试", func() {
					st.loaded, st.loading, st.err = false, false, ""
				})
			})
			return
		}
		if !st.loaded {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}

		a.singerHead(c, st, mid)

		ui.Tabs(c, &st.tab, "热歌", "新歌", "专辑")
		switch st.tab {
		case 1:
			st.newList.songs = st.news
			a.singerSongPanel(c, st.news, &st.newList, "暂无新歌", func(i int) {
				a.playListNow(st.news, i)
			})
		case 2:
			a.singerAlbumPanel(c, st, mid)
		default:
			st.hotList.songs = st.hot
			a.singerSongPanel(c, st.hot, &st.hotList, "没有取到热门歌曲", func(i int) {
				a.playListNow(st.hot, i)
			})
		}
	})
}

// singerHead 信息头：圆形立绘 + 名称/元信息/简介 + 行动区（与歌单页同一套骨架，
// 主项目 mountHead 的歌手版：artRound）。
func (a *App) singerHead(c *ui.Context, st *singerState, mid string) {
	t := c.Theme()
	// Avatar 与 profile.Pic 是两个来源，这里取「先到先得」：info 的 avatar
	// 在就用它，否则用 desc 的 pic（singerPicURL 负责 http→https 与 T001R 兜底）。
	pic := st.info.Avatar
	if pic == "" {
		pic = st.profile.Pic
	}
	art := a.Covers.Get(singerPicURL(mid, pic), a.invalidate)

	ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Center).Children(func() {
		if art != nil {
			ui.Image(c, art).Size(168, 168).Fit(ui.Cover).Radius(84)
		} else {
			ui.Box(c).Size(168, 168).Radius(84).Background(t.SurfaceHover).Center().Children(func() {
				ui.Icon(c, Icons["user"]).FontSize(fz(56)).TextColor(t.TextMuted).AlignSelf(ui.Center)
			})
		}
		ui.Column(c).Grow(1).Gap(8).Children(func() {
			ui.Text(c, st.displayName()).FontSize(fz(26)).FontWeight(800).MaxLines(2).Ellipsis("…")

			meta := joinParts(
				st.metaForeign(),
				st.profile.Area,
				st.profile.Birthday,
				metaCount("歌曲", st.songTotal()),
				metaCount("专辑", st.albumN),
			)
			ui.Text(c, meta).FontSize(fz(13)).TextColor(t.TextMuted)

			if st.profile.Desc != "" {
				lines := 2
				toggle := "展开"
				if st.expand {
					lines = 0
					toggle = "收起"
				}
				body := ui.Text(c, st.profile.Desc).FontSize(fz(12.5)).TextColor(t.TextMuted)
				if lines > 0 {
					body = body.MaxLines(lines).Ellipsis("…")
				}
				linkButton(c, toggle, func() { st.expand = !st.expand })
			}

			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				if len(st.hot) > 0 {
					primaryBtn(c, "播放热歌", func() { a.playListNow(st.hot, 0) })
				}
				if len(st.news) > 0 {
					plainBtn(c, "播放新歌", func() { a.playListNow(st.news, 0) })
				}
			})
		})
	})
}

// metaForeign 外文名只在与展示名不同时才进元信息。
func (st *singerState) metaForeign() string {
	if st.profile.ForeignName != "" && st.profile.ForeignName != st.displayName() {
		return st.profile.ForeignName
	}
	return ""
}

func (st *singerState) songTotal() int64 {
	if n := st.hotTotal; n > 0 {
		return n
	}
	return int64(len(st.hot))
}

// metaCount 形如「歌曲 128」的计数片段（0 计数不显示）。
func metaCount(label string, n int64) string {
	if n <= 0 {
		return ""
	}
	return label + " " + strconv.FormatInt(n, 10)
}

// singerSongPanel 歌曲面板（整页滚动，行数固定 50/30 无需虚拟化）。
func (a *App) singerSongPanel(c *ui.Context, songs []player.Song, st *songListState, empty string, play func(i int)) {
	if len(songs) == 0 {
		ui.Text(c, empty).FontSize(fz(13)).TextColor(c.Theme().TextMuted).Padding(24)
		return
	}
	ui.Column(c).FillWidth().Children(func() {
		for i := range songs {
			a.songRow(c, st, i, play)
		}
	})
}

func (a *App) singerAlbumPanel(c *ui.Context, st *singerState, mid string) {
	t := c.Theme()
	if len(st.albums) == 0 {
		ui.Text(c, "暂无专辑").FontSize(fz(13)).TextColor(t.TextMuted).Padding(24)
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "共 "+strconv.FormatInt(st.albumN, 10)+" 张").FontSize(fz(12)).TextColor(t.TextMuted)
	})
	ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
		for i := range st.albums {
			a.albumCard(c, st.albums[i])
		}
	})
}

// albumCard 专辑卡（150 宽，同 playlistCard 卡面；落点是专辑页）。
func (a *App) albumCard(c *ui.Context, al backend.SingerAlbum) {
	t := c.Theme()
	art := a.Covers.Get(coverURL(al.Pmid, al.Mid, 300), a.invalidate)
	card := ui.Column(c).Width(150).Gap(6)
	card.Children(func() {
		box := ui.Box(c).Size(150, 150).Clip().Radius(10).Background(t.SurfaceHover).
			Transition(hoverFade)
		box.Children(func() {
			if art != nil {
				ui.Image(c, art).Fill().Fit(ui.Cover)
			} else {
				ui.Box(c).Fill().Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(34)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			}
		})
		ui.Text(c, al.Name).FontSize(fz(12.5)).MaxLines(2).Ellipsis("…").LineHeight(1.35)
		ui.Text(c, joinParts(al.AlbumType, al.TimePublic)).
			FontSize(fz(11)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
	})
	if card.Clicked() {
		a.Router.Push("/album/" + al.Mid + "?name=" + url.QueryEscape(al.Name))
	}
}

// singerAlbumPageSize 是歌手专辑接口的单页大小。专辑总数没有可靠上限，
// 必须按页拉完；只取第一页会让后发行的专辑（例如周杰伦《太阳之子》）漏出
// 歌手页的「专辑」标签。
const singerAlbumPageSize = 30

type singerAlbumPageFunc func(page, num int) (backend.SingerAlbums, error)

// fetchAllSingerAlbums 按页拉取完整歌手专辑列表。优先用响应 total 判断终点，
// total 缺失时继续请求，遇到空页或不再增加新专辑的页面即停止。
// 后续分页失败时保留已经取到的专辑，避免网络抖动把整个「专辑」标签清空。
func fetchAllSingerAlbums(fetch singerAlbumPageFunc, pageSize int) (backend.SingerAlbums, error) {
	if pageSize <= 0 {
		pageSize = singerAlbumPageSize
	}
	first, err := fetch(1, pageSize)
	if err != nil {
		return first, err
	}

	out := first
	out.AlbumList = append([]backend.SingerAlbum(nil), first.AlbumList...)
	seen := make(map[string]struct{}, len(out.AlbumList))
	for _, album := range out.AlbumList {
		seen[singerAlbumKey(album)] = struct{}{}
	}
	if out.Total <= 0 {
		out.Total = first.Total
	}

	for page := 2; ; page++ {
		if out.Total > 0 && int64(len(out.AlbumList)) >= out.Total {
			break
		}
		next, err := fetch(page, pageSize)
		if err != nil {
			return out, err
		}
		before := len(out.AlbumList)
		for _, album := range next.AlbumList {
			key := singerAlbumKey(album)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out.AlbumList = append(out.AlbumList, album)
		}
		if out.Total <= 0 {
			out.Total = next.Total
		}
		if len(next.AlbumList) == 0 || len(out.AlbumList) == before || len(next.AlbumList) < pageSize {
			break
		}
	}
	return out, nil
}

// singerAlbumKey 跨页去重。mid 是稳定标识；极少数条目缺 mid 时退化为专辑
// 展示属性，既能识别重复页，也不会把同名的不同版本误合并。
func singerAlbumKey(album backend.SingerAlbum) string {
	if album.Mid != "" {
		return "mid:" + album.Mid
	}
	return strings.Join([]string{"brief", album.Pmid, album.Name, album.TranName, album.AlbumType, album.TimePublic, album.SingerName}, "\x00")
}

// ensureSinger 五路并拉（info / desc / 热歌 / 新歌 / 专辑）：简介与专辑失败
// 不阻塞主内容，热歌是页面主体，拉不到才算整页失败。
func (a *App) ensureSinger(st *singerState, mid string) {
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	go func() {
		var (
			info    backend.SingerBase
			profile backend.SingerProfile
			hot     backend.SingerSongs
			news    backend.SingerSongs
			albums  backend.SingerAlbums
			hotErr  error
		)
		var wg sync.WaitGroup
		wg.Add(5)
		go func() { defer wg.Done(); a.withAPILimit(func() { info, _ = a.API.SingerInfo(mid) }) }()
		go func() { defer wg.Done(); a.withAPILimit(func() { profile, _ = a.API.SingerDesc(mid) }) }()
		go func() { defer wg.Done(); a.withAPILimit(func() { hot, hotErr = a.API.SingerSongs(mid, 1, 50, 1) }) }()
		go func() { defer wg.Done(); a.withAPILimit(func() { news, _ = a.API.SingerSongs(mid, 1, 30, 2) }) }()
		go func() {
			defer wg.Done()
			albums, _ = fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
				var result backend.SingerAlbums
				var err error
				a.withAPILimit(func() { result, err = a.API.SingerAlbums(mid, page, num) })
				return result, err
			}, singerAlbumPageSize)
		}()
		wg.Wait()
		a.update(func() {
			st.loading = false
			st.loaded = true
			if hotErr != nil {
				st.err = hotErr.Error()
				return
			}
			st.info = info
			st.profile = profile
			st.hot = toPlayerSongs(hot.SongList)
			st.hotTotal = hot.TotalNum
			// 最新发布与热门同列风格：去掉与热门完全重合的条目
			//（order=2 是按发行时间倒序，头部往往就是热门歌）。
			seen := map[string]bool{}
			for _, s := range st.hot {
				seen[s.Mid] = true
			}
			st.news = st.news[:0]
			for _, s := range toPlayerSongs(news.SongList) {
				if !seen[s.Mid] {
					st.news = append(st.news, s)
				}
			}
			st.albums = albums.AlbumList
			st.albumN = albums.Total
			if st.albumN <= 0 {
				st.albumN = int64(len(st.albums))
			}
		})
	}()
}

// ===== 专辑页 =====

type albumState struct {
	loaded      bool
	loading     bool
	err         string
	name        string // URL 占位名
	info        backend.AlbumInfo
	singers     []backend.AlbumSinger
	songs       []player.Song
	total       int64
	page        int
	hasMore     bool
	expand      bool
	loadingMore bool
	list        songListState
	scroll      ui.ScrollState
}

func (a *App) albumStateAt(mid, name string) *albumState {
	if a.albums == nil {
		a.albums = map[string]*albumState{}
	}
	return getOrCreatePage(a.albums, &a.albumRec, mid, func() *albumState {
		return &albumState{name: name}
	})
}

func (st *albumState) displayName() string {
	if st.info.Name != "" {
		return st.info.Name
	}
	if st.name != "" {
		return st.name
	}
	return "专辑"
}

// artistName 专辑归属歌手：detail 的 singer 字符串 → singers 列表 → 首曲歌手。
func (st *albumState) artistName() string {
	if s := strings.TrimSpace(st.info.Singer); s != "" {
		return s
	}
	if len(st.singers) > 0 {
		names := make([]string, 0, len(st.singers))
		for _, g := range st.singers {
			if g.Name != "" {
				names = append(names, g.Name)
			}
		}
		return strings.Join(names, "/")
	}
	if len(st.songs) > 0 {
		return st.songs[0].Artists
	}
	return ""
}

func (a *App) albumView(c *ui.Context, mid, name string) {
	st := a.albumStateAt(mid, name)
	a.ensureAlbum(st, mid)
	t := c.Theme()

	a.pageEnter(c, ui.Scroll(c).Fill().Padding(20, 24, 24, 24).Gap(16).TrackScroll(&st.scroll)).Children(func() {
		if st.err != "" {
			ui.Column(c).FillWidth().Center().Gap(8).Padding(48).Children(func() {
				ui.Text(c, st.err).FontSize(fz(13)).TextColor(t.TextMuted)
				plainBtn(c, "重试", func() {
					st.loaded, st.loading, st.err = false, false, ""
				})
			})
			return
		}
		if !st.loaded {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}

		// 信息头（同歌单页骨架）
		art := a.Covers.Get(coverURL(st.info.Pmid, mid, 300), a.invalidate)
		ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Center).Children(func() {
			if art != nil {
				ui.Image(c, art).Size(168, 168).Fit(ui.Cover).Radius(14)
			} else {
				ui.Box(c).Size(168, 168).Radius(14).Background(t.SurfaceHover).Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(48)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			}
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				ui.Text(c, st.displayName()).FontSize(fz(26)).FontWeight(800).MaxLines(2).Ellipsis("…")
				ui.Text(c, joinParts(
					st.artistName(),
					st.info.TimePublic,
					metaCount("歌曲", st.songTotal()),
					st.info.AlbumType,
				)).FontSize(fz(13)).TextColor(t.TextMuted)
				if st.info.Desc != "" {
					lines := 2
					toggle := "展开"
					if st.expand {
						lines = 0
						toggle = "收起"
					}
					body := ui.Text(c, st.info.Desc).FontSize(fz(12.5)).TextColor(t.TextMuted)
					if lines > 0 {
						body = body.MaxLines(lines).Ellipsis("…")
					}
					linkButton(c, toggle, func() { st.expand = !st.expand })
				}
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					if len(st.songs) > 0 {
						primaryBtn(c, "播放全部", func() { a.playListNow(st.songs, 0) })
					}
				})
			})
		})

		// 歌曲行：整页滚动，触底翻页
		ui.Column(c).FillWidth().Children(func() {
			st.list.songs = st.songs
			for i := range st.songs {
				a.songRow(c, &st.list, i, func(i int) { a.playListNow(st.songs, i) })
			}
			if st.loadingMore {
				ui.Row(c).FillWidth().Center().Padding(14).Children(func() { ui.Spinner(c) })
			}
		})

		if st.hasMore && !st.loadingMore && st.scroll.MaxY > 0 && st.scroll.Y >= st.scroll.MaxY-300 {
			st.loadingMore = true
			st.page++
			go a.loadAlbumPage(st, mid, st.page)
		}
	})
}

func (st *albumState) songTotal() int64 {
	if n := st.total; n > 0 {
		return n
	}
	return int64(len(st.songs))
}

func (a *App) ensureAlbum(st *albumState, mid string) {
	if st.loaded || st.loading {
		return
	}
	st.loading = true
	st.page = 1
	go func() {
		var (
			detail backend.AlbumDetail
			songs  backend.AlbumSongs
			dErr   error
			sErr   error
		)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a.withAPILimit(func() { detail, dErr = a.API.AlbumDetail(mid) }) }()
		go func() { defer wg.Done(); a.withAPILimit(func() { songs, sErr = a.API.AlbumSongs(mid, 1, 100) }) }()
		wg.Wait()
		a.update(func() {
			st.loading = false
			st.loaded = true
			// 歌曲是页面主体；详情失败只影响头部（名称回退 URL 占位名）。
			if sErr != nil {
				st.err = sErr.Error()
				return
			}
			_ = dErr
			st.info = detail.Album
			st.singers = detail.Singers
			st.songs = toPlayerSongs(songs.SongList)
			st.total = songs.TotalNum
			st.hasMore = int64(len(st.songs)) < st.total
			st.list.songs = st.songs
		})
	}()
}

func (a *App) loadAlbumPage(st *albumState, mid string, page int) {
	go func() {
		var d backend.AlbumSongs
		var err error
		a.withAPILimit(func() { d, err = a.API.AlbumSongs(mid, page, 100) })
		a.update(func() {
			st.loadingMore = false
			if err != nil {
				return
			}
			st.songs = append(st.songs, toPlayerSongs(d.SongList)...)
			st.hasMore = int64(len(st.songs)) < st.total
			st.list.songs = st.songs
		})
	}()
}
