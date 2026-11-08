package player

import (
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
)

// TTML（Timed Text Markup Language）逐字歌词解析。
//
// 网盘导入的 TTML 常见两种写法：
//
//	<p begin="00:00:12.000" end="00:00:15.000">
//	  <span begin="00:00:12.000" end="00:00:12.500">逐</span>
//	  <span begin="00:00:12.500" end="00:00:13.000">字</span>
//	</p>
//
// 以及带翻译的版本（AMLL / Apple Music 风格）：
//
//	<span ttm:role="x-translation">翻译</span>
//	<span ttm:role="x-romanization">romaji</span>
//
// 解析结果复用 QrcLine / QrcWord，后面的逐字高亮与行级滚动不需要区分
// QRC 还是 TTML。

type ttmlSpan struct {
	begin, end       float64
	hasBegin, hasEnd bool
	dur              float64
	hasDur           bool
	role             string
	text             strings.Builder
	timedChild       bool
}

type ttmlWord struct {
	begin, end       float64
	hasBegin, hasEnd bool
	text             string
}

type ttmlLine struct {
	begin, end       float64
	hasBegin, hasEnd bool
	dur              float64
	hasDur           bool
	text             strings.Builder
	words            []ttmlWord
	trans, roma      strings.Builder
}

// looksTTML 判断歌词正文是否是 TTML 文档。只看根元素附近的 `<tt`，避免
// 歌词正文里恰好出现 "ttml" 就被误判。
func looksTTML(src string) bool {
	s := strings.TrimSpace(strings.TrimPrefix(src, "\ufeff"))
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		if lower[i] != '<' {
			continue
		}
		rest := lower[i:]
		if strings.HasPrefix(rest, "<ttml") {
			return true
		}
		if strings.HasPrefix(rest, "<tt") && len(rest) > 3 {
			switch rest[3] {
			case ' ', '\t', '\r', '\n', '>':
				return true
			}
		}
	}
	return false
}

