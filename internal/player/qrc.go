package player

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"

	qrclib "github.com/jixunmoe-go/qrc"
)

// QRC（QQ 音乐逐字歌词）解码与解析。
//
// 上游 /song/{mid}/lyric?qrc=1 的 lyric 字段是解密后的 QRC 明文，形如：
//
//	<?xml …><QrcInfos>…<Lyric_1 Lyrics="[st,dur]词(st,dur)…&#10;…"/>…</QrcInfos>
//
// 正常路径由 Typhoeus-go 在服务端解密。但上游换过加密形态、也出现过
// 「服务端解密失败、原样透传密文」的情况，所以这里再接一层
// jixunmoe-go/qrc 的 .qrc 解码器兜底：拿到 hex 密文或 QMC 魔数开头的
// 二进制时在本地解出来。
//
// 时间轴口径（与 QQ 音乐 / Lyricify / AMLL 一致）：
//   - 行：[start,dur]，毫秒，绝对
//   - 词：text(start,dur)，毫秒，绝对
//
// 例：[1790,2062]那(1790,375)一(2165,309)年 (2474,315)
// 行从 1790ms 起、持续 2062ms；「那」1790→2165，「一」2165→2474。

// QrcWord 是逐字时间轴上的一个词（QRC 里通常是一个字）。
type QrcWord struct {
	Time float64 // 秒，绝对
	Dur  float64 // 秒
	Text string
}

// QrcLine 是一行逐字歌词。Words 为空时该行只有行级时间轴。
type QrcLine struct {
	Time  float64 // 秒
	Dur   float64 // 秒
	Text  string
	Trans string
	Words []QrcWord
}

// End 返回该行的结束时间（秒）。
func (l QrcLine) End() float64 {
	if l.Dur > 0 {
		return l.Time + l.Dur
	}
	if n := len(l.Words); n > 0 {
		last := l.Words[n-1]
		return last.Time + last.Dur
	}
	return l.Time
}

// WordFills 把每个词在 t 时刻「已唱」的比例（0..1）写进 out 并返回，
// 供逐字高亮使用：已唱过的是 1，还没开始的是 0，正在唱的那个词内插值。
//
// out 由调用方复用（渲染每帧都要算一次，不该每帧分配）。
func (l QrcLine) WordFills(t float64, out []float32) []float32 {
	out = out[:0]
	for _, w := range l.Words {
		var v float32
		switch {
		case w.Dur <= 0:
			if t >= w.Time {
				v = 1
			}
		case t >= w.Time+w.Dur:
			v = 1
		case t > w.Time:
			v = float32((t - w.Time) / w.Dur)
		}
		out = append(out, v)
	}
	return out
}

var (
	// XML 信封里的歌词属性：老版本是 LyricContent，新版本是 Lyrics。
	reQrcXMLAttr = regexp.MustCompile(`(?:LyricContent|Lyrics)="([^"]*)"`)
	// 行：[start,dur]正文
	reQrcLine = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
	// 词：正文(start,dur)——括号里必须是两个整数，才不会把歌词里的
	// 普通括号（如「(Live)」）误判成时间标签。
	reQrcWord = regexp.MustCompile(`([^()]*)\((\d+),(\d+)\)`)
	// 元信息行：[ti:…] [ar:…] [al:…] [by:…] [offset:…]
	reQrcTag = regexp.MustCompile(`^\[[a-zA-Z]+:`)
)

// qmcMagic 是 .qrc 文件密文的 QMC 头，jixunmoe-go/qrc 内部同样以它判别。
var qmcMagic = []byte{0x98, 0x25, 0xB0, 0xAC, 0xE3, 0x02, 0x83, 0x68, 0xE8, 0xFC, 0x6C}

// ParseQRC 解析 QRC 歌词。src 可以是：
//   - 上游 qrc=1 返回的 QRC XML 信封（LyricContent / Lyrics 属性）
//   - 纯文本 QRC（每行 [start,dur]词(start,dur)…）
//   - .qrc 文件密文（hex 文本或 QMC 魔数开头的二进制）——本地用
//     jixunmoe-go/qrc 解码
//
// 解不出来时返回 nil，调用方回退行级歌词。
func ParseQRC(src string) []QrcLine {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil
	}
	body := extractQRCContent(src)
	if !looksPlainQRC(body) {
		if out, ok := decodeQRCBlob(body); ok {
			body = extractQRCContent(out)
		}
	}
	return parseQRCBody(body)
}

// HasWordTiming 判断这份逐字歌词是否真带词级时间轴。有些来源返回的
// 「QRC」每行只有行时间（词时长为 0），那种按行级渲染更合适。
func HasWordTiming(lines []QrcLine) bool {
	for _, l := range lines {
		for _, w := range l.Words {
			if w.Dur > 0 {
				return true
			}
		}
	}
	return false
}

// extractQRCContent 从 XML 信封里抽出歌词正文并还原实体；不是信封时
// 原样返回。&#10; / 字面 \n 都还原成换行。
func extractQRCContent(s string) string {
	if m := reQrcXMLAttr.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = decodeXMLEntities(s)
	// 有些来源把换行写成字面量 \n（两个字符）。
	if strings.Contains(s, `\n`) {
		s = strings.ReplaceAll(s, `\n`, "\n")
	}
	return s
}

