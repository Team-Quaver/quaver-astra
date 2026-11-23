package appui

import (
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	searchHintDelay = 180 * time.Millisecond
	searchHintLimit = 8
)

// searchHintState 是搜索框的补全建议。输入停顿一小会儿才请求，避免每个
// 按键都打一次上游；seq 让晚到的旧响应失效。
type searchHintState struct {
	kw      string
	items   []string
	seq     uint64
	loading bool
	done    bool
	due     time.Time
	active  int // 当前高亮项，-1 表示没有
}

func (a *App) syncSearchHints(c *ui.Context) {
	h := &a.searchHint
	kw := strings.TrimSpace(a.searchDraft)
	if kw != h.kw {
		h.kw = kw
		h.items = nil
		h.loading = false
		h.done = false
		h.due = time.Time{}
		h.active = -1
		h.seq++
	}
	if kw == "" || h.loading || h.done {
		return
	}
	if h.due.IsZero() {
		h.due = c.Now().Add(searchHintDelay)
		c.After(searchHintDelay)
		return
	}
	if c.Now().Before(h.due) {
		return
	}
	h.due = time.Time{}
	a.fetchSearchHints(kw)
}

func (a *App) fetchSearchHints(kw string) {
	h := &a.searchHint
	h.seq++
	seq := h.seq
	if a.API == nil {
		h.done = true
		return
	}
	h.loading = true
	go func() {
		var items []string
		var err error
		a.withAPILimit(func() { items, err = a.API.SearchComplete(kw) })
		a.update(func() {
			if seq != h.seq || h.kw != kw {
				return
			}
			h.loading = false
			h.done = true
			if err == nil {
				h.items = items
			}
			a.clampSearchHint()
		})
	}()
}

func (a *App) clampSearchHint() {
	h := &a.searchHint
	n := min(len(h.items), searchHintLimit)
	if h.active >= n {
		h.active = n - 1
	}
}

// visibleSearchHints 只返回与当前输入匹配的那一组建议，并把高亮收进
// 可见范围。输入框里的文字先于响应变化，旧建议不能挂在新关键词下面。
func (a *App) visibleSearchHints() []string {
	h := &a.searchHint
	if h.kw != strings.TrimSpace(a.searchDraft) {
		return nil
	}
	n := min(len(h.items), searchHintLimit)
	a.clampSearchHint()
	return h.items[:n]
}

// searchHintInput 处理输入框里的上下键与回车：上下移动建议高亮，回车搜索
// 高亮项，没有高亮时搜索输入框原文。
func (a *App) searchHintInput(sf ui.Element, items []string) {
	n := len(items)
	if sf.Shortcut(0, ui.KeyDown) && n > 0 {
		if a.searchHint.active < 0 {
			a.searchHint.active = 0
		} else {
			a.searchHint.active = min(a.searchHint.active+1, n-1)
		}
	}
	if sf.Shortcut(0, ui.KeyUp) && n > 0 {
		if a.searchHint.active < 0 {
			a.searchHint.active = n - 1
		} else {
			a.searchHint.active = max(a.searchHint.active-1, 0)
		}
	}
	if !sf.Submitted() {
		return
	}
	kw := strings.TrimSpace(a.searchDraft)
	if a.searchHint.active >= 0 && a.searchHint.active < n {
		kw = items[a.searchHint.active]
	}
	a.openSearch(kw)
}

func (a *App) openSearch(raw string) {
	kw := strings.TrimSpace(raw)
	if kw == "" {
		return
	}
	a.searchDraft = kw
	a.searchOpen = false
	a.searchHint.active = -1
	a.Router.Push("/search?kw=" + url.QueryEscape(kw))
}

// searchHintList 在当前输入框下方列出一组建议。
func (a *App) searchHintList(c *ui.Context, items []string) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(1).Padding(6, 0, 0, 0).Children(func() {
		for i, hint := range items {
			active := i == a.searchHint.active
			row := ui.ButtonBase(c).FillWidth().Height(32).Padding(2, 8).Radius(7).
				AlignItems(ui.Center).Justify(ui.Start).Gap(8)
			row.Children(func() {
				col := t.TextMuted
				if active {
					col = t.Accent
				}
				ui.Icon(c, Icons["search"]).FontSize(fz(12.5)).TextColor(col)
				ui.Text(c, hint).FontSize(fz(13)).TextColor(col).SingleLine().Ellipsis("…")
			})
			switch {
			case active:
				row.Background(t.Accent.Alpha(0.13))
			case row.Hovered():
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() {
				a.openSearch(hint)
			}
		}
	})
}
