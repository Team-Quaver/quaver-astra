package appui

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
)

func TestWindowBackgroundFollowsForcedTheme(t *testing.T) {
	store := conf.Open(t.TempDir())
	cases := []struct {
		theme string
		want  string
	}{
		{"light", "#f7f7f8"},
		{"dark", "#131417"},
		{"system", "light-dark(#f7f7f8, #131417)"},
		{"broken", "light-dark(#f7f7f8, #131417)"},
	}
	for _, tc := range cases {
		store.Set("Style.Theme", tc.theme)
		if got := WindowBackground(store); got != tc.want {
			t.Errorf("theme %q: background %q, want %q", tc.theme, got, tc.want)
		}
	}
	if got := WindowBackground(nil); got != "light-dark(#f7f7f8, #131417)" {
		t.Errorf("nil store: %q", got)
	}
}
