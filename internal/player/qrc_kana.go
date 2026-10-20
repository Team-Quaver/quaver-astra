package player

import (
	"strconv"
	"strings"
	"unicode"
)

// QRC 的 [kana: ...] 振假名注音。
//
// QQ 音乐日文逐字歌词把汉字上方的假名放在一条独立元数据行里：
//
//	[kana:2かい1つ3ほん...]
//
// 数字表示这段读音覆盖几个「可注音字符」（汉字、々、〆，连续数字串算一个），
// 紧跟的假名是读音；假名后可带 (起始毫秒,时长毫秒) 自己的逐字时间轴。
// 对齐是跨全曲的单向游标，不能按行重置——错一个字符，后面整首都会串位。
//
// 这与 roma 字段不是一回事：roma 是整行音译，走歌词下方辅助行；
// kana 是词上方 Ruby，永远不参与翻译/音译档位选择。

// KanaUnit 是一条 [kana:] 标签里的注音单元。
type KanaUnit struct {
	KanjiCount int
	KanaText   string
	Spans      []RubySpan
}

type kanaLocation struct {
	line, word int
	charIndex  int
	charLen    int
}

// extractKanaTag 取最后一条 [kana:...] 标签；大小写不敏感。
func extractKanaTag(text string) string {
	found := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if len(line) < 6 || !strings.EqualFold(line[:6], "[kana:") {
			continue
		}
		if strings.HasSuffix(line, "]") {
			found = line
		}
	}
	return found
}

// stripKanaTag 摘掉 [kana:] 元数据行，避免它被当成歌词正文。
func stripKanaTag(text string) string {
	if !strings.Contains(strings.ToLower(text), "[kana:") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if len(line) >= 6 && strings.EqualFold(line[:6], "[kana:") && strings.HasSuffix(line, "]") {
			continue
		}
		out = append(out, raw)
	}
	return strings.Join(out, "\n")
}

// ParseKanaUnits 解析 [kana:...] 的内容。数字只认单个 1..9；括号属于紧挨
// 在它前面的那个假名。
func ParseKanaUnits(rawTag string) []KanaUnit {
	content := strings.TrimSpace(rawTag)
	if len(content) >= 6 && strings.EqualFold(content[:6], "[kana:") {
		content = content[6:]
	}
	content = strings.TrimSuffix(content, "]")
	runes := []rune(content)
	var out []KanaUnit
	for i := 0; i < len(runes); {
		r := runes[i]
		if r < '1' || r > '9' {
			i++
			continue
		}
		unit := KanaUnit{KanjiCount: int(r - '0')}
		i++
		var text strings.Builder
		for i < len(runes) {
			r = runes[i]
			if r >= '1' && r <= '9' {
				break
			}
			if r == '(' {
				if close := indexRune(runes, i+1, ')'); close >= 0 {
					slice := string(runes[i+1 : close])
					if comma := strings.IndexByte(slice, ','); comma >= 0 {
						start, err1 := strconv.ParseFloat(strings.TrimSpace(slice[:comma]), 64)
						dur, err2 := strconv.ParseFloat(strings.TrimSpace(slice[comma+1:]), 64)
						if err1 == nil && err2 == nil && start >= 0 && dur >= 0 {
							// 括号描述的是它前面刚写下的那个假名。
							target := lastRune(text.String())
							if target != 0 {
								unit.Spans = append(unit.Spans, RubySpan{
									Time: start / 1000,
									Dur:  dur / 1000,
									Text: string(target),
								})
							}
						}
					}
					i = close + 1
					continue
				}
			}
			text.WriteRune(r)
			i++
		}
		unit.KanaText = text.String()
		out = append(out, unit)
	}
	return out
}

func indexRune(runes []rune, from int, want rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == want {
			return i
		}
	}
	return -1
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// applyKanaToLines 把注音按全曲可注音字符流挂到 QrcWord.Ruby 上。
func applyKanaToLines(lines []QrcLine, rawTag string) {
	if rawTag == "" {
		return
	}
	units := ParseKanaUnits(rawTag)
	if len(units) == 0 {
		return
	}
	var locs []kanaLocation
	for li := range lines {
		for wi := range lines[li].Words {
			runes := []rune(lines[li].Words[wi].Text)
			for ci := 0; ci < len(runes); {
				r := runes[ci]
				if isKanaBase(r) {
					locs = append(locs, kanaLocation{li, wi, ci, 1})
					ci++
					continue
				}
				if unicode.IsDigit(r) && (r <= '9' || r >= '０' && r <= '９') {
					start := ci
					for ci < len(runes) && isKanaDigit(runes[ci]) {
						ci++
					}
					locs = append(locs, kanaLocation{li, wi, start, ci - start})
					continue
				}
				ci++
			}
		}
	}
	if len(locs) == 0 {
		return
	}

	pointer := 0
	for _, unit := range units {
		if pointer >= len(locs) || unit.KanjiCount <= 0 {
			break
		}
		n := min(unit.KanjiCount, len(locs)-pointer)
		targets := locs[pointer : pointer+n]
		pointer += n
		if unit.KanaText == "" {
			continue
		}
		first := targets[0]
		word := &lines[first.line].Words[first.word]
		if len(unit.Spans) > 0 {
			word.Ruby = append(word.Ruby, unit.Spans...)
			continue
		}
		if len(targets) == 1 {
			start, end := word.Time, word.Time+word.Dur
			total := len([]rune(word.Text))
			if total > 1 && end > start {
				step := word.Dur / float64(total)
				start = word.Time + float64(first.charIndex)*step
				end = word.Time + float64(first.charIndex+first.charLen)*step
			}
			word.Ruby = append(word.Ruby, RubySpan{Time: start, Dur: max(0, end-start), Text: unit.KanaText})
			continue
		}
		last := targets[len(targets)-1]
		end := lines[last.line].Words[last.word].Time + lines[last.line].Words[last.word].Dur
		word.Ruby = append(word.Ruby, RubySpan{Time: word.Time, Dur: max(0, end-word.Time), Text: unit.KanaText})
	}
}

func isKanaBase(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff ||
		r >= 0x3400 && r <= 0x4dbf ||
		r == '々' || r == '〆'
}

func isKanaDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= '０' && r <= '９'
}
