//go:build linux && (amd64 || arm64)

package text

import (
	"fmt"
	"reflect"
	"testing"
)

// Punctuation and full-width forms in a language that writes Han get the
// family the language picks before the generic families (#595), and
// nothing else does.
func TestHanPunctRuns(t *testing.T) {
	for _, c := range []struct {
		text  string
		style Style
		spans []Span
		want  []hanRun
	}{
		{"macOS、Windows、Linux", Style{Lang: "zh-Hans"}, nil, []hanRun{{5, 6, "zh-Hans", ""}, {13, 14, "zh-Hans", ""}}},
		{"a（b）c", Style{Lang: "ja", Family: "Inter"}, nil, []hanRun{{1, 2, "ja", "Inter"}, {3, 4, "ja", "Inter"}}},
		{"文件、。编辑", Style{Lang: "zh"}, nil, []hanRun{{2, 4, "zh", ""}}},
		{"macOS、Windows", Style{Lang: "en"}, nil, nil},
		{"macOS、Windows", Style{}, nil, nil},
		{"a、b、c", Style{Lang: "en"}, []Span{{End: 2, Lang: "zh-Hant"}, {End: 5}}, []hanRun{{1, 2, "zh-Hant", ""}}},
		{"a、、b", Style{Lang: "zh-Hans"}, []Span{{End: 2, Family: "serif"}, {End: 4}}, []hanRun{{1, 2, "zh-Hans", "serif"}, {2, 3, "zh-Hans", ""}}},
	} {
		if got := hanPunctRuns([]rune(c.text), c.style, c.spans); !reflect.DeepEqual(got, c.want) {
			t.Errorf("hanPunctRuns(%q, %+v, %+v) = %v, want %v", c.text, c.style, c.spans, got, c.want)
		}
	}
}

func TestPangoFamilyBeforeGeneric(t *testing.T) {
	before := func(generic string) string { return "CJK " + generic }
	for _, c := range []struct{ list, want string }{
		{"", "CJK system-ui,UI,system-ui,sans-serif"},
		{"sans-serif", "CJK sans-serif,sans-serif"},
		{"Inter, monospace, serif", "Inter,CJK monospace,monospace,serif"},
		{"Inter", "Inter,CJK system-ui"},
	} {
		if got := pangoFamily(c.list, "UI", before); got != c.want {
			t.Errorf("pangoFamily(%q) = %q, want %q", c.list, got, c.want)
		}
		if got, want := pangoFamily(c.list, "UI", func(string) string { return "" }), pangoFamily(c.list, "UI", nil); got != want {
			t.Errorf("pangoFamily(%q) without a family = %q, want %q", c.list, got, want)
		}
	}
}

func TestFcHanLang(t *testing.T) {
	for tag, want := range map[string]string{
		"zh": "zh-cn", "zh-Hans": "zh-cn", "zh-CN": "zh-cn", "zh_CN": "zh-cn", "zh-Hans-SG": "zh-sg", "zh-SG": "zh-sg",
		"zh-Hant": "zh-tw", "zh-TW": "zh-tw", "zh-Hant-HK": "zh-hk", "zh-HK": "zh-hk", "zh-MO": "zh-mo", "zh-Hans-HK": "zh-cn",
		"yue": "zh-hk", "yue-Hans": "zh-cn", "cmn-Hant-TW": "zh-tw", "ja": "ja", "ja-JP": "ja", "ko-KR": "ko",
	} {
		if got := fcHanLang(tag); got != want {
			t.Errorf("fcHanLang(%q) = %q, want %q", tag, got, want)
		}
	}
}

// With the system's fonts, an ideographic comma between Latin words in
// Chinese is drawn with the font fontconfig picks for Chinese (#595).
func TestPangoHanPunctBetweenLatin(t *testing.T) {
	e, err := newPango()
	if err != nil {
		t.Skip(err)
	}
	han := e.hanFamily("zh-Hans", "sans-serif")
	if han == "" {
		t.Skip("no font for Chinese")
	}
	want := e.shape([]rune("、"), Style{Family: han, Size: 12, Lang: "zh-Hans"}, nil, 0, false, false)[0].runs[0].font
	text := []rune("macOS、Windows")
	for _, r := range e.shape(text, Style{Family: "sans-serif", Size: 12, Lang: "zh-Hans"}, nil, 0, false, false)[0].runs {
		if r.start <= 5 && 5 < r.end && r.font != want {
			t.Errorf("、 drawn with another font than %s's", han)
		}
	}
}

// A flex item may give text exactly the width of its unconstrained
// layout. Kerning in strings such as "11" must not make Pango wrap it.
func TestPangoLayoutAtMeasuredWidth(t *testing.T) {
	s := newSystem()
	for _, family := range []string{"sans-serif", "serif", "monospace"} {
		for _, size := range []float32{10, 12, 14, 16} {
			for _, value := range []string{"11", "10", "12", "1911", "2011", "2111", "2211", "2001", "2002", "2012", "2021", "2011-04-17", "2011-06-17", "11:30", "2.11.1", "AV", "office"} {
				t.Run(fmt.Sprintf("%s/%g/%s", family, size, value), func(t *testing.T) {
					p := Params{Text: value, Style: Style{Family: family, Size: size}}
					p.Width = s.Layout(p).Width
					for _, maxLines := range []int{0, 1} {
						p.MaxLines = maxLines
						l := s.Layout(p)
						if len(l.Lines) != 1 || l.Truncated || l.Lines[0].End != len(l.Runes) {
							t.Errorf("at measured width %g, MaxLines %d: %d lines, truncated %v, end %d of %d", p.Width, maxLines, len(l.Lines), l.Truncated, l.Lines[0].End, len(l.Runes))
						}
					}
				})
			}
		}
	}
}

// Letter spacing makes a text's measured width fractional, so a flex
// item may lay it out at a width a thousandth of a dip short of its own
// measurement. Truncating the scaled width must not wrap it.
func TestPangoLayoutAtMeasuredWidthWithLetterSpacing(t *testing.T) {
	s := newSystem()
	for _, spacing := range []float32{0.1, 0.25, 0.5, 1, 1.5} {
		for _, value := range []string{"System", "Settings", "Favorites", "Home", "Now Playing"} {
			t.Run(fmt.Sprintf("%g/%s", spacing, value), func(t *testing.T) {
				p := Params{Text: value, Style: Style{Family: "sans-serif", Size: 14, LetterSpacing: spacing}}
				p.Width = s.Layout(p).Width
				for _, maxLines := range []int{0, 1} {
					p.MaxLines = maxLines
					l := s.Layout(p)
					if len(l.Lines) != 1 || l.Truncated || l.Lines[0].End != len(l.Runes) {
						t.Errorf("at measured width %g, MaxLines %d: %d lines, truncated %v, end %d of %d", p.Width, maxLines, len(l.Lines), l.Truncated, l.Lines[0].End, len(l.Runes))
					}
				}
			})
		}
	}
}
