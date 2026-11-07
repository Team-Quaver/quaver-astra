// 微云音乐网盘：授权、网盘库列表、刮削与播放。
//
// 数据全部来自内嵌后端：GET /cloud/library 合并 QQ 云盘匹配结果与本地
// 刮削状态；播放走 /stream/resolve 的 cloud_file_id 分支，QMCv1 解密与
// Range 中继都在后端完成。前端只负责把条目转换成播放器曲目。
package appui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type cloudState struct {
	loaded  bool
	loading bool
	err     string

	items  []backend.CloudItem
	stats  backend.CloudStats
	scrape backend.CloudScrapeState

	status   backend.WeiyunStatus
	statusAt time.Time

	// 网页授权：authURL 打开后后台轮询，直到确认、过期或出错。
	authURL     string
	authExpires time.Time
	authPolling bool
	authMsg     string

	// 手动令牌输入草稿（TextInput 需要跨帧稳定持有）。
	tokenDraft string

	scrapePolling bool
	list          ui.ListState

	// 手动编辑曲目信息（后端 /cloud/meta/set，编辑后刮削不再覆盖）。
	edit cloudEditState
}

type cloudEditState struct {
	open          bool
	key           string
	title, artist string
	album         string
}

func (a *App) cloudView(c *ui.Context) {
	st := &a.cloud
	a.ensureCloud()

	a.pageEnter(c, ui.Column(c).Fill().Padding(20, 24, 24, 24).Gap(14)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "音乐网盘").FontSize(fz(24)).FontWeight(800)
			if n := st.stats.Total; n > 0 {
				ui.Textf(c, "%d 个文件", n).FontSize(fz(13)).TextColor(c.Theme().TextMuted)
			}
			ui.Spacer(c)
			linkButton(c, "刷新", func() {
				st.loaded = false
				a.ensureCloud()
			})
			if st.stats.Total > 0 {
				linkButton(c, "刮削全部", func() { a.startCloudScrape(st, nil, true) })
			}
		})

		if !a.PL.LoggedIn() {
			a.cloudNotice(c, "登录 QQ 音乐后才能读取网盘列表。", "去登录", func() {
				a.Router.Push("/login")
			})
			return
		}

		// 微云 MCP 令牌是独立授权：QQ 登录只决定列表，播放还需要它。
		a.cloudAuthPanel(c, st)

		if st.loading && !st.loaded {
			ui.Column(c).FillWidth().Center().Padding(48).Children(func() { ui.Spinner(c) })
			return
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(fz(13)).TextColor(c.Theme().TextMuted).Padding(12)
			linkButton(c, "重试", func() {
				st.loaded = false
				a.ensureCloud()
			})
			return
		}
		if len(st.items) == 0 {
			ui.Column(c).FillWidth().Center().Gap(8).Padding(48).Children(func() {
				ui.Icon(c, Icons["cloud"]).FontSize(fz(34)).TextColor(c.Theme().TextMuted)
				ui.Text(c, "网盘里还没有音乐文件").FontSize(fz(13)).TextColor(c.Theme().TextMuted)
			})
			return
		}

		a.cloudScrapeLine(c, st)
		a.cloudList(c, st)
		a.cloudEditDialog(c, st)
	})
}

// cloudNotice 是一条带动作的提示。
func (a *App) cloudNotice(c *ui.Context, msg, action string, fn func()) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(12, 14).Radius(10).
		Background(t.SurfaceHover.Alpha(0.55)).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, msg).FontSize(fz(12.5)).TextColor(t.TextMuted).Grow(1)
		if action != "" {
			plainBtn(c, action, fn)
		}
	})
}

