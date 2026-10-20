//go:build linux && (amd64 || arm64)

package gl

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/egoist/mygo/internal/gpu/gputest"
	"github.com/egoist/mygo/internal/scene"
)

// TestPartialFramesMatchWhole draws random scenes changing a little at a
// time into a framebuffer that keeps what was drawn, as a GtkGLArea's,
// each frame only where it changed, and requires the bytes of each frame
// drawn whole, over an opaque color and a translucent one. Now and then
// the framebuffer loses what it held, as GTK makes the area's buffers
// anew, which Invalidate tells.
func TestPartialFramesMatchWhole(t *testing.T) {
	seeds := uint64(12)
	if testing.Short() {
		seeds = 6
	}
	for _, api := range apis {
		t.Run(api.name, func(t *testing.T) {
			read := offscreen(t, api.es, 320, 240)
			var outer int32
			glGetIntegerv(glFramebufferBinding, &outer)
			// The frames drawn whole go into a framebuffer of their own.
			tex := newTexture(glRGBA8, glRGBA, 320, 240, nil, 0)
			var fb uint32
			glGenFramebuffers(1, &fb)
			glBindFramebuffer(glFramebuffer, fb)
			glFramebufferTexture2D(glFramebuffer, glColorAttachment0, glTexture2D, tex, 0)
			defer func() {
				glDeleteFramebuffers(1, &fb)
				glDeleteTextures(1, &tex)
			}()
			frames, partial := 0, 0
			for seed := range seeds {
				m := gputest.NewChanging(seed)
				if seed%3 == 2 {
					m.Clear = scene.Color{R: 20, G: 40, B: 200, A: 77}
				}
				s := m.Scene()
				// Renderers of their own, as each scene has an atlas of its own.
				r, err := New()
				if err != nil {
					t.Fatal(err)
				}
				r.Partial = true
				whole, err := New()
				if err != nil {
					t.Fatal(err)
				}
				for step := range 30 {
					what := "first"
					if step > 0 {
						what = m.Change(s)
					}
					if step%11 == 10 {
						// GTK made the buffers anew: what they hold is undefined.
						glBindFramebuffer(glFramebuffer, uint32(outer))
						glClearColor(1, 0, 1, 1)
						glClear(glColorBufferBit)
						r.Invalidate()
						what += ", invalidated"
					}
					glBindFramebuffer(glFramebuffer, uint32(outer))
					if err := r.Render(s); err != nil {
						t.Fatal(err)
					}
					got := read()
					frames++
					part := "whole"
					if r.redrew != redrewWhole {
						partial++
						part = fmt.Sprint(r.redrewPart)
					}
					glBindFramebuffer(glFramebuffer, fb)
					if err := whole.Render(s); err != nil {
						t.Fatal(err)
					}
					want := read()
					if !bytes.Equal(got, want) {
						n := 0
						for i := range got {
							if got[i] != want[i] {
								n++
							}
						}
						t.Fatalf("seed %d, step %d (%s), redrew %s: %d bytes differ from the frame drawn whole", seed, step, what, part, n)
					}
				}
				r.Release()
				whole.Release()
			}
			if partial < frames/3 {
				t.Errorf("%d frames of %d drawn in part", partial, frames)
			}
		})
	}
}
