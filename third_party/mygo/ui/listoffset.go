package ui

// ScrollToOffset scrolls the list to show its content from y DIPs, as a
// scroll container's ScrollState holds: for an app animating a scroll of
// its own by calling this each frame, since ScrollTo goes to a row at
// once. The list keeps the offset within its content, as it does the
// offset of a wheel.
func (s *ListState) ScrollToOffset(y float64) {
	s.req = listRequest{set: true, offset: true, y: max(0, y)}
	s.scrolled = true
}

// Offset returns how far the list shows its content from its start, in
// DIPs, as the last layout left it: where ScrollToOffset put it, or the
// user scrolled it.
func (s *ListState) Offset() float64 { return s.wroteY }