func (a *App) ensureCloud() {
	st := &a.cloud
	if st.loading || st.loaded {
		return
	}
	if a.API == nil {
		st.loaded = true
		return
	}
	st.loading = true
	go func() {
		var status backend.WeiyunStatus
		var lib backend.CloudLibrary
		var libErr error
		a.withAPILimit(func() { status, _ = a.API.WeiyunStatus() })
		a.withAPILimit(func() { lib, libErr = a.API.CloudLibrary() })
		a.update(func() {
			st.loading = false
			st.loaded = true
			st.status = status
			st.statusAt = time.Now()
			if libErr != nil {
				st.err = libErr.Error()
				return
			}
			st.err = ""
			st.items = lib.Items
			st.stats = lib.Stats
			st.scrape = lib.Scrape
			if lib.Scrape.Running {
				a.pollCloudScrape(st)
			}
		})
	}()
}

// cloudAuthPanel 显示微云授权状态；未授权时展开授权入口。
func (a *App) cloudAuthPanel(c *ui.Context, st *cloudState) {
	t := c.Theme()
	if !st.authPolling && (st.statusAt.IsZero() || time.Since(st.statusAt) > 3*time.Second) {
		// 已授权时只低频刷新状态，不阻塞列表；授权轮询期间不重复查。
		st.statusAt = time.Now()
		go func() {
			var latest backend.WeiyunStatus
			var err error
			a.withAPILimit(func() { latest, err = a.API.WeiyunStatus() })
			if err == nil {
				a.update(func() {
					st.status = latest
					st.statusAt = time.Now()
				})
			}
		}()
	}

	if st.status.Configured {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, Icons["cloud"]).FontSize(fz(14)).TextColor(t.Accent)
			ui.Text(c, "微云已连接").FontSize(fz(12.5)).TextColor(t.TextMuted)
			ui.Spacer(c)
			linkButton(c, "断开授权", func() {
				go func() {
					_, err := a.API.WeiyunAuthClear()
					a.update(func() {
						if err != nil {
							st.authMsg = "断开失败：" + err.Error()
							return
						}
						st.status.Configured = false
						st.authMsg = "已断开微云授权"
					})
				}()
			})
		})
		if st.authMsg != "" {
			ui.Text(c, st.authMsg).FontSize(fz(11.5)).TextColor(t.TextMuted)
		}
		return
	}

	ui.Column(c).FillWidth().Gap(10).Padding(14).Radius(10).
		Background(t.SurfaceHover.Alpha(0.5)).Border(1, t.Border).Children(func() {
		ui.Text(c, "连接微云").FontSize(fz(14)).FontWeight(700)
		ui.Text(c, "授权后即可播放网盘文件；令牌只保存在本机。").
			FontSize(fz(11.5)).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			primaryBtn(c, "打开授权页面", func() { a.startWeiyunAuth(c, st) })
			if st.authPolling {
				ui.Spinner(c)
				ui.Text(c, "等待网页确认…").FontSize(fz(12)).TextColor(t.TextMuted)
			}
		})
		if !st.authExpires.IsZero() && time.Now().Before(st.authExpires) {
			ui.Text(c, "授权码有效期至 "+st.authExpires.Local().Format("15:04:05")).
				FontSize(fz(11.5)).TextColor(t.TextMuted)
		}
		if st.authMsg != "" {
			ui.Text(c, st.authMsg).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
		}

		ui.Divider(c)
		ui.Text(c, "已有令牌？直接粘贴保存。").
			FontSize(fz(11.5)).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c, &st.tokenDraft).Label("微云令牌").
				Placeholder("粘贴微云 MCP 令牌").Width(320)
			if in.Submitted() && strings.TrimSpace(st.tokenDraft) != "" {
				a.saveWeiyunToken(st)
			}
			plainBtn(c, "保存令牌", func() { a.saveWeiyunToken(st) })
		})
	})
}

func (a *App) startWeiyunAuth(c *ui.Context, st *cloudState) {
	if st.authPolling {
		return
	}
	var start backend.WeiyunAuthStart
	var err error
	a.withAPILimit(func() { start, err = a.API.WeiyunAuthStart() })
	if err != nil {
		st.authMsg = "发起授权失败：" + err.Error()
		return
	}
	st.authURL = start.URL
	st.authExpires = time.Now().Add(time.Duration(start.ExpiresInSec) * time.Second)
	st.authMsg = ""
	if c != nil && start.URL != "" {
		c.OpenURL(start.URL)
	}
	a.pollWeiyunAuth(st, start.ExpiresInSec)
}

