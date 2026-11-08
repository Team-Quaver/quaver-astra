package player

import (
	"testing"
	"time"
)

type mutableLyricBackend struct {
	stubBackend
	lyric string
}

func (b *mutableLyricBackend) FetchLyric(string, bool, bool) (string, string, string, error) {
	return b.lyric, "", "", nil
}

// TestReloadLyricReplacesCurrent 守住网盘导入歌词后的即时刷新：不换歌也
// 能把 Player 里缓存的旧歌词换掉。
func TestReloadLyricReplacesCurrent(t *testing.T) {
	be := &mutableLyricBackend{lyric: "[00:01.00]旧歌词"}
	p := New(be, &fakeEngine{}, fakePrefs{})
	p.queue = []Song{{Mid: "x"}}
	p.index = 0
	p.fetchLyric(Song{Mid: "x"}, p.playSeq)
	lines, state := p.LyricLines()
	if state != "ok" || len(lines) != 1 || lines[0].Text != "旧歌词" {
		t.Fatalf("初始歌词 = %+v / %q", lines, state)
	}

	be.lyric = "[00:01.00]新歌词"
	p.ReloadLyric()
	deadline := time.Now().Add(3 * time.Second)
	for {
		lines, state = p.LyricLines()
		if state == "ok" && len(lines) == 1 && lines[0].Text == "新歌词" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("重新拉取后歌词 = %+v / %q", lines, state)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
