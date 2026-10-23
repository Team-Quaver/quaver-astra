//go:build windows

package windows

import "testing"

// TestGlassMargins checks that a maximized window with a hidden title bar
// extends its frame from the bottom over the whole client area, leaving DWM
// no top to draw caption buttons in, and that other windows extend it all
// over.
func TestGlassMargins(t *testing.T) {
	all := margins{-1, -1, -1, -1}
	if m := glassMargins(false, 760); m != all {
		t.Errorf("restored: %+v, want %+v", m, all)
	}
	if m := glassMargins(true, 1032); m != (margins{Bottom: 1032}) {
		t.Errorf("maximized: %+v, want the bottom over 1032 pixels", m)
	}
	if m := glassMargins(true, 0); m.Top != 0 || m.Bottom < 1 {
		t.Errorf("maximized without a client area: %+v, want a bottom and no top", m)
	}
}
