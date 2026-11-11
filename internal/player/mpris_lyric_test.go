package player

import (
	"strings"
	"testing"
)

func TestLyricLinesToLRCAndText(t *testing.T) {
	lines := []LyricLine{
		{Time: 1, Text: " 第一行 ", Trans: "first", Roma: "ichi"},
		{Time: 59.999, Text: "第二行"},
		{Time: 60.5, Text: ""},
	}

	lrc := LyricLinesToLRC(lines)
	wantLRC := "[00:01.00]第一行\n[01:00.00]第二行"
	if lrc != wantLRC {
		t.Fatalf("LRC = %q，期望 %q", lrc, wantLRC)
	}

	plain := LyricLinesToText(lines)
	wantPlain := "第一行\n第二行"
	if plain != wantPlain {
		t.Fatalf("纯文本 = %q，期望 %q", plain, wantPlain)
	}
}

func TestMPRISLyricsFromQRCAndTTML(t *testing.T) {
	qrc := LyricLinesToLRC(qrcToLines(ParseQRC(qrcPlainSample)))
	wantQRC := "[00:01.79]那一年\n[00:05.05]作曲:"
	if qrc != wantQRC {
		t.Fatalf("QRC 转 LRC = %q，期望 %q", qrc, wantQRC)
	}

	ttml := LyricLinesToLRC(qrcToLines(ParseTTML(testTTML)))
	wantTTML := "[00:01.00]Hello world\n[00:03.00]第二行"
	if ttml != wantTTML {
		t.Fatalf("TTML 转 LRC = %q，期望 %q", ttml, wantTTML)
	}

	if got := LyricLinesToText(qrcToLines(ParseTTML(testTTML))); got != "Hello world\n第二行" {
		t.Fatalf("TTML 转纯文本 = %q", got)
	}
}

func TestMPRISLyricsFollowMode(t *testing.T) {
	prefs := newMapPrefs()
	p := New(ttmlBackend{}, &fakeEngine{}, prefs)
	p.fetchLyric(Song{Mid: "x"}, p.playSeq)

	if got := p.LyricAsText(); !strings.HasPrefix(got, "[00:01.00]Hello world") {
		t.Fatalf("默认 asText 应为 LRC，实得 %q", got)
	}
	if !p.MprisLrc() {
		t.Fatal("默认 MprisLrc 应为 true")
	}

	p.SetMprisLrc(false)
	if got := p.LyricAsText(); got != "Hello world\n第二行" {
		t.Fatalf("纯文本模式 asText = %q", got)
	}
	if p.MprisLrc() {
		t.Fatal("切到纯文本后 MprisLrc 应为 false")
	}
	if got := prefs.Bool("Lyrics.MprisLrc", true); got {
		t.Fatal("Lyrics.MprisLrc 没有落盘为 false")
	}

	p.SetMprisLrc(true)
	if got := p.LyricAsText(); !strings.HasPrefix(got, "[00:01.00]Hello world") {
		t.Fatalf("切回 LRC 后 asText = %q", got)
	}
}

func TestMPRISLyricsReadPreference(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set("Lyrics.MprisLrc", false)
	p := New(ttmlBackend{}, &fakeEngine{}, prefs)
	p.fetchLyric(Song{Mid: "x"}, p.playSeq)
	if got := p.LyricAsText(); got != "Hello world\n第二行" {
		t.Fatalf("启动时纯文本偏好未生效，asText = %q", got)
	}
}

func TestMPRISLyricsReadLegacyStringPreference(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set("Lyrics.MprisLrc", "False")
	p := New(ttmlBackend{}, &fakeEngine{}, prefs)
	p.fetchLyric(Song{Mid: "x"}, p.playSeq)
	if got := p.LyricAsText(); got != "Hello world\n第二行" {
		t.Fatalf("旧式字符串偏好未生效，asText = %q", got)
	}
}
