package player

import (
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
)

// TTMLLine is one lyric line from an AMLL/Apple Music TTML document.
//
// TTML is richer than QRC: a line may belong to one of several singers
// (Agent), and may carry a second, independently timed background-vocal
// line. Keeping those fields apart lets the UI render duets and backing
// vocals without flattening them into the main lyric text.
type TTMLLine struct {
	Time  float64
	Dur   float64
	Text  string
	Trans string
	Roma  string
	Agent string
	Key   string
	Words []QrcWord

	BackgroundText  string
	BackgroundWords []QrcWord
}

// End returns the line's end time in seconds.
func (l TTMLLine) End() float64 {
	if l.Dur > 0 {
		return l.Time + l.Dur
	}
	if n := len(l.Words); n > 0 {
		last := l.Words[n-1]
		return last.Time + last.Dur
	}
	return l.Time
}

// WordFills returns each word's sung fraction at t.
func (l TTMLLine) WordFills(t float64, out []float32) []float32 {
	out = out[:0]
	for _, w := range l.Words {
		out = append(out, wordFill(w, t))
	}
	return out
}

func wordFill(w QrcWord, t float64) float32 {
	switch {
	case w.Dur <= 0:
		if t >= w.Time {
			return 1
		}
	case t >= w.Time+w.Dur:
		return 1
	case t > w.Time:
		return float32((t - w.Time) / w.Dur)
	}
	return 0
}

// ParseTTML parses the word-timed subset of TTML used by AMLL TTML DB and
// Apple Music. It supports timed spans, singer agents, translations,
// romanization, and nested background vocals (ttm:role="x-bg").
//
// The parser is deliberately tolerant: malformed lines are skipped and a
// document without any usable lyric line returns nil. It never guesses a
// word's start from the line start when the file already has word timings.
func ParseTTML(src string) []TTMLLine {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil
	}
	var doc ttmlNode
	if err := xml.Unmarshal([]byte(src), &doc); err != nil || !strings.EqualFold(doc.Name, "tt") {
		return nil
	}
	body := doc.firstChild("body")
	if body == nil {
		return nil
	}

	var out []TTMLLine
	var walk func(*ttmlNode)
	walk = func(n *ttmlNode) {
		for i := range n.Children {
			child := &n.Children[i]
			switch {
			case strings.EqualFold(child.Name, "div"):
				walk(child)
			case strings.EqualFold(child.Name, "p"):
				if line, ok := parseTTMLParagraph(child); ok {
					out = append(out, line)
				}
			}
		}
	}
	walk(body)
	if len(out) == 0 {
		return nil
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	for i := range out {
		if out[i].Dur <= 0 {
			if i+1 < len(out) && out[i+1].Time > out[i].Time {
				out[i].Dur = out[i+1].Time - out[i].Time
			} else if end := out[i].End(); end > out[i].Time {
				out[i].Dur = end - out[i].Time
			}
		}
	}
	return out
}

func parseTTMLParagraph(p *ttmlNode) (TTMLLine, bool) {
	start, ok := ttmlTime(p.Attr["begin"])
	if !ok {
		return TTMLLine{}, false
	}
	line := TTMLLine{
		Time:  start,
		Agent: p.Attr["agent"],
		Key:   p.Attr["key"],
	}
	if end, ok := ttmlTime(p.Attr["end"]); ok && end > start {
		line.Dur = end - start
	}

	var text strings.Builder
	var bgText strings.Builder
	var visit func(*ttmlNode)
	visit = func(n *ttmlNode) {
		for i := range n.Children {
			child := &n.Children[i]
			if child.Name == "#text" {
				text.WriteString(child.Text)
				continue
			}
			if !strings.EqualFold(child.Name, "span") {
				continue
			}
			switch child.Attr["role"] {
			case "x-translation":
				if s := strings.TrimSpace(child.plainText()); s != "" && line.Trans == "" {
					line.Trans = s
				}
			case "x-roman":
				if s := strings.TrimSpace(child.plainText()); s != "" && line.Roma == "" {
					line.Roma = s
				}
			case "x-bg":
				if line.BackgroundText == "" {
					line.BackgroundText = strings.TrimSpace(child.plainText())
				}
				bgText.WriteString(child.plainText())
				line.BackgroundWords = appendTTMLWords(line.BackgroundWords, child, start)
			default:
				text.WriteString(child.plainText())
				line.Words = appendTTMLWords(line.Words, child, start)
			}
		}
	}
	visit(p)
	line.Text = normalizeTTMLSpace(text.String())
	if line.BackgroundText == "" {
		line.BackgroundText = normalizeTTMLSpace(bgText.String())
	}
	if line.Text == "" {
		return TTMLLine{}, false
	}
	return line, true
}

