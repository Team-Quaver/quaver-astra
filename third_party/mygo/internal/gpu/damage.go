package gpu

import (
	"image"
	"slices"

	"github.com/egoist/mygo/internal/scene"
)

// Partial redraws. A frame that changes little of a window (a caret
// blinking, a spinner turning, a button under the pointer) redraws only
// what changed (scene.Differ), into a buffer that holds an earlier frame:
// what changed since that one, scissored, after clearing it to the
// frame's color, over the rest kept as it was (loaded, not cleared). A
// surface's swap chain shows its buffers in turn, so a buffer the renderer
// gets holds the frame drawn into it some frames ago, as EGL's buffer age
// says: Ages keeps, for each buffer, what changed since it was drawn.

// maxAges is how many buffers Ages tracks, the most a swap chain has (a
// CAMetalLayer three) and one more.
const maxAges = 4

// Ages keeps what changed in the frames of a window since each buffer of
// its swap chain was drawn, by an identity of the buffer (the texture of a
// CAMetalLayer's drawable).
type Ages struct {
	// Forget, unless nil, is called with each buffer Ages stops tracking.
	Forget func(id uintptr)
	bufs   [maxAges]age
	n      int
	frame  uint64
}

type age struct {
	id uintptr
	// changed is what changed since the buffer was drawn, unless whole.
	changed image.Rectangle
	whole   bool
	// used is the frame the buffer was last drawn in.
	used uint64
}

// Add notes a new frame, which changed the area changed of the last, or
// everything when whole.
func (a *Ages) Add(changed image.Rectangle, whole bool) {
	a.frame++
	for i := range a.bufs[:a.n] {
		b := &a.bufs[i]
		b.whole = b.whole || whole
		b.changed = b.changed.Union(changed)
	}
}

// Take returns what changed in the frames since buffer id was drawn, the
// buffer the frame is drawn into, and false when that is unknown (the
// buffer is new, or everything may have changed): the frame draws it whole.
// It then holds the frame.
func (a *Ages) Take(id uintptr) (changed image.Rectangle, known bool) {
	for i := range a.bufs[:a.n] {
		b := &a.bufs[i]
		if b.id == id {
			changed, known = b.changed, !b.whole
			b.changed, b.whole, b.used = image.Rectangle{}, false, a.frame
			return changed, known
		}
	}
	// A new buffer takes the place of the one drawn least recently.
	i := a.n
	if a.n == maxAges {
		i = 0
		for j := range a.bufs {
			if a.bufs[j].used < a.bufs[i].used {
				i = j
			}
		}
		if a.Forget != nil {
			a.Forget(a.bufs[i].id)
		}
	} else {
		a.n++
	}
	a.bufs[i] = age{id: id, used: a.frame}
	return image.Rectangle{}, false
}

// Reset forgets every buffer, as a swap chain makes new ones.
func (a *Ages) Reset() {
	if a.Forget != nil {
		for i := range a.bufs[:a.n] {
			a.Forget(a.bufs[i].id)
		}
	}
	a.bufs, a.n = [maxAges]age{}, 0
}

// Partial returns the area of a w×h frame to redraw, given what changed
// in the buffer drawn into (Ages.Take), grown by grow (the backdrops of
// effects, scene.Differ.AddBackdrops), or false when the frame is better
// drawn whole: past half the frame, drawing it whole costs about the same.
func Partial(changed image.Rectangle, w, h int, grow func([]image.Rectangle) []image.Rectangle) (Scissor, bool) {
	frame := image.Rect(0, 0, w, h)
	area := changed.Intersect(frame)
	if area.Empty() {
		return Scissor{}, true
	}
	if grow != nil {
		var rects [1]image.Rectangle
		for {
			rects[0] = area
			grown := area
			for _, r := range grow(rects[:]) {
				grown = grown.Union(r)
			}
			grown = grown.Intersect(frame)
			if grown == area {
				break
			}
			area = grown
		}
	}
	if 2*area.Dx()*area.Dy() >= w*h {
		return Scissor{}, false
	}
	return Scissor{int32(area.Min.X), int32(area.Min.Y), int32(area.Max.X), int32(area.Max.Y)}, true
}

// Intersect returns the pixels both s and o take.
func (s Scissor) Intersect(o Scissor) Scissor { return s.intersect(o) }

// Has reports whether Ages tracks buffer id.
func (a *Ages) Has(id uintptr) bool {
	for i := range a.bufs[:a.n] {
		if a.bufs[i].id == id {
			return true
		}
	}
	return false
}

// Pipelines keeps, for each op of the frames a renderer builds, a hash of
// the pipelines (shaders, blending) its instances draw with. An op drawn
// with others than in the last frame (a batch specialized for other
// classes as batching changed around it, a pipeline made meanwhile, a
// clip cut by a mask) draws a unit in the last place apart here and
// there, which a frame drawn whole would change too: its bounds count as
// changed.
type Pipelines struct {
	keys, last []uint64
}

// Damage hashes the pipelines of the ops b built, pipe(i) being batch i's,
// and adds to damage, what changed since the last frame drawn as d found
// (unless not known), the bounds of the ops d matches with an op of the
// last frame drawn with other pipelines, and returns it.
func (p *Pipelines) Damage(b *Builder, pipe func(i int) uint64, d *scene.Differ, damage []image.Rectangle, known bool) []image.Rectangle {
	n := len(b.OpStart) - 1
	p.keys = slices.Grow(p.keys[:0], n)[:n]
	clear(p.keys)
	op := 0
	for i, bt := range b.Batches {
		lo, hi := int32(bt.Start), int32(bt.Start+bt.Count)
		for op < n && b.OpStart[op+1] <= lo {
			op++
		}
		key := pipe(i)
		for k := op; k < n && b.OpStart[k] < hi; k++ {
			if b.OpStart[k+1] > b.OpStart[k] {
				p.keys[k] = (p.keys[k] ^ key) * 1099511628211
			}
		}
	}
	if !known {
		return damage
	}
	bounds := d.Bounds()
	for j, key := range p.keys {
		if i, ok := d.Match(j); ok && i < len(p.last) && p.last[i] != key {
			damage = append(damage, bounds[j])
		}
	}
	return damage
}

// Remember keeps the hashes of the frame Damage was given last, drawn, to
// compare the next with.
func (p *Pipelines) Remember() { p.keys, p.last = p.last, p.keys }
