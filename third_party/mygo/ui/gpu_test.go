package ui

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/surface"
	"github.com/egoist/mygo/transfer"
)

// testSurface is a surface that counts the frames drawn in memory.
type testSurface struct{ pixels int }

func (s *testSurface) Native() platform.SurfaceNative           { return platform.SurfaceNative{HWND: 1} }
func (s *testSurface) Size() (float64, float64, float64)        { return 200, 100, 1 }
func (s *testSurface) RequestFrame()                            {}
func (s *testSurface) RefreshRate() float64                     { return 60 }
func (s *testSurface) PresentPixels([]byte, int, int, int)      { s.pixels++ }
func (s *testSurface) SetCursor(platform.Cursor)                {}
func (s *testSurface) SetTextInput(platform.TextInputState)     {}
func (s *testSurface) UpdateAccessibility(*platform.AccessTree) {}
func (s *testSurface) StartDataDrag(r platform.DragRequest) {
	r.Done(transfer.Result{Err: platform.ErrUnsupported})
}
func (s *testSurface) CancelDataDrag()                  {}
func (s *testSurface) SetDropFormats([]transfer.Format) {}
func (s *testSurface) NewWebView(*platform.WindowOptions, platform.WindowHandler) (platform.WebView, error) {
	return nil, platform.ErrUnsupported
}
func (s *testSurface) PlaceWebViews([]platform.WebViewPlacement) {}

// testGPU is a GPU renderer whose device goes away when fail is set.
type testGPU struct {
	fail             bool
	frames, released int
}

func (g *testGPU) Render(*scene.Scene) error {
	if g.fail {
		return errors.New("the device was removed")
	}
	g.frames++
	return nil
}

func (g *testGPU) Release() { g.released++ }

// gpuHost returns a window host on a test surface whose GPU renderers
// newGPU makes, and a function drawing a frame.
func gpuHost(t *testing.T, make func() (gpuRenderer, error)) (*windowHost, *testSurface, func()) {
	t.Setenv("MYGO_GPU", "")
	newGPU = func(platform.SurfaceNative) (gpuRenderer, error) { return make() }
	t.Cleanup(func() { newGPU = newGPURenderer })
	s := &testSurface{}
	h := &windowHost{conn: &surface.Conn{Surface: s}}
	h.rt = newRuntime(func(c *context) { coreText(c, "Hello") }, h)
	return h, s, func() { h.event(platform.SurfaceEvent{Kind: platform.SurfaceFrame}) }
}

// softGPU is a renderer drawing on the CPU, as Direct3D's WARP.
type softGPU struct{ testGPU }

func (g *softGPU) Software() bool { return true }

func TestGPUComesBack(t *testing.T) {
	var made []*testGPU
	gone := false
	h, s, frame := gpuHost(t, func() (gpuRenderer, error) {
		if gone {
			return nil, errors.New("no device")
		}
		g := &testGPU{}
		made = append(made, g)
		return g, nil
	})
	frame()
	if len(made) != 1 || made[0].frames != 1 || s.pixels != 0 {
		t.Fatalf("first frame: %d renderers, %d frames in memory", len(made), s.pixels)
	}

	// A renderer that fails gives way to another at once.
	made[0].fail = true
	frame()
	if made[0].released != 1 || len(made) != 2 || made[1].frames != 1 || s.pixels != 0 {
		t.Fatalf("after a failure: released %d, %d renderers, %d frames in memory", made[0].released, len(made), s.pixels)
	}

	// Without a device, frames are drawn in memory until the wait is over.
	gone = true
	made[1].fail = true
	frame()
	frame()
	if len(made) != 2 || s.pixels != 2 || h.backoff != time.Second {
		t.Fatalf("without a device: %d renderers, %d frames in memory, wait %v", len(made), s.pixels, h.backoff)
	}
	// None either when the wait is over: the next wait is longer.
	h.retryAt = time.Now()
	frame()
	if s.pixels != 3 || h.backoff != 2*time.Second || h.retryAt.IsZero() {
		t.Fatalf("still without a device: %d frames in memory, wait %v", s.pixels, h.backoff)
	}
	// It is back.
	gone = false
	h.retryAt = time.Now()
	frame()
	if len(made) != 3 || made[2].frames != 1 || s.pixels != 3 || !h.retryAt.IsZero() {
		t.Fatalf("once back: %d renderers, %d frames in memory", len(made), s.pixels)
	}
}