// appendTTMLWords flattens a span tree into timed words. A timed leaf is
// one word; containers with timed descendants recurse instead of becoming
// one large, badly timed word. Untimed text between spans is kept as a
// zero-duration spacer so spaces and punctuation survive.
func appendTTMLWords(dst []QrcWord, n *ttmlNode, fallback float64) []QrcWord {
	begin, hasBegin := ttmlTime(n.Attr["begin"])
	end, hasEnd := ttmlTime(n.Attr["end"])
	timedChildren := false
	for i := range n.Children {
		child := &n.Children[i]
		if child.Name == "#text" || child.Attr["role"] != "" {
			continue
		}
		if _, ok := ttmlTime(child.Attr["begin"]); ok {
			timedChildren = true
			break
		}
	}
	if hasBegin && !timedChildren {
		word := normalizeTTMLSpace(n.plainText())
		if word != "" {
			dur := 0.0
			if hasEnd && end > begin {
				dur = end - begin
			}
			dst = append(dst, QrcWord{Time: begin, Dur: dur, Text: word})
		}
		return dst
	}
	if hasBegin || timedChildren {
		for i := range n.Children {
			child := &n.Children[i]
			if child.Name == "#text" {
				if text := ttmlInlineSpace(child.Text); text != "" {
					dst = append(dst, QrcWord{Time: fallback, Text: text})
				}
				continue
			}
			if strings.EqualFold(child.Name, "span") {
				dst = appendTTMLWords(dst, child, begin)
			}
		}
		return dst
	}
	word := ttmlInlineSpace(n.plainText())
	if word != "" {
		dst = append(dst, QrcWord{Time: fallback, Text: word})
	}
	return dst
}

func ttmlTime(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if strings.HasSuffix(s, "ms") {
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "ms")), 64)
		return v / 1000, err == nil
	}
	if strings.HasSuffix(s, "s") {
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "s")), 64)
		return v, err == nil
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) > 3 {
			return 0, false
		}
		total := 0.0
		for _, part := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return 0, false
			}
			total = total*60 + v
		}
		return total, true
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func normalizeTTMLSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func ttmlInlineSpace(s string) string {
	if word := normalizeTTMLSpace(s); word != "" {
		return word
	}
	if s != "" && !strings.ContainsAny(s, "\r\n\t") {
		return " "
	}
	return ""
}

// ttmlNode is a namespace-agnostic XML tree that preserves mixed text and
// child order, which encoding/xml's usual struct mapping does not.
type ttmlNode struct {
	Name     string
	Text     string
	Attr     map[string]string
	Children []ttmlNode
}

func (n *ttmlNode) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	n.Name = start.Name.Local
	n.Attr = make(map[string]string, len(start.Attr))
	for _, a := range start.Attr {
		n.Attr[a.Name.Local] = a.Value
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var child ttmlNode
			if err := d.DecodeElement(&child, &t); err != nil {
				return err
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			n.Children = append(n.Children, ttmlNode{Name: "#text", Text: string(t)})
		case xml.EndElement:
			if t.Name == start.Name {
				return nil
			}
		}
	}
}

func (n *ttmlNode) firstChild(name string) *ttmlNode {
	if n == nil {
		return nil
	}
	for i := range n.Children {
		if strings.EqualFold(n.Children[i].Name, name) {
			return &n.Children[i]
		}
	}
	return nil
}

func (n *ttmlNode) plainText() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*ttmlNode)
	walk = func(cur *ttmlNode) {
		if cur.Name == "#text" {
			b.WriteString(cur.Text)
			return
		}
		for i := range cur.Children {
			walk(&cur.Children[i])
		}
	}
	walk(n)
	return b.String()
}
