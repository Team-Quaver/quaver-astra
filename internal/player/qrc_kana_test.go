package player

import "testing"

func TestQRCKanaRuby(t *testing.T) {
	src := "[0,3000]君(0,1000)の(1000,1000)名(2000,1000)\n[kana:1きみ1な]"
	lines := ParseQRC(src)
	if len(lines) != 1 || len(lines[0].Words) != 3 {
		t.Fatalf("QRC 前置解析失败：%+v", lines)
	}
	words := lines[0].Words
	if len(words[0].Ruby) != 1 || words[0].Ruby[0].Text != "きみ" {
		t.Fatalf("君 的振假名错误：%+v", words[0].Ruby)
	}
	if words[0].Ruby[0].Time != 0 || words[0].Ruby[0].Dur != 1 {
		t.Errorf("君 的注音时间轴错误：%+v", words[0].Ruby[0])
	}
	if len(words[2].Ruby) != 1 || words[2].Ruby[0].Text != "な" {
		t.Fatalf("名 的振假名错误：%+v", words[2].Ruby)
	}
}

func TestQRCKanaSpansAndCaseInsensitiveTag(t *testing.T) {
	tag := "[KANA:1か(100,200)な(300,200)]"
	units := ParseKanaUnits(tag)
	if len(units) != 1 || units[0].KanaText != "かな" || len(units[0].Spans) != 2 {
		t.Fatalf("注音单元解析错误：%+v", units)
	}
	if units[0].Spans[0].Text != "か" || units[0].Spans[0].Time != 0.1 || units[0].Spans[0].Dur != 0.2 {
		t.Errorf("第一片注音时间轴错误：%+v", units[0].Spans[0])
	}
	if units[0].Spans[1].Text != "な" || units[0].Spans[1].Time != 0.3 {
		t.Errorf("第二片注音时间轴错误：%+v", units[0].Spans[1])
	}
	if got := extractKanaTag("x\n" + tag + "\n"); got != tag {
		t.Errorf("extractKanaTag=%q，期望 %q", got, tag)
	}
}

func TestQRCKanaDigitRunCountsOnce(t *testing.T) {
	lines := ParseQRC("[0,2000]22(0,1000)漢(1000,1000)\n[kana:1にじゅうに1かん]")
	if len(lines) != 1 || len(lines[0].Words) < 2 {
		t.Fatalf("解析失败：%+v", lines)
	}
	if got := lines[0].Words[0].Ruby; len(got) != 1 || got[0].Text != "にじゅうに" {
		t.Errorf("连续数字串应按一个基字符位：%+v", got)
	}
}
