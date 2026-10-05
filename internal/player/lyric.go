package player

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reLrcTS = regexp.MustCompile(`\[(\d{1,2}):(\d{2})(?:[.:](\d{1,3}))?\]`)

// LyricLine 一行歌词（时间 + 原文 + 可选翻译）。
type LyricLine struct {
	Time  float64 // 秒
	Text  string
	Trans string
}

// ParseLrc 解析行级 LRC（多时间戳展开、// 与占位行跳过），并把翻译行
// 按最近时间戳（1.5s 内、一一对应）对齐进主行。
func ParseLrc(lrc, trans string) []LyricLine {
	base := lrcLines(lrc)
	if len(base) == 0 {
		return nil
	}
	if t := lrcLines(trans); len(t) > 0 {
		alignTrans(base, t)
	}
	sort.Slice(base, func(i, j int) bool { return base[i].Time < base[j].Time })
	return base
}

var junkPrefixes = []string{"//", "作词", "作曲", "编曲", "填词", "谱曲", "Lyric", "lyric"}

func lrcLines(s string) []LyricLine {
	var out []LyricLine
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		stamps := lrcTimestamps(line)
		if len(stamps) == 0 {
			continue
		}
		text := strings.TrimSpace(lrcStrip(line))
		if text == "" {
			continue
		}
		skip := false
		for _, p := range junkPrefixes {
			if strings.HasPrefix(text, p) {
				skip = true
				break
			}
		}
		if skip {
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

// alignTrans 把翻译行按时间就近（<1.5s）对齐进主行，一一对应。
func alignTrans(base, trans []LyricLine) {
	used := make([]bool, len(trans))
	for i := range base {
		best, bestD := -1, 1.5
		for j := range trans {
			if used[j] {
				continue
			}
			d := abs(base[i].Time - trans[j].Time)
			if d < bestD {
				bestD, best = d, j
			}
		}
		if best >= 0 {
			used[best] = true
			base[i].Trans = trans[best].Text
		}
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
