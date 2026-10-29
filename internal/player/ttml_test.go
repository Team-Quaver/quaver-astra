package player

import (
	"math"
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
