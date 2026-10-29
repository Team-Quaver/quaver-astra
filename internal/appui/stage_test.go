package appui

import (
	"image/color"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"
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
