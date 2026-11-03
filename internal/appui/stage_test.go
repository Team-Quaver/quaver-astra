package appui

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// TestClassicQRCKeepsWordSpaces 守住英文歌词的词间空格：逐字歌词里的
// 空格是独立 QRC 词，整句必须走同一份文本排版，不能按词拆成 Row 后把
// 空白词丢掉。
func TestClassicQRCKeepsWordSpaces(t *testing.T) {
	app, _ := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	words := []player.QrcWord{
		{Time: 0, Dur: 0.9, Text: "Hayato"},
		{Time: 0.9, Dur: 0, Text: " "},
		{Time: 0.9, Dur: 2.1, Text: "Yamamoto"},
	}
	spans, starts := app.qrcWordSpans(words, []float32{1, 1, 0}, 20, karaokeColor)
	if len(spans) != 3 || len(starts) != 2 {
		t.Fatalf("英文空格词被丢弃：spans=%d starts=%d", len(spans), len(starts))
	}
	var got strings.Builder
	for _, span := range spans {
		got.WriteString(span.Text)
	}
	if got.String() != "Hayato Yamamoto" {
		t.Errorf("整句排版文本 = %q，期望保留词间空格", got.String())
	}
}

// TestQRCKanaRubyIsNotDuplicated 守住日语重复字：ruby 词的正文只在整句
// 排版里出现一次，注音作为同行的辅助文本，不额外叠一层正文。
func TestQRCKanaRubyIsNotDuplicated(t *testing.T) {
	app, _ := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	ql := player.ParseQRC("[0,2000]震(0,1000)えて(1000,1000)\n[kana:1ふる]")[0]
	tester := ui.NewTester(func(c *ui.Context) {
		app.karaokeRubyLine(c, ql, true, 20, 300, ui.Start, karaokeColor, false)
	}, 400, 200)
	tester.Frame()

	texts := tester.Texts()
	if len(texts) != 1 {
		t.Fatalf("ruby 行应只有一个文本元素，实测 %v", texts)
	}
	if texts[0] != "震えて ふる" {
		t.Errorf("ruby 行标签 = %q，期望正文与注音各出现一次", texts[0])
	}
}

// TestQRCKanaRubyBandHugsLyric 守住注音与正文的垂直间距：ruby 行只额外
// 占一行小字的高度，不能按正文行高再叠一层（否则注音和歌词之间会出现
// 一大片空白）。同一字号下 ruby 行高 = 正文行高 + 小字 ascent+descent。
func TestQRCKanaRubyBandHugsLyric(t *testing.T) {
	app, _ := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	ql := player.ParseQRC("[0,2000]震(0,1000)えて(1000,1000)\n[kana:1ふる]")[0]
	const size = 20
	tester := ui.NewTester(func(c *ui.Context) {
		app.karaokeRubyLine(c, ql, true, size, 300, ui.Start, karaokeColor, false)
	}, 400, 200)
	tester.Frame()

	r, ok := tester.Find("震えて ふる")
	if !ok {
		t.Fatalf("找不到 ruby 行；当前文本：%v", tester.Texts())
	}
	_, baseH := ui.ShapeText("震えて", ui.Font{Family: app.lyricFontFamily(), Size: fz(size), Weight: 800}, 300).Size()
	rm := ui.Font{Family: app.lyricFontFamily(), Size: fz(size * 0.45), Weight: 800}.Metrics()
	want := baseH + rm.Ascent + rm.Descent
	if math.Abs(float64(r.H-want)) > 1.5 {
		t.Errorf("ruby 行高 = %.1f，期望 %.1f（正文 %.1f + 注音 %.1f）——注音与正文间距过大",
			r.H, want, baseH, rm.Ascent+rm.Descent)
	}
	if r.H >= baseH*2 {
		t.Errorf("ruby 行高 = %.1f ≥ 正文两倍 %.1f：注音又占了一整行高", r.H, baseH*2)
	}
}