// decodeXMLEntities 还原 XML 属性值里的实体。&amp; 最后替换，避免二次解码。
func decodeXMLEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], ';')
		if end < 0 || end > 8 {
			b.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i+1 : i+end]
		if v, ok := xmlEntity(ent); ok {
			b.WriteString(v)
			i += end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return strings.ReplaceAll(b.String(), "&amp;", "&")
}

func xmlEntity(ent string) (string, bool) {
	switch ent {
	case "lt", "LT":
		return "<", true
	case "gt", "GT":
		return ">", true
	case "quot", "QUOT":
		return `"`, true
	case "apos":
		return "'", true
	case "nbsp":
		return " ", true
	case "amp", "AMP":
		return "&", true
	}
	switch {
	case strings.HasPrefix(ent, "#x"), strings.HasPrefix(ent, "#X"):
		if n, err := strconv.ParseInt(ent[2:], 16, 32); err == nil && n >= 0 {
			return string(rune(n)), true
		}
	case strings.HasPrefix(ent, "#"):
		if n, err := strconv.ParseInt(ent[1:], 10, 32); err == nil && n >= 0 {
			return string(rune(n)), true
		}
	}
	return "", false
}

// looksPlainQRC 判断一段 payload 是否已经是可用的 QRC 明文。
func looksPlainQRC(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if reQrcLine.MatchString(line) || reQrcTag.MatchString(line) {
			return true
		}
		// 只看头几行的形状就够了，不用扫完整份歌词。
		break
	}
	return false
}

// decodeQRCBlob 用 jixunmoe-go/qrc 本地解出 .qrc 密文（hex 文本或
// QMC 魔数开头的二进制）。不是密文、或解不开时返回 false。
func decodeQRCBlob(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	raw := []byte(s)
	if !bytes.HasPrefix(raw, qmcMagic) {
		compact := strings.Join(strings.Fields(s), "")
		decoded, err := hex.DecodeString(compact)
		if err != nil || len(decoded) == 0 {
			return "", false
		}
		raw = decoded
	}
	out, err := qrclib.DecodeQRC(raw)
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return "", false
	}
	return string(out), true
}

// parseQRCBody 解析纯文本 QRC 正文。
func parseQRCBody(body string) []QrcLine {
	var out []QrcLine
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || reQrcTag.MatchString(line) {
			continue
		}
		m := reQrcLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		startMS, _ := strconv.ParseFloat(m[1], 64)
		durMS, _ := strconv.ParseFloat(m[2], 64)
		content := m[3]
		words := qrcWords(content, startMS)
		text := strings.TrimSpace(qrcPlainText(content))
		if text == "" && len(words) == 0 {
			// 纯间奏行（[start,dur](start,dur)）：跳过，行级视图里显示空行没意义。
			continue
		}
		out = append(out, QrcLine{
			Time:  startMS / 1000,
			Dur:   durMS / 1000,
			Text:  text,
			Words: words,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// qrcPlainText 去掉正文里的词级时间标签，只留歌词文字。
func qrcPlainText(content string) string {
	return reQrcWord.ReplaceAllString(content, "$1")
}

// qrcWords 抽出正文里的词级时间轴。lineStartMS 是所在行的起始毫秒。
func qrcWords(content string, lineStartMS float64) []QrcWord {
	ms := reQrcWord.FindAllStringSubmatch(content, -1)
	if len(ms) == 0 {
		return nil
	}
	words := make([]QrcWord, 0, len(ms))
	maxEnd := 0.0
	for _, m := range ms {
		t, _ := strconv.ParseFloat(m[2], 64)
		d, _ := strconv.ParseFloat(m[3], 64)
		words = append(words, QrcWord{Time: t / 1000, Dur: d / 1000, Text: m[1]})
		if e := t + d; e > maxEnd {
			maxEnd = e
		}
	}
	// 口径兜底：官方 QRC 的词时间是绝对毫秒，绝对时间必然 ≥ 行起始。
	// 若整行词尾都落在行起始之前，说明这份来源给的是相对行首的偏移，
	// 补上行起始。（不猜「第一个词是不是 0」——首行行起始就是 0，猜不准。）
	if lineStartMS > 0 && maxEnd < lineStartMS {
		off := lineStartMS / 1000
		for i := range words {
			words[i].Time += off
		}
	}
	return words
}

// qrcToLines 把逐字歌词摊成行级视图：列表滚动、点击跳转、索引查找
// 这些行级逻辑不用改，逐字高亮再按行号回查 QrcLine。
func qrcToLines(ql []QrcLine) []LyricLine {
	out := make([]LyricLine, 0, len(ql))
	for _, l := range ql {
		out = append(out, LyricLine{Time: l.Time, Text: l.Text, Trans: l.Trans})
	}
	return out
}

// alignQrcTrans 把翻译行按时间就近（<1.5s）对齐进逐字歌词行，
// 口径与 LRC 的 alignTrans 一致。
func alignQrcTrans(lines []QrcLine, trans []LyricLine) {
	used := make([]bool, len(trans))
	for i := range lines {
		best, bestD := -1, 1.5
		for j := range trans {
			if used[j] {
				continue
			}
			if d := abs(lines[i].Time - trans[j].Time); d < bestD {
				bestD, best = d, j
			}
		}
		if best >= 0 {
			used[best] = true
			lines[i].Trans = trans[best].Text
		}
	}
}
