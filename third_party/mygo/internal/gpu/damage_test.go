package gpu

import (
	"image"
	"testing"
)

func TestAges(t *testing.T) {
	var forgot []uintptr
	a := Ages{Forget: func(id uintptr) { forgot = append(forgot, id) }}
	caret := image.Rect(10, 10, 12, 30)
	clock := image.Rect(100, 0, 140, 20)
	// Three buffers in turn, each new: drawn whole.
	for id := uintptr(1); id <= 3; id++ {
		a.Add(caret, false)
		if _, known := a.Take(id); known {
			t.Fatalf("buffer %d, new, is known", id)
		}
	}
	// Buffer 1 lacks the two frames drawn since it was: the caret's.
	a.Add(clock, false)
	if got, known := a.Take(1); !known || got != caret.Union(clock) {
		t.Errorf("buffer 1 lacks %v (known %v), want %v", got, known, caret.Union(clock))
	}
	// Buffer 2 lacks the frame drawn into 3, the clock's and nothing.
	a.Add(image.Rectangle{}, false)
	if got, known := a.Take(2); !known || got != caret.Union(clock) {
		t.Errorf("buffer 2 lacks %v (known %v), want %v", got, known, caret.Union(clock))
	}
	// A frame changing everything makes every buffer drawn whole.
	a.Add(image.Rectangle{}, true)
	if _, known := a.Take(3); known {
		t.Error("buffer 3 is known after a frame changing everything")
	}
	a.Add(caret, false)
	if _, known := a.Take(1); known {
		t.Error("buffer 1 is known after a frame changing everything")
	}
	// A fifth buffer takes the place of the one drawn least recently.
	a.Take(4)
	a.Take(5)
	if len(forgot) != 1 || forgot[0] != 2 {
		t.Errorf("forgot %v, want [2]", forgot)
	}
	a.Reset()
	if len(forgot) != 5 || a.Has(1) {
		t.Errorf("after Reset, forgot %v and tracks buffer 1: %v", forgot, a.Has(1))
	}
}

func TestPartial(t *testing.T) {
	// Within the frame.
	if sc, ok := Partial(image.Rect(-5, 10, 20, 30), 100, 100, nil); !ok || sc != (Scissor{0, 10, 20, 30}) {
		t.Errorf("Partial = %v %v", sc, ok)
	}
	// Half the frame or more is drawn whole.
	if _, ok := Partial(image.Rect(0, 0, 100, 50), 100, 100, nil); ok {
		t.Error("half the frame drawn in part")
	}
	// Grown until what it grows by is in it: a backdrop from 40 to 60
	// meets the area, an effect reading one from 0 to 45 then meets that.
	grow := func(d []image.Rectangle) []image.Rectangle {
		for _, b := range []image.Rectangle{image.Rect(40, 0, 60, 10), image.Rect(0, 0, 45, 10)} {
			if d[0].Overlaps(b) {
				d = append(d, b)
			}
		}
		return d
	}
	if sc, ok := Partial(image.Rect(50, 0, 55, 5), 100, 100, grow); !ok || sc != (Scissor{0, 0, 60, 10}) {
		t.Errorf("grown: %v %v, want {0 0 60 10}", sc, ok)
	}
}
