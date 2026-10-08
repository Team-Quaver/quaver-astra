package player

import (
	"strings"
	"testing"
)

// 官方口径样例（百度百科 / 博客抓到的真实 QRC 行）：
// 行 [1790,2062] = 1790ms 起、持续 2062ms；词 (1790,375) = 「那」1790→2165。
const qrcPlainSample = `[ti:测试]
[ar:歌手]
[1790,2062]那(1790,375)一(2165,309)年 (2474,315)
[5052,3516]作(5052,252)曲(5304,248):(5552,252)
`

// 上游 qrc=1 返回的 XML 信封（换行写成实体 &#10;），与 AMLL 注释里
// 记录的形状一致。
const qrcXMLSample = `<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1">` +
	`<Lyric_1 LyricType="1" Lyrics="[ti:晴天]&#10;[ar:周杰伦]&#10;[1790,2062]那(1790,375)一(2165,309)年 (2474,315)&#10;[5052,3516]作(5052,252)曲(5304,248)" /></LyricInfo></QrcInfos>`

func TestParseQRCPlainText(t *testing.T) {
	lines := ParseQRC(qrcPlainSample)
	if len(lines) != 2 {
		t.Fatalf("应解析出 2 行（元信息行跳过），实测 %d：%+v", len(lines), lines)
	}
	l := lines[0]
	if l.Time != 1.79 || l.Dur != 2.062 {
		t.Errorf("行时间应为 1.79s/2.062s，实测 %v/%v", l.Time, l.Dur)
	}
	// 词时间是绝对毫秒，不叠加行起始
	if l.Words[0].Text != "那" || l.Words[0].Time != 1.79 || l.Words[0].Dur != 0.375 {
		t.Errorf("首词应为 那@1.79+0.375，实测 %+v", l.Words[0])
	}
	if l.Words[1].Time != 2.165 {
		t.Errorf("第二词起始应为 2.165s（绝对），实测 %v", l.Words[1].Time)
	}
	if l.Text != "那一年 " && l.Text != "那一年" {
		t.Errorf("去时间标签后的正文异常: %q", l.Text)
	}
	if !HasWordTiming(lines) {
		t.Error("这份样例带词级时间轴，HasWordTiming 应为 true")
	}
}

func TestParseQRCXMLEnvelope(t *testing.T) {
	lines := ParseQRC(qrcXMLSample)
	if len(lines) != 2 {
		t.Fatalf("XML 信封应解析出 2 行，实测 %d：%+v", len(lines), lines)
	}
	// &#10; 要还原成换行、&#10; 之外的实体也要还原
	if lines[0].Text != "那一年 " && lines[0].Text != "那一年" {
		t.Errorf("信封正文抽取/实体还原异常: %q", lines[0].Text)
	}
	if lines[0].Words[0].Time != 1.79 {
		t.Errorf("信封解析出的词时间异常: %+v", lines[0].Words[0])
	}
}

// 行只有行级时间、词时长全是 0 的「假 QRC」不该被当成逐字歌词。
func TestHasWordTimingRejectsLineOnly(t *testing.T) {
	lines := ParseQRC("[0,1000]一(0,0)二(0,0)\n[1000,900]三(0,0)")
	if len(lines) != 2 {
		t.Fatalf("应解析出 2 行，实测 %d", len(lines))
	}
	if HasWordTiming(lines) {
		t.Error("词时长全 0 时 HasWordTiming 应为 false（退回行级渲染）")
	}
}

// 词时间为相对行首偏移的变体：整行词尾都落在行起始之前时补上行起始。
func TestQrcRelativeWordTimesFallback(t *testing.T) {
	lines := ParseQRC("[30000,3000]甲(0,500)乙(500,500)")
	if len(lines) != 1 {
		t.Fatalf("应解析出 1 行，实测 %d", len(lines))
	}
	w := lines[0].Words
	if w[0].Time != 30 || w[1].Time != 30.5 {
		t.Errorf("相对时间轴未补行起始：%+v", w)
	}
}

func TestQrcLineWordFills(t *testing.T) {
	line := QrcLine{Time: 10, Dur: 2, Words: []QrcWord{
		{Time: 10, Dur: 1, Text: "a"},
		{Time: 11, Dur: 1, Text: "b"},
	}}
	var buf []float32
	cases := []struct {
		t    float64
		want []float32
	}{
		{9, []float32{0, 0}},
		{10.5, []float32{0.5, 0}},
		{11, []float32{1, 0}},
		{11.5, []float32{1, 0.5}},
		{12, []float32{1, 1}},
		{99, []float32{1, 1}},
	}
	for _, c := range cases {
		got := line.WordFills(c.t, buf)
		buf = got
		if len(got) != 2 || got[0] != c.want[0] || got[1] != c.want[1] {
			t.Errorf("t=%v 应得 %v，实测 %v", c.t, c.want, got)
		}
	}
}

// 不是 QRC 的输入（脏数据 / 空串 / 纯 LRC）应返回 nil，由调用方回退。
func TestParseQRCRejectsNonQRC(t *testing.T) {
	for _, s := range []string{"", "   ", "[00:02.25]普通的 LRC 行", "zzzz", "aabb"} {
		if got := ParseQRC(s); len(got) != 0 {
			t.Errorf("ParseQRC(%q) 应为空，实测 %+v", s, got)
		}
	}
}

// 上游 CGI 返回的密文（hex）由 jixunmoe-go/qrc 在本地解出来：
// 这是「服务端解密失败时本地兜底」那条路径的正身。
func TestDecodeQRCBlobRealFixture(t *testing.T) {
	out, ok := decodeQRCBlob(qrcLiveFixtureHex)
	if !ok {
		t.Fatal("真实上游密文应当能本地解出")
	}
	if !strings.Contains(out, "[ti:晴天]") {
		t.Fatalf("本地解码结果异常: %.120q", out)
	}
}

func TestDecodeQRCBlobRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "   ", "zzzz", "aabb", "[0,1000]already plain"} {
		if _, ok := decodeQRCBlob(s); ok {
			t.Errorf("decodeQRCBlob(%q) 不该成功", s)
		}
	}
}

// 翻译按行起始时间就近对齐进逐字歌词行。
func TestAlignQrcTrans(t *testing.T) {
	ql := ParseQRC("[0,2000]一(0,500)\n[5000,2000]二(5000,500)")
	if len(ql) != 2 {
		t.Fatalf("前置解析失败：%+v", ql)
	}
	alignQrcTrans(ql, []LyricLine{{Time: 0.1, Text: "one"}, {Time: 5.2, Text: "two"}})
	if ql[0].Trans != "one" || ql[1].Trans != "two" {
		t.Errorf("翻译对齐异常：%q / %q", ql[0].Trans, ql[1].Trans)
	}
}