// pollWeiyunAuth 每 2 秒询问一次后端授权是否完成；409 是「网页上还没点」。
func (a *App) pollWeiyunAuth(st *cloudState, expiresInSec int) {
	if st.authPolling {
		return
	}
	st.authPolling = true
	a.invalidate()
	go func() {
		deadline := time.Now().Add(time.Duration(expiresInSec) * time.Second)
		for {
			time.Sleep(2 * time.Second)
			err := a.API.WeiyunAuthPoll()
			if err == nil {
				a.update(func() {
					st.authPolling = false
					st.authMsg = "微云授权完成"
					st.status.Configured = true
					st.statusAt = time.Now()
					st.items = nil
					st.loaded = false
					a.ensureCloud()
				})
				return
			}
			if backend.WeiyunAuthPending(err) {
				if time.Now().After(deadline) {
					a.update(func() {
						st.authPolling = false
						st.authMsg = "授权码已过期，请重新发起授权。"
					})
					return
				}
				continue
			}
			a.update(func() {
				st.authPolling = false
				st.authMsg = "授权失败：" + err.Error()
			})
			return
		}
	}()
}

func (a *App) saveWeiyunToken(st *cloudState) {
	token := strings.TrimSpace(st.tokenDraft)
	if token == "" {
		st.authMsg = "令牌不能为空"
		return
	}
	go func() {
		_, err := a.API.WeiyunAuthSet(token, "")
		a.update(func() {
			if err != nil {
				st.authMsg = "保存失败：" + err.Error()
				return
			}
			st.tokenDraft = ""
			st.authMsg = "令牌已保存"
			st.status.Configured = true
			st.statusAt = time.Now()
			st.loaded = false
			a.ensureCloud()
		})
	}()
}

func (a *App) cloudScrapeLine(c *ui.Context, st *cloudState) {
	t := c.Theme()
	sc := st.scrape
	if sc.Running {
		done := sc.Processed
		total := sc.Total
		if total <= 0 {
			total = st.stats.Total
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c)
			ui.Textf(c, "刮削中 %d / %d", done, total).FontSize(fz(12)).TextColor(t.TextMuted)
			if sc.Matched > 0 || sc.Unmatched > 0 {
				ui.Textf(c, "匹配 %d · 未匹配 %d", sc.Matched, sc.Unmatched).
					FontSize(fz(12)).TextColor(t.TextMuted)
			}
		})
		return
	}
	if st.stats.Scraped > 0 || st.stats.WithCover > 0 || st.stats.WithLyric > 0 {
		ui.Textf(c, "已刮削 %d · 有封面 %d · 有歌词 %d · 可播 %d",
			st.stats.Scraped, st.stats.WithCover, st.stats.WithLyric, st.stats.Playable).
			FontSize(fz(11.5)).TextColor(t.TextMuted)
	}
	if sc.LastError != "" {
		ui.Text(c, "上次刮削："+sc.LastError).FontSize(fz(11.5)).TextColor(t.TextMuted).MaxLines(2)
	}
}

func (a *App) startCloudScrape(st *cloudState, keys []string, all bool) {
	if st.scrape.Running {
		return
	}
	force := keys != nil
	go func() {
		started, err := a.API.CloudScrapeStart(keys, all, force)
		a.update(func() {
			if err != nil {
				st.authMsg = "刮削失败：" + err.Error()
				return
			}
			if started {
				st.scrape.Running = true
				a.pollCloudScrape(st)
			}
		})
	}()
}