// ParseTTML 解析 TTML 逐字歌词。解析不出任何带时间轴的行时返回 nil，
// 调用方会回退到 QRC / LRC。
func ParseTTML(src string) []QrcLine {
	if !looksTTML(src) {
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(strings.TrimPrefix(src, "\ufeff")))
	// 用户手打的 TTML 常有没有转义的 &，或漏掉闭合标签；尽量读下去，
	// 不要因为一处小错丢掉整份歌词。
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose

	var (
		out   []QrcLine
		line  *ttmlLine
		stack []*ttmlSpan
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "p":
				if line != nil {
					out = appendTTMLLine(out, line)
				}
				line = ttmlLineFromAttrs(t.Attr)
				stack = stack[:0]
			case "span":
				if line == nil {
					continue
				}
				stack = append(stack, ttmlSpanFromAttrs(t.Attr))
			case "br":
				if line != nil {
					line.text.WriteString(" ")
				}
			}
		case xml.CharData:
			if line == nil {
				continue
			}
			if len(stack) > 0 {
				appendTTMLText(&stack[len(stack)-1].text, string(t))
			} else {
				appendTTMLText(&line.text, string(t))
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "span":
				if line == nil || len(stack) == 0 {
					continue
				}
				sp := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				addTTMLSpan(line, sp, stack)
			case "p":
				if line != nil {
					out = appendTTMLLine(out, line)
					line = nil
					stack = stack[:0]
				}
			}
		}
	}
	if line != nil {
		out = appendTTMLLine(out, line)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func ttmlLineFromAttrs(attrs []xml.Attr) *ttmlLine {
	l := &ttmlLine{}
	for _, a := range attrs {
		switch strings.ToLower(a.Name.Local) {
		case "begin":
			l.begin, l.hasBegin = parseTTMLTime(a.Value)
		case "end":
			l.end, l.hasEnd = parseTTMLTime(a.Value)
		case "dur":
			if d, ok := parseTTMLTime(a.Value); ok {
				l.dur, l.hasDur = d, true
			}
		}
	}
	if l.hasDur {
		l.end, l.hasEnd = l.begin+l.dur, true
	}
	return l
}

func ttmlSpanFromAttrs(attrs []xml.Attr) *ttmlSpan {
	sp := &ttmlSpan{}
	for _, a := range attrs {
		switch strings.ToLower(a.Name.Local) {
		case "begin":
			sp.begin, sp.hasBegin = parseTTMLTime(a.Value)
		case "end":
			sp.end, sp.hasEnd = parseTTMLTime(a.Value)
		case "dur":
			if d, ok := parseTTMLTime(a.Value); ok {
				sp.dur, sp.hasDur = d, true
			}
		case "role":
			sp.role = strings.ToLower(strings.TrimSpace(a.Value))
		}
	}
	if sp.hasDur {
		sp.end, sp.hasEnd = sp.begin+sp.dur, true
	}
	return sp
}

// addTTMLSpan 把闭合的 span 归入行：带时间的是词，翻译/音译进辅助行，
// 其余文字并入正文。嵌套 span 的文字交给最外层统一收，避免重复。
func addTTMLSpan(line *ttmlLine, sp *ttmlSpan, stack []*ttmlSpan) {
	text := sp.text.String()
	switch sp.role {
	case "x-translation", "translation":
		line.trans.WriteString(text)
		return
	case "x-romanization", "romanization":
		line.roma.WriteString(text)
		return
	}
	if (sp.hasBegin || sp.hasEnd) && !sp.timedChild {
		line.words = append(line.words, ttmlWord{
			begin: sp.begin, end: sp.end,
			hasBegin: sp.hasBegin, hasEnd: sp.hasEnd,
			text: text,
		})
	}
	if len(stack) > 0 {
		// 父 span 还要继续收文字；只有最外层落到正文，顺序才不会乱。
		if sp.hasBegin || sp.hasEnd {
			stack[len(stack)-1].timedChild = true
		}
		appendTTMLText(&stack[len(stack)-1].text, text)
		return
	}
	appendTTMLText(&line.text, text)
}

func appendTTMLLine(out []QrcLine, l *ttmlLine) []QrcLine {
	text := strings.TrimSpace(l.text.String())
	words := finalizeTTMLWords(l.words, l)
	if text == "" && len(words) > 0 {
		var b strings.Builder
		for _, w := range words {
			b.WriteString(w.Text)
		}
		text = strings.TrimSpace(b.String())
	}
	if text == "" && len(words) == 0 {
		return out
	}
	if !l.hasBegin {
		if len(words) > 0 {
			l.begin = words[0].Time
			l.hasBegin = true
		} else {
			return out
		}
	}
	end := l.end
	if !l.hasEnd || end <= l.begin {
		end = l.begin
		for _, w := range words {
			if e := w.Time + w.Dur; e > end {
				end = e
			}
		}
	}
	dur := end - l.begin
	if dur < 0 {
		dur = 0
	}
	if !HasWordTiming([]QrcLine{{Words: words}}) {
		words = nil
	}
	return append(out, QrcLine{
		Time:  l.begin,
		Dur:   dur,
		Text:  text,
		Trans: strings.TrimSpace(l.trans.String()),
		Roma:  strings.TrimSpace(l.roma.String()),
		Words: words,
	})
}

func finalizeTTMLWords(raw []ttmlWord, l *ttmlLine) []QrcWord {
	out := make([]QrcWord, 0, len(raw))
	for i, w := range raw {
		switch {
		case w.hasBegin && w.hasEnd:
			if w.end < w.begin {
				w.end = w.begin
			}
		case w.hasBegin:
			// 只给了起点：到下一个词的起点，最后一个词到行尾。
			if i+1 < len(raw) && raw[i+1].hasBegin && raw[i+1].begin >= w.begin {
				w.end = raw[i+1].begin
			} else if l.hasEnd && l.end >= w.begin {
				w.end = l.end
			}
		case w.hasEnd:
			// 只给了终点：从上一个词的终点（或行首）起算。
			if len(out) > 0 {
				w.begin = out[len(out)-1].Time + out[len(out)-1].Dur
			} else if l.hasBegin {
				w.begin = l.begin
			}
		}
		if !w.hasBegin && !w.hasEnd {
			continue
		}
		dur := w.end - w.begin
		if dur < 0 {
			dur = 0
		}
		out = append(out, QrcWord{Time: w.begin, Dur: dur, Text: w.text})
	}
	// 有的制作工具给的是相对行首的时间（0.0、0.5…）。若整行词尾都落在
	// 行起始之前，按相对时间处理，补上行起始；官方 TTML 一般是绝对时间。
	if l.hasBegin && l.begin > 0 && len(out) > 0 {
		maxEnd := 0.0
		for _, w := range out {
			if e := w.Time + w.Dur; e > maxEnd {
				maxEnd = e
			}
		}
		if maxEnd <= l.begin {
			for i := range out {
				out[i].Time += l.begin
			}
		}
	}
	return out
}

// parseTTMLTime 解析 TTML 时间表达式：秒数、hh:mm:ss(.fff)、
// hh:mm:ss:ff（帧）、1.5s、1500ms。解析不了时返回 false。
func parseTTMLTime(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, false
	}
	scale := 1.0
	switch {
	case strings.HasSuffix(s, "ms"):
		scale, s = 0.001, strings.TrimSuffix(s, "ms")
	case strings.HasSuffix(s, "s"):
		s = strings.TrimSuffix(s, "s")
	case strings.HasSuffix(s, "f"):
		scale, s = 1.0/30, strings.TrimSuffix(s, "f")
	}
	s = strings.ReplaceAll(s, ",", ".")
	parts := strings.Split(s, ":")
	toFloat := func(v string) (float64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	switch len(parts) {
	case 1:
		v, ok := toFloat(parts[0])
		return v * scale, ok
	case 2:
		m, ok1 := toFloat(parts[0])
		sec, ok2 := toFloat(parts[1])
		return (m*60 + sec) * scale, ok1 && ok2
	case 3:
		h, ok1 := toFloat(parts[0])
		m, ok2 := toFloat(parts[1])
		sec, ok3 := toFloat(parts[2])
		return (h*3600 + m*60 + sec) * scale, ok1 && ok2 && ok3
	case 4:
		h, ok1 := toFloat(parts[0])
		m, ok2 := toFloat(parts[1])
		sec, ok3 := toFloat(parts[2])
		frames, ok4 := toFloat(parts[3])
		return (h*3600 + m*60 + sec + frames/30) * scale, ok1 && ok2 && ok3 && ok4
	}
	return 0, false
}

// appendTTMLText 收 xml 字符数据：缩进用的换行/制表空白丢掉，词与词
// 之间真正的空格保留。
func appendTTMLText(b *strings.Builder, s string) {
	if strings.TrimSpace(s) == "" && strings.ContainsAny(s, "\r\n\t") {
		return
	}
	b.WriteString(s)
}
