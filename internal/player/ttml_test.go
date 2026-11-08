package player

import (
	"math"
	"testing"
)

const testTTML = `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml"
    xmlns:ttm="http://www.w3.org/ns/ttml#metadata"
    xmlns:tts="http://www.w3.org/ns/ttml#styling">
  <body>
    <div>
      <p begin="00:00:01.000" end="00:00:03.000">
        <span begin="00:00:01.000" end="00:00:01.500">Hel</span><span begin="00:00:01.500" end="00:00:02.000">lo</span> <span begin="00:00:02.000" end="00:00:03.000">world</span>
        <span ttm:role="x-translation">你好世界</span>
        <span ttm:role="x-romanization">haro</span>
      </p>
      <p begin="00:00:03.000" dur="00:00:02.000">第二行</p>
    </div>
  </body>
</tt>`

func TestParseTTMLWordsAndAux(t *testing.T) {
	lines := ParseTTML(testTTML)
	if len(lines) != 2 {
		t.Fatalf("TTML 行数 = %d，期望 2", len(lines))
	}
	first := lines[0]
	if first.Time != 1 || math.Abs(first.Dur-2) > 1e-9 {
		t.Fatalf("首行时间 = %v/%v，期望 1/2", first.Time, first.Dur)
	}
	if first.Text != "Hello world" {
		t.Fatalf("首行正文 = %q，期望 %q", first.Text, "Hello world")
	}
	if first.Trans != "你好世界" || first.Roma != "haro" {
		t.Fatalf("辅助行 = %q / %q", first.Trans, first.Roma)
	}
	if len(first.Words) != 3 {
		t.Fatalf("首行词数 = %d，期望 3", len(first.Words))
	}
	want := []QrcWord{
		{Time: 1, Dur: 0.5, Text: "Hel"},
		{Time: 1.5, Dur: 0.5, Text: "lo"},
		{Time: 2, Dur: 1, Text: "world"},
	}
	for i, w := range want {
		got := first.Words[i]
		if got.Time != w.Time || got.Dur != w.Dur || got.Text != w.Text {
			t.Fatalf("第 %d 个词 = %+v，期望 %+v", i, got, w)
		}
	}
	if !HasWordTiming(lines) {
		t.Fatal("TTML 逐字时间轴没有被识别")
	}
	if second := lines[1]; second.Text != "第二行" || len(second.Words) != 0 || second.Dur != 2 {
		t.Fatalf("第二行 = %+v", second)
	}
}

func TestParseTTMLMissingEndUsesNextWord(t *testing.T) {
	src := `<tt xmlns="http://www.w3.org/ns/ttml"><body><div>
<p begin="1s" end="3s"><span begin="1s">A</span><span begin="2s">B</span></p>
</div></body></tt>`
	lines := ParseTTML(src)
	if len(lines) != 1 || len(lines[0].Words) != 2 {
		t.Fatalf("解析结果 = %+v", lines)
	}
	if got := lines[0].Words[0]; got.Time != 1 || got.Dur != 1 {
		t.Fatalf("第一个词 = %+v，期望 1s 起、1s 长", got)
	}
	if got := lines[0].Words[1]; got.Time != 2 || got.Dur != 1 {
		t.Fatalf("第二个词 = %+v，期望 2s 起、1s 长", got)
	}
}

func TestParseTTMLRelativeWordTimes(t *testing.T) {
	src := `<tt xmlns="http://www.w3.org/ns/ttml"><body><div>
<p begin="10s" end="12s"><span begin="0s" end="1s">逐</span><span begin="1s" end="2s">字</span></p>
</div></body></tt>`
	lines := ParseTTML(src)
	if len(lines) != 1 || len(lines[0].Words) != 2 {
		t.Fatalf("解析结果 = %+v", lines)
	}
	if got := lines[0].Words[0]; got.Time != 10 {
		t.Fatalf("相对时间没有补上行起始：%+v", got)
	}
	if got := lines[0].Words[1]; got.Time != 11 {
		t.Fatalf("第二个词时间 = %v，期望 11", got.Time)
	}
}

func TestParseTTMLTimeForms(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"1.5", 1.5},
		{"1.5s", 1.5},
		{"1500ms", 1.5},
		{"00:01:02.500", 62.5},
		{"00:00:01:15", 1.5},
	}
	for _, c := range cases {
		got, ok := parseTTMLTime(c.in)
		if !ok || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("parseTTMLTime(%q) = %v,%v，期望 %v", c.in, got, ok, c.want)
		}
	}
	if _, ok := parseTTMLTime("not-a-time"); ok {
		t.Error("非法时间被接受了")
	}
}

type ttmlBackend struct{ stubBackend }

func (ttmlBackend) FetchLyric(string, bool, bool) (string, string, string, error) {
	return testTTML, "", "", nil
}

func TestFetchLyricUsesTTML(t *testing.T) {
	p := New(ttmlBackend{}, &fakeEngine{}, fakePrefs{})
	p.fetchLyric(Song{Mid: "x"}, p.playSeq)

	lines, state := p.LyricLines()
	if state != "ok" || len(lines) != 2 {
		t.Fatalf("歌词状态 = %q，行数 = %d", state, len(lines))
	}
	ql := p.QrcLines()
	if len(ql) != 2 || len(ql[0].Words) != 3 {
		t.Fatalf("逐字时间轴 = %+v", ql)
	}
	if got := p.LyricFormat(); got != "ttml" {
		t.Fatalf("逐字歌词来源 = %q，期望 ttml", got)
	}
}