func (a *App) pollCloudScrape(st *cloudState) {
	if st.scrapePolling {
		return
	}
	st.scrapePolling = true
	go func() {
		for {
			time.Sleep(800 * time.Millisecond)
			var sc backend.CloudScrapeState
			var err error
			a.withAPILimit(func() { sc, err = a.API.CloudScrapeStatus() })
			if err != nil {
				a.update(func() {
					st.scrapePolling = false
					st.authMsg = "查询刮削进度失败：" + err.Error()
				})
				return
			}
			a.update(func() { st.scrape = sc })
			if !sc.Running {
				a.update(func() {
					st.scrapePolling = false
					// 刮削会改写标题/封面/歌词，结束后重新拉一次库。
					st.loaded = false
					a.ensureCloud()
				})
				return
			}
		}
	}()
}

func (a *App) cloudList(c *ui.Context, st *cloudState) {
	t := c.Theme()
	st.list.Label = func(i int) string { return st.items[i].Title }

	lst := ui.List(c, &st.list, len(st.items), func(i int) {
		it := st.items[i]
		row := ui.Row(c).FillWidth().Height(58).PaddingX(10).Gap(12).
			AlignItems(ui.Center).Radius(8).Transition(hoverFade)
		cur, _ := a.PL.Current()
		isCur := cur.CloudKey != "" && cur.CloudKey == it.Key
		playing := isCur && a.PL.Playing()
		row.Children(func() {
			ui.Box(c).Width(26).Center().Children(func() {
				if playing {
					ui.Icon(c, Icons["note"]).FontSize(fz(15)).TextColor(t.Accent).AlignSelf(ui.Center)
				} else {
					ui.Textf(c, "%02d", i+1).FontSize(fz(12)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				}
			})
			if art := a.Covers.Get(a.cloudCoverURL(it, 300), a.invalidate); art != nil {
				ui.Image(c, art).Size(44, 44).Fit(ui.Cover).Radius(6)
			} else {
				ui.Box(c).Size(44, 44).Radius(6).Background(t.SurfaceHover).Center().Children(func() {
					ui.Icon(c, Icons["note"]).FontSize(fz(18)).TextColor(t.TextMuted).AlignSelf(ui.Center)
				})
			}
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, cloudDisplayTitle(it)).FontSize(fz(14)).SingleLine().Ellipsis("…")
				sub := it.Artist
				if it.Album != "" {
					sub += " · " + it.Album
				}
				if sub == "" {
					sub = it.FileName
				}
				ui.Text(c, sub).FontSize(fz(12)).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			a.cloudBadge(c, t, it)
			dur := 0.0
			if it.Song != nil {
				dur = it.Song.Duration()
			}
			ui.Text(c, fmtTime(dur)).FontSize(fz(12)).TextColor(t.TextMuted).
				Width(44).TextAlign(ui.End)
		})
		if row.Hovered() && !isCur {
			row.Background(t.SurfaceHover)
		}
		if isCur {
			row.Background(a.rowTint(t))
		}
		if row.DoubleClicked() {
			if !it.Playable {
				c.Toast("这条网盘记录没有可用的 file_id")
				return
			}
			a.playCloudSong(st, it)
		}
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("播放").Chosen() {
				a.playCloudSong(st, it)
			}
			if it.Playable {
				song := cloudSong(it)
				if m.Item("下一首播放").Chosen() {
					a.PL.EnqueueNext(song)
					c.Toast("已插队：" + cloudDisplayTitle(it))
				}
				if m.Item("加入队列").Chosen() {
					a.PL.EnqueueMany([]player.Song{song})
					c.Toast("已加入队列")
				}
			}
			m.Separator()
			if m.Item("编辑曲目信息").Chosen() {
				st.edit = cloudEditState{
					open: true, key: it.Key, title: it.Title,
					artist: it.Artist, album: it.Album,
				}
			}
			if m.Item("重新刮削此条").Chosen() {
				a.startCloudScrape(st, []string{it.Key}, false)
			}
			if m.Item("导入歌词文件…").Chosen() {
				a.importCloudLyric(st, it)
			}
			if it.LyricImport && m.Item("恢复自动刮削的歌词").Chosen() {
				a.revertCloudLyric(st, it)
			}
			if m.Item("导入封面图片…").Chosen() {
				a.importCloudCover(st, it)
			}
		})
	}).Grow(1).FillWidth()

	if len(st.items) == 0 {
		lst.Children(func() {
			ui.Text(c, "网盘里还没有音乐文件").FontSize(fz(13)).TextColor(t.TextMuted)
		})
	}
}

