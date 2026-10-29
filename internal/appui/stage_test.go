package appui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

const testTTML = `<tt xmlns="http://www.w3.org/ns/ttml"
	xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
	<body><div>
		<p begin="0.000" end="3.000" ttm:agent="v1">
			<span begin="0.000" end="0.700">夜</span>
			<span begin="0.700" end="1.400">に</span>
			<span begin="1.400" end="3.000">駆ける</span>
			<span ttm:role="x-translation">奔向黑夜</span>
			<span ttm:role="x-bg" begin="1.500" end="2.600">
				<span begin="1.500" end="2.000">(run)</span>
				<span begin="2.000" end="2.600">(away)</span>
			</span>
		</p>
		<p begin="3.100" end="6.000" ttm:agent="v2">
			<span begin="3.100" end="4.000">君</span>
			<span begin="4.000" end="6.000">の声</span>
		</p>
	</div></body></tt>`

func TestFoliaStageRendersAMLLWordsBackgroundAndDuet(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	lines := player.ParseTTML(testTTML)
	if len(lines) != 2 {
		t.Fatalf("test TTML parsed %d lines", len(lines))
	}
	app.setAMLL("m1", "file", lines, nil)
	app.PL.SetLyricAuxMode("translation")
	app.setLyricStage("folia")
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	for _, want := range []string{"夜", "に", "駆ける", "奔向黑夜", "(run)", "(away)", "君", "の声", "AMLL TTML"} {
		if !tester.HasText(want) {
			t.Errorf("Folia/AMLL stage is missing %q; texts=%v", want, tester.Texts())
		}
	}
	if _, ok := tester.Find("切换正在播放布局"); !ok {
		t.Errorf("stage switch missing from now-playing chrome")
	}
}

func TestAMLLImprovesClassicLyricColumn(t *testing.T) {
	app, tester := npTestApp(t, color.RGBA{R: 96, G: 148, B: 210, A: 255})
	app.setAMLL("m1", "file", player.ParseTTML(testTTML), nil)
	app.PL.SetLyricAuxMode("translation")
	tester.Advance(450 * time.Millisecond)
	tester.Frame()

	if !tester.HasText("歌词源 · AMLL · 本地") {
		t.Errorf("classic layout does not expose AMLL source; texts=%v", tester.Texts())
	}
	for _, want := range []string{"夜", "に", "駆ける", "奔向黑夜", "(run)"} {
		if !tester.HasText(want) {
			t.Errorf("classic AMLL lyric is missing %q; texts=%v", want, tester.Texts())
		}
	}
}

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
