package ui

import (
	"strings"
	"testing"
)

// Issue #425: Find walks the element tree without checking geometry, so it
// can return an element the window never shows. Clicking it must report the
// miss instead of silently clicking nothing, while an on-window element
// still clicks as before.
func TestClickOffWindowElement(t *testing.T) {
	var entered string
	view := func(c *Context) {
		Row(c).Translate(1400, 100).Children(func() {
			Button(c, "off").OnClick(func() { t.Error("off-window button must not be clicked") })
		})
		Button(c, "on").OnClick(func() { entered = "on" })
	}

	tt := NewTester(view, 1200, 800)
	tt.Frame()

	if err := tt.Click("off"); err == nil {
		t.Fatal("Click on an off-window element returned nil")
	} else if !strings.Contains(err.Error(), "outside the window") {
		t.Fatalf("Click error does not name the cause: %v", err)
	}

	if err := tt.ClickWith(Shift, "off"); err == nil {
		t.Fatal("ClickWith on an off-window element returned nil")
	}

	if err := tt.RightClick("off"); err == nil {
		t.Fatal("RightClick on an off-window element returned nil")
	}

	if err := tt.Click("on"); err != nil {
		t.Fatalf("Click on an on-window element failed: %v", err)
	}
	if entered != "on" {
		t.Fatal("Click on an on-window element did not reach the button")
	}
}