func (a *App) cloudBadge(c *ui.Context, t *ui.Theme, it backend.CloudItem) {
	label := ""
	fg := t.TextMuted
	switch {
	case !it.Playable:
		label, fg = "不可播", ui.Hex("#e81123")
	case it.PlaybackState == "blocked":
		label, fg = "已阻止", ui.Hex("#e81123")
	case it.Matched == 2:
		label = "未匹配"
	case it.ScrapedAt > 0:
		label = "已刮削"
		fg = t.Accent
	}
	if label == "" {
		return
	}
	ui.Row(c).Padding(2, 8).Radius(999).Border(1, fg.Alpha(0.45)).
		Margin(0, 6, 0, 0).Children(func() {
		ui.Text(c, label).FontSize(fz(10.5)).FontWeight(700).TextColor(fg)
	})
}

// playCloudSong 用「网盘中所有可播条目」作为队列起播，保证下一首仍有歌。
func (a *App) playCloudSong(st *cloudState, it backend.CloudItem) {
	songs := make([]player.Song, 0, len(st.items))
	start := 0
	for _, item := range st.items {
		if !item.Playable {
			continue
		}
		if item.Key == it.Key {
			start = len(songs)
		}
		songs = append(songs, cloudSong(item))
	}
	if len(songs) == 0 {
		return
	}
	a.playListNow(songs, start)
}

// cloudSong 把网盘条目转换成播放器曲目。Mid 用 "cloud:"+key 保证队列内
// 唯一，且不会与 QQ 曲库 mid 撞车；取流时后端只看 CloudFileID。
func cloudSong(it backend.CloudItem) player.Song {
	out := player.Song{
		Mid:           "cloud:" + it.Key,
		Name:          it.Title,
		Title:         it.Title,
		Artists:       it.Artist,
		Album:         it.Album,
		AlbumMid:      it.AlbumMid,
		AlbumPmid:     it.AlbumPmid,
		CloudFileID:   it.FileID,
		CloudKey:      it.Key,
		CloudName:     it.FileName,
		CloudHasCover: it.HasCover,
	}
	if it.Song != nil {
		out.Interval = it.Song.Duration()
		out.SongID = songIDOf(*it.Song)
		out.SongType = int64(it.Song.Type)
		for _, g := range it.Song.Singer {
			out.Singers = append(out.Singers, player.Singer{ID: g.ID, Mid: g.Mid, Name: g.Name})
		}
		if out.Artists == "" {
			out.Artists = it.Song.Artists()
		}
		if out.Album == "" {
			out.Album = it.Song.AlbumName()
		}
		if out.AlbumMid == "" && it.Song.Album != nil {
			out.AlbumMid = it.Song.Album.Mid
			out.AlbumPmid = it.Song.Album.Pmid
		}
	}
	if out.Title == "" {
		out.Title = it.FileName
	}
	if out.Artists == "" {
		out.Artists = "未知歌手"
	}
	return out
}

func songIDOf(s backend.Song) int64 {
	if s.ID != "" {
		if v, err := s.ID.Int64(); err == nil {
			return v
		}
	}
	return 0
}

func cloudDisplayTitle(it backend.CloudItem) string {
	if it.Title != "" {
		return it.Title
	}
	return it.FileName
}