func TestSoftwareUntilTheGPUIsBack(t *testing.T) {
	onGPU := true
	var gpus []*testGPU
	var soft *softGPU
	h, s, frame := gpuHost(t, func() (gpuRenderer, error) {
		if onGPU {
			g := &testGPU{}
			gpus = append(gpus, g)
			return g, nil
		}
		soft = &softGPU{}
		return soft, nil
	})
	frame()
	// The GPU goes away: a renderer drawing in software takes its place,
	// and frames still show.
	onGPU = false
	gpus[0].fail = true
	frame()
	if soft == nil || soft.frames != 1 || s.pixels != 0 || !h.degraded || h.retryAt.IsZero() {
		t.Fatalf("after the loss: software %v, %d frames in memory, degraded %v", soft != nil, s.pixels, h.degraded)
	}
	// When the wait is over, a frame tries for the GPU, still away.
	first := soft
	h.retryAt = time.Now()
	frame()
	if first.released != 1 || soft == first || soft.frames != 1 || h.backoff != 2*time.Second {
		t.Fatalf("trying again: released %d, wait %v", first.released, h.backoff)
	}
	// It is back.
	onGPU = true
	h.retryAt = time.Now()
	frame()
	if soft.released != 1 || len(gpus) != 2 || gpus[1].frames != 1 || h.degraded || !h.retryAt.IsZero() {
		t.Errorf("once back: software released %d, %d GPU renderers, degraded %v", soft.released, len(gpus), h.degraded)
	}
}

func TestNoGPUFromTheStart(t *testing.T) {
	tries := 0
	h, s, frame := gpuHost(t, func() (gpuRenderer, error) {
		tries++
		return nil, errors.New("no device")
	})
	frame()
	frame()
	if tries != 1 || s.pixels != 2 || !h.retryAt.IsZero() {
		t.Errorf("%d tries, %d frames in memory, retry at %v", tries, s.pixels, h.retryAt)
	}

	// A machine whose renderer draws in software from the start keeps it.
	tries = 0
	h, s, frame = gpuHost(t, func() (gpuRenderer, error) {
		tries++
		return &softGPU{}, nil
	})
	frame()
	frame()
	if tries != 1 || s.pixels != 0 || h.degraded || !h.retryAt.IsZero() {
		t.Errorf("software from the start: %d tries, degraded %v, retry at %v", tries, h.degraded, h.retryAt)
	}
}

// TestEveryFrameOnTheGPU checks that with a GPU, every frame draws on it:
// the first, one changing little after a pause, and one changing little
// while frames follow each other.
func TestEveryFrameOnTheGPU(t *testing.T) {
	g := &testGPU{}
	h, s, frame := gpuHost(t, func() (gpuRenderer, error) { return g, nil })
	x := float32(10)
	h.rt = newRuntime(func(c *context) {
		coreBox(c).Fill().Background(RGB(200, 200, 200)).Children(func() {
			coreBox(c).Size(10, 10).Background(RGB(0, 0, 255)).Absolute().Left(x).Top(10)
		})
	}, h)
	frame()
	h.lastFrame = time.Now().Add(-time.Second)
	x = 30
	frame()
	x = 40
	frame()
	if g.frames != 3 || s.pixels != 0 || h.path != "drawn on the GPU" {
		t.Errorf("%d frames on the GPU, %d drawn in memory, the last %q", g.frames, s.pixels, h.path)
	}
}

func TestTransformedFramesStayOnGPU(t *testing.T) {
	g := &testGPU{}
	h, s, frame := gpuHost(t, func() (gpuRenderer, error) { return g, nil })
	active := true
	h.rt = newRuntime(func(c *context) {
		coreBox(c).Size(30, 20).Translate(10, 10).Rotate(8).Background(RGB(50, 80, 200))
		if active {
			c.AnimationFrame()
		}
	}, h)
	frame()
	frame()
	active = false
	h.lastFrame = time.Now().Add(-time.Second)
	frame()
	if g.frames != 3 || s.pixels != 0 || h.soft.Image.Pix != nil {
		t.Fatal("active or resting transforms switched to CPU rendering")
	}
}

// A painter tells whether its frame draws on a GPU: from the first frame,
// whose renderer is made as it paints; not with a renderer drawing in
// software, nor in memory, nor headless. When that changes as a frame is
// presented, the window is painted again.
func TestAccelerated(t *testing.T) {
	soft, fail := false, false
	h, _, frame := gpuHost(t, func() (gpuRenderer, error) {
		switch {
		case fail:
			return nil, errors.New("no device")
		case soft:
			return &softGPU{}, nil
		}
		return &testGPU{}, nil
	})
	var told []bool
	h.rt = newRuntime(func(c *context) {
		coreBox(c).Size(20, 20).Draw(func(p *Painter, r Rect) { told = append(told, p.Accelerated()) })
	}, h)
	invalidated := 0
	h.conn.Invalidate = func() { invalidated++ }
	frame()
	if len(told) != 1 || !told[0] || invalidated != 0 {
		t.Fatalf("on a GPU: told %v, invalidated %d times", told, invalidated)
	}

	// The GPU goes away as a frame presents: the next is painted without.
	h.gpu.(*testGPU).fail, fail = true, true
	frame()
	if invalidated != 1 || h.gpu != nil {
		t.Fatalf("after the loss: invalidated %d times, renderer %v", invalidated, h.gpu)
	}
	told = told[:0]
	frame()
	if len(told) != 1 || told[0] || invalidated != 1 {
		t.Fatalf("in memory: told %v, invalidated %d times", told, invalidated)
	}

	// A renderer drawing in software, as WARP, is not a GPU.
	h.event(platform.SurfaceEvent{Kind: platform.SurfaceRenew})
	soft, fail = true, false
	told = told[:0]
	frame()
	if len(told) != 1 || told[0] {
		t.Errorf("in software: told %v", told)
	}

	var headless []bool
	coreRender(func(c *context) {
		coreBox(c).Size(20, 20).Draw(func(p *Painter, r Rect) { headless = append(headless, p.Accelerated()) })
	}, 40, 40, 1)
	if len(headless) == 0 || slices.Contains(headless, true) {
		t.Errorf("headless: told %v", headless)
	}
}

