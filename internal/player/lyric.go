package player

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reLrcTS = regexp.MustCompile(`\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\]`)

// LyricLine 一行歌词（时间 + 原文 + 可选翻译 / 音译）。
//
// Trans 与 Roma 是同一位置的辅助行：翻译档优先 Trans，音译档优先 Roma，
// 本行缺哪一路就回落另一路。振假名不在这里——它挂在 QrcWord.Ruby 上。
type LyricLine struct {
	Time  float64 // 秒
	Text  string
	Trans string
	Roma  string
}

// ParseLrc 解析行级 LRC（多时间戳展开、// 与占位行跳过），并分别把
// 翻译 / 音译行对齐进主行。
func ParseLrc(lrc, trans, roma string) []LyricLine {
	base := lrcLines(lrc)
	if len(base) == 0 {
		return nil
	}
	attachAux(base, lrcAuxLines(trans), "trans")
	attachAux(base, lrcAuxLines(roma), "roma")
	sort.Slice(base, func(i, j int) bool { return base[i].Time < base[j].Time })
	return base
}

// AttachLrcAux 把翻译与音译两路辅助行挂到已有行级歌词上（QRC 行同样用）。
func AttachLrcAux(base []LyricLine, trans, roma string) {
	attachAux(base, lrcAuxLines(trans), "trans")
	attachAux(base, lrcAuxLines(roma), "roma")
}

var junkPrefixes = []string{
	"//", "作词", "作曲", "编曲", "填词", "谱曲", "Lyric", "lyric",
	// QQ 翻译文件的首行版权声明，不是歌词翻译。
	"QQ音乐享有本翻译作品的著作权",
}

func lrcLines(s string) []LyricLine { return parseLrcLines(s, false) }

// lrcAuxLines 解析翻译/音译行，并为占位行保留一个空槽位。
//
// QQ 的翻译/音译文件与主歌词逐行对应：没有翻译的行写成 "//"。
// 这些占位行是唯一能区分「同一时间戳下的制作名单行」和「正文行」
// 的线索——丢掉它们，后面的翻译就会整体上移一行。
func lrcAuxLines(s string) []LyricLine { return parseLrcLines(s, true) }

// parseLrcLines 解析 QRC 或 LRC 行。keepSlots 为 true 时，占位/制作
// 名单行仍占一个空槽位，保持与主歌词的行序对应。
func parseLrcLines(s string, keepSlots bool) []LyricLine {
	var out []LyricLine
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		// qrc=1 时上游把翻译/音译也换成 QRC 行时间（[start,dur]text）。
		if m := reQrcLine.FindStringSubmatch(line); m != nil {
			start, _ := strconv.ParseFloat(m[1], 64)
			text := strings.TrimSpace(qrcPlainText(m[3]))
			if text == "" {
				continue // 纯间奏行：主歌词那边同样不会出现
			}
			if junkLine(text) {
				if keepSlots {
					out = append(out, LyricLine{Time: start / 1000})
				}
				continue
			}
			out = append(out, LyricLine{Time: start / 1000, Text: text})
			continue
		}
		stamps := lrcTimestamps(line)
		if len(stamps) == 0 {
			continue
		}
		text := strings.TrimSpace(lrcStrip(line))
		if text == "" {
			continue
		}
		if junkLine(text) {
			if keepSlots {
				for _, t := range stamps {
					out = append(out, LyricLine{Time: t})
				}
			}
			continue
		}
		for _, t := range stamps {
			out = append(out, LyricLine{Time: t, Text: text})
		}
	}
	return out
}

// lrcTimestamps 取一行里的全部时间戳（秒）。
func lrcTimestamps(line string) []float64 {
	var out []float64
	for _, m := range reLrcTS.FindAllStringSubmatch(line, -1) {
		min, _ := strconv.ParseFloat(m[1], 64)
		sec, _ := strconv.ParseFloat(m[2], 64)
		frac := 0.0
		if m[3] != "" {
			// 1~3 位小数都按比例归一
			f, _ := strconv.ParseFloat("0."+m[3], 64)
			frac = f
		}
		out = append(out, min*60+sec+frac)
	}
	return out
}

// lrcStrip 去掉一行开头的时间戳标签。
func lrcStrip(line string) string {
	for {
		loc := reLrcTS.FindStringIndex(line)
		if loc == nil || loc[0] != 0 {
			return line
		}
		line = line[loc[1]:]
	}
}

