package player

import (
	"math"
	"strings"
	"testing"
)

func TestParseTTMLWordsAgentsAndBackground(t *testing.T) {
	const src = `<tt xmlns="http://www.w3.org/ns/ttml"
		xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
	<body><div>
		<p begin="00:01.500" end="00:05.500" ttm:agent="v1">
			<span begin="00:01.500" end="00:02.000">Hello</span>
			<span> </span>
			<span begin="00:02.000" end="00:02.750">world</span>
			<span ttm:role="x-translation">你好世界</span>
			<span ttm:role="x-roman">hello world</span>
			<span ttm:role="x-bg" begin="00:03.000" end="00:04.000">
				<span begin="00:03.000" end="00:03.500">(hello)</span>
				<span begin="00:03.500" end="00:04.000">(world)</span>
			</span>
		</p>
		<p begin="00:06.000" end="00:07.250" ttm:agent="v2">
			<span begin="00:06.000" end="00:07.250">回答</span>
		</p>
	</div></body></tt>`

	lines := ParseTTML(src)
	if len(lines) != 2 {
		t.Fatalf("parsed %d lines, want 2: %#v", len(lines), lines)
	}
	line := lines[0]
	if line.Agent != "v1" || line.Text != "Hello world" {
		t.Fatalf("line identity = agent %q text %q", line.Agent, line.Text)
	}
	if line.Trans != "你好世界" || line.Roma != "hello world" {
		t.Fatalf("aux text = translation %q romanization %q", line.Trans, line.Roma)
	}
	if len(line.Words) != 3 {
		t.Fatalf("words = %#v, want two sung words and one spacer", line.Words)
	}
	if line.Words[0].Text != "Hello" || math.Abs(line.Words[0].Time-1.5) > 1e-6 ||
		math.Abs(line.Words[0].Dur-0.5) > 1e-6 {
		t.Fatalf("first word timing = %#v", line.Words[0])
	}
	if line.Words[1].Text != " " {
		t.Fatalf("mixed-content spacer lost: %#v", line.Words)
	}
	if len(line.BackgroundWords) != 2 ||
		line.BackgroundWords[1].Text != "(world)" ||
		math.Abs(line.BackgroundWords[1].Time-3.5) > 1e-6 {
		t.Fatalf("background words = %#v", line.BackgroundWords)
	}
	if lines[1].Agent != "v2" || lines[1].End() != 7.25 {
		t.Fatalf("duet line = %#v", lines[1])
	}
}

// TestParseTTMLKeepsInterSpanWordSpaces 守住 AMLL 的词间空格：AMLL TTML 把
// 空格写成 <span> 之间的独立文本节点（`</span> <span>`），word 时间轴必须
// 把这些节点留在 Words 里，否则逐字行会把英文单词连成一串。
func TestParseTTMLKeepsInterSpanWordSpaces(t *testing.T) {
	const src = `<tt xmlns="http://www.w3.org/ns/ttml"
		xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
	<body><div>
		<p begin="16.110" end="18.500" ttm:agent="v1"><span begin="16.110" end="16.450">Fi</span><span begin="16.450" end="16.780">nally</span> <span begin="16.780" end="17.070">we</span> <span begin="17.070" end="17.380">got</span> <span begin="17.380" end="17.570">to</span> <span begin="17.570" end="17.720">the</span> <span begin="17.720" end="17.920">en</span><span begin="17.920" end="18.050">d of</span> <span begin="18.050" end="18.240">the</span> <span begin="18.240" end="18.500">world</span></p>
	</div></body></tt>`

	lines := ParseTTML(src)
	if len(lines) != 1 {
		t.Fatalf("parsed %d lines: %#v", len(lines), lines)
	}
	var got strings.Builder
	for _, w := range lines[0].Words {
		got.WriteString(w.Text)
	}
	if want := "Finally we got to the end of the world"; got.String() != want {
		t.Fatalf("逐字文本 = %q，期望 %q", got.String(), want)
	}
	if lines[0].Text != "Finally we got to the end of the world" {
		t.Errorf("整行文本 = %q", lines[0].Text)
	}
	// 词间空格是零时长 spacer，时间落到整行起点，不参与逐字着色。
	for _, w := range lines[0].Words {
		if w.Text == " " && math.Abs(w.Time-16.110) > 1e-6 {
			t.Errorf("spacer 时间 = %v，期望行首 16.110", w.Time)
		}
	}
}

// TestParseTTMLKeepsTrailingSpaceInsideSpan 兼容把词间空格写在 span 内部的
// 来源（`<span>the </span>`），不能只在 span 之间的文本节点上保留空格。
func TestParseTTMLKeepsTrailingSpaceInsideSpan(t *testing.T) {
	const src = `<tt><body><div><p begin="1.0" end="3.0"><span begin="1.0" end="2.0">the </span><span begin="2.0" end="3.0">end</span></p></div></body></tt>`
	lines := ParseTTML(src)
	if len(lines) != 1 {
		t.Fatalf("parsed %d lines: %#v", len(lines), lines)
	}
	var got strings.Builder
	for _, w := range lines[0].Words {
		got.WriteString(w.Text)
	}
	if got.String() != "the end" {
		t.Errorf("逐字文本 = %q，期望保留 span 内的词间空格", got.String())
	}
}

// TestParseTTMLKeepsSpaceAfterFinalLyric 行末歌词与翻译 span 之间的空格是
// 排版缩进，不能变成行尾的空格块（它会撑宽逐字行）。
func TestParseTTMLKeepsSpaceAfterFinalLyric(t *testing.T) {
	const src = `<tt><body><div><p begin="1.0" end="3.0"><span begin="1.0" end="2.0">end</span> <span ttm:role="x-translation">结束</span></p></div></body></tt>`
	lines := ParseTTML(src)
	if len(lines) != 1 {
		t.Fatalf("parsed %d lines: %#v", len(lines), lines)
	}
	var got strings.Builder
	for _, w := range lines[0].Words {
		got.WriteString(w.Text)
	}
	if got.String() != "end" {
		t.Errorf("逐字文本 = %q，翻译前不应留下行尾空格", got.String())
	}
}

func TestParseTTMLInfersLineEndAndSorts(t *testing.T) {
	const src = `<tt><body><div>
		<p begin="5.0"><span begin="5.0" end="5.4">B</span></p>
		<p begin="2.0"><span begin="2.0" end="2.8">A</span></p>
		<p begin="8.0"><span begin="8.0" end="8.5">C</span></p>
	</div></body></tt>`
	lines := ParseTTML(src)
	if len(lines) != 3 {
		t.Fatalf("parsed %d lines: %#v", len(lines), lines)
	}
	if lines[0].Text != "A" || lines[1].Text != "B" || lines[2].Text != "C" {
		t.Fatalf("lines not sorted: %#v", lines)
	}
	if math.Abs(lines[0].Dur-3.0) > 1e-6 {
		t.Fatalf("missing end was not inferred from the next line: %#v", lines[0])
	}
}

func TestParseTTMLRejectsMalformedOrUntimed(t *testing.T) {
	if lines := ParseTTML(`<tt><body><div><p>no timing</p></div></body></tt>`); lines != nil {
		t.Fatalf("untimed document parsed as %#v", lines)
	}
	if lines := ParseTTML(`not xml`); lines != nil {
		t.Fatalf("malformed document parsed as %#v", lines)
	}
}