// pacedSurface is a test surface whose frames following each other are
// paced by the display, and which draws one at once on FrameNow.
type pacedSurface struct {
	testSurface
	h   *windowHost
	now int
}

func (s *pacedSurface) FrameNow() {
	s.now++
	s.h.event(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
}

// waitingGPU is a GPU renderer that has a frame wait until wake is called,
// while ready is false.
type waitingGPU struct {
	testGPU
	ready bool
	wake  func()
}

func (g *waitingGPU) Ready(wake func()) bool {
	if g.ready {
		return true
	}
	g.wake = wake
	return false
}

// TestPacedFrameWaitsForTheDisplay checks that a frame paced by the
// display waits, without drawing, for the GPU renderer to be able to draw
// it, and draws once it wakes the window, through the main thread.
func TestPacedFrameWaitsForTheDisplay(t *testing.T) {
	var gpus []*waitingGPU
	h, _, frame := gpuHost(t, func() (gpuRenderer, error) {
		g := &waitingGPU{ready: true}
		gpus = append(gpus, g)
		return g, nil
	})
	s := &pacedSurface{h: h}
	var posted []func()
	h.conn.Surface = s
	h.conn.Post = func(fn func()) { posted = append(posted, fn) }
	paced := func() { h.event(platform.SurfaceEvent{Kind: platform.SurfaceFrame, Paced: true}) }
	runPosted := func() {
		fns := posted
		posted = nil
		for _, fn := range fns {
			fn()
		}
	}
	frame()
	g := gpus[0]
	paced()
	if g.frames != 2 {
		t.Fatalf("a paced frame the renderer is ready for: %d frames, want 2", g.frames)
	}

	// Not ready: the frame waits.
	g.ready = false
	paced()
	if g.frames != 2 || g.wake == nil || !h.waiting {
		t.Fatalf("waiting: %d frames, wake %v, waiting %v", g.frames, g.wake != nil, h.waiting)
	}
	// The renderer wakes the window through the main thread, which draws
	// the frame at once.
	g.ready = true
	g.wake()
	if g.frames != 2 || len(posted) != 1 {
		t.Fatalf("woken off the main thread: %d frames, %d posted", g.frames, len(posted))
	}
	runPosted()
	if g.frames != 3 || s.now != 1 || h.waiting {
		t.Fatalf("woken: %d frames, %d drawn at once, waiting %v", g.frames, s.now, h.waiting)
	}

	// Refreshes passing while a frame waits ask for the same frame, which
	// draws at the time the first asked for it.
	g.ready = false
	paced()
	asked := h.waitedAt
	paced()
	if h.waitedAt != asked {
		t.Fatal("a later refresh changed the time of the frame waiting")
	}
	g.ready = true
	g.wake()
	runPosted()
	if g.frames != 4 || s.now != 2 || h.waiting || h.rt.c.now != asked {
		t.Fatalf("waited across refreshes: %d frames, %d drawn at once, waiting %v, frame time %v (asked %v)", g.frames, s.now, h.waiting, h.rt.c.now, asked)
	}

	// A frame the surface draws for another reason, as a resize's, draws
	// the waiting one: a wake after it draws nothing more.
	g.ready = false
	g.wake = nil
	paced()
	frame()
	if g.frames != 5 || h.waiting {
		t.Fatalf("drawn meanwhile: %d frames, waiting %v", g.frames, h.waiting)
	}
	g.wake()
	runPosted()
	if g.frames != 5 || s.now != 2 {
		t.Fatalf("a late wake: %d frames, %d drawn at once", g.frames, s.now)
	}

	// A renderer let go while a frame waits for it never wakes the window:
	// the next paced frame asks the renderer taking its place.
	paced()
	if !h.waiting {
		t.Fatal("not waiting")
	}
	g.fail = true
	frame() // fails, and another renderer draws it
	if len(gpus) != 2 || h.waiting {
		t.Fatalf("after a failure: %d renderers, waiting %v", len(gpus), h.waiting)
	}
	gpus[1].ready = false
	paced()
	if !h.waiting || h.waitingOn != gpus[1] {
		t.Fatal("the paced frame did not wait for the new renderer")
	}
	h.gpu = &waitingGPU{ready: true}
	paced()
	if h.waiting {
		t.Fatal("a paced frame still waits for a renderer let go")
	}
}