func junkLine(text string) bool {
	for _, p := range junkPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// attachAux 把一路辅助行对齐进主行。
//
// 翻译/音译文件与主歌词通常逐行对应（没有翻译的行是空槽位），所以
// 两边行数一致时直接按行号配对。这样同一时间戳下的制作名单行也不会
// 抢走正文的翻译——时间上它们完全分不出来，只有行序能。
//
// 行数不一致时退化为保序对齐：在时间窗口内让匹配数最多、时间误差最小，
// 且不允许交叉。主歌词被过滤掉几行制作名单（例如「编曲」）后仍然能对上。
func attachAux(base, aux []LyricLine, field string) {
	if len(aux) == 0 || len(base) == 0 {
		return
	}
	const maxDelta = 1.5
	if len(base) == len(aux) {
		for i := range base {
			if abs(base[i].Time-aux[i].Time) < maxDelta && aux[i].Text != "" {
				setAux(&base[i], aux[i].Text, field)
			}
		}
		return
	}
	for _, p := range alignAuxOrdered(base, aux, maxDelta) {
		setAux(&base[p.base], aux[p.aux].Text, field)
	}
}

func setAux(line *LyricLine, text, field string) {
	switch field {
	case "trans":
		line.Trans = text
	case "roma":
		line.Roma = text
	}
}

// alignAuxOrdered 在保持行序的前提下，把辅助行尽量多地配给时间接近的
// 主行。返回的配对按主行顺序排列。
func alignAuxOrdered(base, aux []LyricLine, maxDelta float64) []struct{ base, aux int } {
	n, m := len(base), len(aux)
	type score struct {
		count int
		cost  float64
	}
	better := func(a, b score) bool {
		return a.count > b.count || (a.count == b.count && a.cost < b.cost)
	}
	dp := make([][]score, n+1)
	for i := range dp {
		dp[i] = make([]score, m+1)
	}
	matchable := func(i, j int) bool {
		return aux[j].Text != "" && abs(base[i].Time-aux[j].Time) < maxDelta
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			best := dp[i+1][j]
			if s := dp[i][j+1]; better(s, best) {
				best = s
			}
			if matchable(i, j) {
				s := dp[i+1][j+1]
				s.count++
				s.cost += abs(base[i].Time - aux[j].Time)
				if better(s, best) {
					best = s
				}
			}
			dp[i][j] = best
		}
	}
	var pairs []struct{ base, aux int }
	for i, j := 0, 0; i < n && j < m; {
		cur := dp[i][j]
		if matchable(i, j) {
			next := dp[i+1][j+1]
			next.count++
			next.cost += abs(base[i].Time - aux[j].Time)
			if next == cur {
				pairs = append(pairs, struct{ base, aux int }{i, j})
				i++
				j++
				continue
			}
		}
		if dp[i+1][j] == cur {
			i++
			continue
		}
		j++
	}
	return pairs
}

// AuxText 按档位挑唯一一条辅助行：翻译档优先翻译、音译档优先音译，
// 本行缺哪一路就回落另一路；off 永远返回空串。
func AuxText(line LyricLine, mode string) string {
	switch mode {
	case "translation":
		if strings.TrimSpace(line.Trans) != "" {
			return line.Trans
		}
		return line.Roma
	case "romanization":
		if strings.TrimSpace(line.Roma) != "" {
			return line.Roma
		}
		return line.Trans
	default:
		return ""
	}
}

// LyricLinesToText 把解析后的歌词拍成 xesam:asText 的纯文本载荷：只留主歌词，
// 一行一句，不带时间戳。翻译/音译属于辅助行，不进入这个字段。
func LyricLinesToText(lines []LyricLine) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if text := strings.TrimSpace(line.Text); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

// LyricLinesToLRC 把解析后的歌词序列化成 LRC 文本（[mm:ss.xx]一行）。
//
// QRC/TTML 的逐字时间轴已经在行级 LyricLine 里，这里只保留行级时间戳：
// LRC 装不下词级时间轴。时间先取整到厘秒再拆分，避免 59.999 秒进位成
// 60.00；空行与翻译/音译跳过。
func LyricLinesToLRC(lines []LyricLine) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			continue
		}
		cs := int64(math.Round(line.Time * 100))
		if cs < 0 {
			cs = 0
		}
		min := cs / 6000
		sec := (cs % 6000) / 100
		frac := cs % 100
		out = append(out, fmt.Sprintf("[%02d:%02d.%02d]%s", min, sec, frac, text))
	}
	return strings.Join(out, "\n")
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// LyricIndexAt 返回 t 时刻正在唱的行号（-1 表示前奏）。
func LyricIndexAt(lines []LyricLine, t float64) int {
	if len(lines) == 0 {
		return -1
	}
	// 歌词时间轴整体比实际播放早 ~0.2s，与主项目一致做补偿
	t += 0.2
	idx := -1
	for i := range lines {
		if lines[i].Time <= t {
			idx = i
		} else {
			break
		}
	}
	return idx
}
