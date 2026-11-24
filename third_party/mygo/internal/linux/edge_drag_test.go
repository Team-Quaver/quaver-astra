//go:build linux && (amd64 || arm64)

package linux

import "testing"

// TestEdgeTakesPointerWhileButtonHeld 是「拖到窗口边缘/外面后进度条卡住」
// 的回归保护：按住键拖动时，边缘属于页面，不再被缩放区吞掉；没有按键时
// 边缘仍然是窗口缩放的。
func TestEdgeTakesPointerWhileButtonHeld(t *testing.T) {
	const (
		noButton   = 0
		button1    = 1 << 8
		button3    = 1 << 10
		noEdge     = -1
		bottomEdge = 6
		leftEdge   = 3
	)
	for _, tc := range []struct {
		name  string
		edge  int32
		state uint32
		want  bool
	}{
		{"no edge, no button", noEdge, noButton, false},
		{"no edge, dragging", noEdge, button1, false},
		{"bottom edge, hovering", bottomEdge, noButton, true},
		{"bottom edge, dragging", bottomEdge, button1, false},
		{"left edge, dragging", leftEdge, button1, false},
		{"left edge, another button", leftEdge, button3, false},
	} {
		if got := edgeTakesPointer(tc.edge, tc.state); got != tc.want {
			t.Errorf("%s: edgeTakesPointer(%d, %#x) = %v, want %v", tc.name, tc.edge, tc.state, got, tc.want)
		}
	}
}