// cloudCoverURL 优先用后端刮削/导入的封面，没有时回退曲库专辑封面。
func (a *App) cloudCoverURL(it backend.CloudItem, size int) string {
	if it.HasCover && it.Key != "" && a.API != nil {
		return a.API.CloudCoverURL(it.Key)
	}
	return coverURL(it.AlbumPmid, it.AlbumMid, size)
}

// cloudEditDialog 手动修正曲名/歌手/专辑。后端会把这条记录标记为已编辑，
// 后续自动刮削不再覆盖。
func (a *App) cloudEditDialog(c *ui.Context, st *cloudState) {
	ed := &st.edit
	if !ed.open {
		return
	}
	t := c.Theme()
	ui.DialogBase(c, &ed.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Width(420).Padding(18).Gap(12).Background(t.Surface).Radius(14).
			Border(1, t.Border).Shadow(0, 12, 36, 0, ui.RGBA(0, 0, 0, 0.28))
		panel.Children(func() {
			ui.Text(c, "编辑曲目信息").FontSize(fz(15)).FontWeight(800)
			ui.TextInput(c, &ed.title).Label("曲名").Placeholder("曲名").FillWidth()
			ui.TextInput(c, &ed.artist).Label("歌手").Placeholder("歌手").FillWidth()
			ui.TextInput(c, &ed.album).Label("专辑").Placeholder("专辑").FillWidth()
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				plainBtn(c, "取消", func() { ed.open = false })
				primaryBtn(c, "保存", func() {
					key, title, artist, album := ed.key, ed.title, ed.artist, ed.album
					ed.open = false
					go func() {
						err := a.API.CloudMetaSet(key, title, artist, album)
						a.update(func() {
							if err != nil {
								st.authMsg = "保存失败：" + err.Error()
								return
							}
							st.loaded = false
							a.ensureCloud()
						})
					}()
				})
			})
		})
	})
}

// importCloudLyric 选一个本地歌词文件导入（明文 LRC/QRC/TTML）。
func (a *App) importCloudLyric(st *cloudState, it backend.CloudItem) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "选择歌词文件",
			Filters: []mygo.FileFilter{{Name: "歌词", Extensions: []string{"lrc", "qrc", "ttml", "txt"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err == nil && len(data) > 512<<10 {
			err = errCloudLyricTooLarge
		}
		if err == nil {
			_, err = a.API.CloudLyricImport(it.Key, filepath.Base(paths[0]), data)
		}
		a.update(func() {
			if err != nil {
				st.authMsg = "导入歌词失败：" + err.Error()
				return
			}
			st.authMsg = "歌词已导入"
			st.loaded = false
			a.ensureCloud()
		})
	}()
}

func (a *App) revertCloudLyric(st *cloudState, it backend.CloudItem) {
	go func() {
		err := a.API.CloudLyricRevert(it.Key)
		a.update(func() {
			if err != nil {
				st.authMsg = "恢复歌词失败：" + err.Error()
				return
			}
			st.authMsg = "已恢复自动刮削歌词"
			st.loaded = false
			a.ensureCloud()
		})
	}()
}

// importCloudCover 选一张本地图片作为自定义封面。
func (a *App) importCloudCover(st *cloudState, it backend.CloudItem) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "选择封面图片",
			Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"jpg", "jpeg", "png", "gif", "webp"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err == nil && len(data) > 8<<20 {
			err = errCloudCoverTooLarge
		}
		if err == nil {
			err = a.API.CloudCoverImport(it.Key, filepath.Base(paths[0]), data)
		}
		a.update(func() {
			if err != nil {
				st.authMsg = "导入封面失败：" + err.Error()
				return
			}
			st.authMsg = "封面已导入"
			st.loaded = false
			a.ensureCloud()
		})
	}()
}

var (
	errCloudLyricTooLarge = uiError("歌词文件超过 512 KiB")
	errCloudCoverTooLarge = uiError("封面文件超过 8 MiB")
)

// uiError 是给本地校验用的轻量错误（后端限制的同一口径）。
type uiError string

func (e uiError) Error() string { return string(e) }
