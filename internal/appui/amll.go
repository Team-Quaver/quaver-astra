package appui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo"
)

const (
	// amllAutoKey enables automatic matching against AMLL TTML DB by the
	// current QQ Music song mid. The matched file is cached locally.
	amllAutoKey = "Lyrics.AMLLAuto"
	// amllBaseKey overrides the TTML DB base URL, as a mirror or private
	// deployment might.
	amllBaseKey = "Lyrics.AMLLBaseURL"
	// lyricStageKey chooses the now-playing layout: classic or folia.
	lyricStageKey = "Lyrics.Stage"
)

const defaultAMLLBase = "https://raw.githubusercontent.com/amll-dev/amll-ttml-db/refs/heads/main"

// amllEntry is the per-song TTML cache state. A missing online file is a
// successful "none" result so every repaint does not retry a 404.
type amllEntry struct {
	state  string // loading|ok|none|error
	source string // network|file|cache
	err    string
	lines  []player.TTMLLine
	at     time.Time
}

func (a *App) amllEnabled() bool {
	return a.Conf == nil || a.Conf.Bool(amllAutoKey, true)
}

func (a *App) lyricStage() string {
	if a.Conf == nil {
		return "classic"
	}
	if a.Conf.String(lyricStageKey, "classic") == "folia" {
		return "folia"
	}
	return "classic"
}

func (a *App) setLyricStage(stage string) {
	if stage != "folia" {
		stage = "classic"
	}
	if a.Conf != nil {
		a.Conf.Set(lyricStageKey, stage)
	}
	a.invalidate()
}

// ensureAMLL starts at most one TTML lookup per song. It runs only after the
// application window exists, which also keeps direct-construction UI tests
// from making network requests.
func (a *App) ensureAMLL() {
	if !a.amllEnabled() || a.Win == nil || a.Conf == nil {
		return
	}
	cur, ok := a.PL.Current()
	if !ok || strings.TrimSpace(cur.Mid) == "" {
		return
	}
	mid := cur.Mid
	a.amllMu.Lock()
	if a.amll == nil {
		a.amll = make(map[string]amllEntry)
	}
	if entry, exists := a.amll[mid]; exists {
		// A transient network failure should not poison this song until
		// restart. Successful/none results stay cached.
		if entry.state != "error" || time.Since(entry.at) < 2*time.Minute {
			a.amllMu.Unlock()
			return
		}
	}
	a.amll[mid] = amllEntry{state: "loading", at: time.Now()}
	a.amllMu.Unlock()

	go func() {
		lines, source, err := a.loadAMLL(mid)
		entry := amllEntry{source: source, lines: lines, at: time.Now()}
		switch {
		case err == nil && len(lines) > 0:
			entry.state = "ok"
		case errors.Is(err, errAMLLNotFound):
			entry.state = "none"
		default:
			entry.state = "error"
			if err != nil {
				entry.err = err.Error()
			} else {
				entry.err = "TTML 没有可用歌词行"
			}
		}
		a.amllMu.Lock()
		if a.amll == nil {
			a.amll = make(map[string]amllEntry)
		}
		a.amll[mid] = entry
		a.amllMu.Unlock()
		a.invalidate()
	}()
}

func (a *App) amllFor(mid string) amllEntry {
	a.amllMu.Lock()
	defer a.amllMu.Unlock()
	return a.amll[mid]
}

func (a *App) setAMLL(mid, source string, lines []player.TTMLLine, err error) {
	if mid == "" {
		return
	}
	entry := amllEntry{source: source, lines: lines, at: time.Now()}
	switch {
	case err == nil && len(lines) > 0:
		entry.state = "ok"
	case errors.Is(err, errAMLLNotFound):
		entry.state = "none"
	default:
		entry.state = "error"
		if err != nil {
			entry.err = err.Error()
		}
	}
	a.amllMu.Lock()
	if a.amll == nil {
		a.amll = make(map[string]amllEntry)
	}
	a.amll[mid] = entry
	a.amllMu.Unlock()
	a.invalidate()
}

