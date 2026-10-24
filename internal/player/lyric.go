package player

import (
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
// 翻译 / 音译行按最近时间戳（1.5s 内、一一对应）对齐进主行。
func ParseLrc(lrc, trans, roma string) []LyricLine {
	base := lrcLines(lrc)
	if len(base) == 0 {
		return nil
	}
	attachAux(base, lrcLines(trans), "trans")
	attachAux(base, lrcLines(roma), "roma")
	sort.Slice(base, func(i, j int) bool { return base[i].Time < base[j].Time })
	return base
}

// AttachLrcAux 把翻译与音译两路辅助行挂到已有行级歌词上（QRC 行同样用）。
func AttachLrcAux(base []LyricLine, trans, roma string) {
	attachAux(base, lrcLines(trans), "trans")
	attachAux(base, lrcLines(roma), "roma")
}

var junkPrefixes = []string{"//", "作词", "作曲", "编曲", "填词", "谱曲", "Lyric", "lyric"}

func lrcLines(s string) []LyricLine {
	var out []LyricLine
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		// qrc=1 时上游把翻译/音译也换成 QRC 行时间（[start,dur]text）。
		if m := reQrcLine.FindStringSubmatch(line); m != nil {
			start, _ := strconv.ParseFloat(m[1], 64)
			text := strings.TrimSpace(qrcPlainText(m[3]))
			if text != "" && !junkLine(text) {
				out = append(out, LyricLine{Time: start / 1000, Text: text})
			}
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

// attachAux 把一路辅助行按时间就近（<1.5s）对齐进主行，一一对应。
func attachAux(base, aux []LyricLine, field string) {
	if len(aux) == 0 {
		return
	}
	used := make([]bool, len(aux))
	for i := range base {
		best, bestD := -1, 1.5
		for j := range aux {
			if used[j] {
				continue
			}
			d := abs(base[i].Time - aux[j].Time)
			if d < bestD {
				bestD, best = d, j
			}
		}
		if best < 0 {
			continue
		}
		used[best] = true
		switch field {
		case "trans":
			base[i].Trans = aux[best].Text
		case "roma":
			base[i].Roma = aux[best].Text
		}
	}
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