var errAMLLNotFound = errors.New("AMLL TTML not found")

// loadAMLL reads the local cache first, then tries the configured AMLL TTML
// DB base URL and the public jsDelivr mirror.
func (a *App) loadAMLL(mid string) ([]player.TTMLLine, string, error) {
	if path := a.amllCachePath(mid); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if lines := player.ParseTTML(string(b)); len(lines) > 0 {
				return lines, "cache", nil
			}
			_ = os.Remove(path) // stale or corrupt cache
		}
	}

	bases := a.amllBases()
	var lastErr error
	notFound := false
	for _, base := range bases {
		src, err := fetchAMLLTTML(base, mid)
		if err != nil {
			notFound = notFound || errors.Is(err, errAMLLNotFound)
			lastErr = err
			continue
		}
		lines := player.ParseTTML(src)
		if len(lines) == 0 {
			lastErr = fmt.Errorf("AMLL TTML 无法解析")
			continue
		}
		a.cacheAMLL(mid, src)
		return lines, "network", nil
	}
	if lastErr == nil {
		lastErr = errAMLLNotFound
	}
	if notFound {
		lastErr = errAMLLNotFound
	}
	return nil, "", lastErr
}

func (a *App) amllBases() []string {
	if a.Conf != nil {
		if custom := strings.TrimSpace(a.Conf.String(amllBaseKey, "")); custom != "" {
			return []string{strings.TrimRight(custom, "/")}
		}
	}
	if custom := strings.TrimSpace(os.Getenv("QAA_AMLL_DB_BASE")); custom != "" {
		return []string{strings.TrimRight(custom, "/")}
	}
	return []string{
		defaultAMLLBase,
		"https://cdn.jsdelivr.net/gh/amll-dev/amll-ttml-db@main",
	}
}

// fetchAMLLTTML matches by QQ Music mid: amll-ttml-db keeps the same
// qq-lyrics/<mid>.ttml layout as the repository's other platform folders.
func fetchAMLLTTML(base, mid string) (string, error) {
	endpoint := strings.TrimRight(base, "/") + "/qq-lyrics/" + url.PathEscape(mid) + ".ttml"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Quaver-Astra/AMLL")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", errAMLLNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AMLL TTML HTTP %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a *App) amllCachePath(mid string) string {
	if a.Conf == nil || a.Conf.Dir() == "" || mid == "" {
		return ""
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, mid)
	if name == "" {
		return ""
	}
	return filepath.Join(a.Conf.Dir(), "cache", "lyrics", "amll", name+".ttml")
}

func (a *App) cacheAMLL(mid, src string) {
	path := a.amllCachePath(mid)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(src), 0o600)
}

// pickAMLLFile lets the user override automatic matching for the current
// song with a local TTML file (for example an edited or unreleased lyric).
func (a *App) pickAMLLFile() {
	cur, ok := a.PL.Current()
	if !ok {
		return
	}
	mid := cur.Mid
	parent := a.Win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      parent,
			Title:       "载入 AMLL TTML 歌词",
			ButtonLabel: "载入",
			Filters:     []mygo.FileFilter{{Name: "TTML 歌词", Extensions: []string{"ttml", "xml"}}},
		})
		if err != nil {
			a.setAMLL(mid, "file", nil, err)
			return
		}
		if len(paths) == 0 {
			return
		}
		b, err := os.ReadFile(paths[0])
		if err != nil {
			a.setAMLL(mid, "file", nil, err)
			return
		}
		lines := player.ParseTTML(string(b))
		if len(lines) == 0 {
			a.setAMLL(mid, "file", nil, fmt.Errorf("文件中没有可用的 TTML 歌词行"))
			return
		}
		a.cacheAMLL(mid, string(b))
		a.setAMLL(mid, "file", lines, nil)
	}()
}
