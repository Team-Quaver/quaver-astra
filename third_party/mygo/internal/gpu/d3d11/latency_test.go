//go:build windows

package d3d11

import (
	"testing"

	"github.com/egoist/mygo/internal/wait"
)

var (
	procCreateSemaphore  = kernel32.NewProc("CreateSemaphoreW")
	procReleaseSemaphore = kernel32.NewProc("ReleaseSemaphore")
)

// TestFrameLatencyTokens checks the frame latency object's tokens, with a
// semaphore standing in for DXGI's: a present is ready when the object is
// signaled, holding its token until a present takes it; otherwise a
// goroutine waits for it and calls on; a present drawn at once waits for
// that goroutine; and each present takes one, so the count stays the
// frames the screen can take.
func TestFrameLatencyTokens(t *testing.T) {
	h, _, err := procCreateSemaphore.Call(0, 0, 8, 0)
	if h == 0 {
		t.Fatal(err)
	}
	l := latency{handle: h}
	defer l.release()
	signal := func() { procReleaseSemaphore.Call(h, 1, 0) }

	if l.ready() {
		t.Fatal("ready before the screen took a frame")
	}
	woke := make(chan struct{}, 2)
	l.waitThen(func() { woke <- struct{}{} })
	l.waitThen(func() { woke <- struct{}{} }) // the same wait
	if l.ready() {
		t.Fatal("ready while waiting")
	}
	signal()
	wait.For(t, "the wait's end", woke)
	if len(woke) != 0 {
		t.Fatal("one wait called on twice")
	}
	if !l.ready() || !l.ready() {
		t.Fatal("not ready with the token")
	}
	// A present takes it: the next is not ready.
	l.take()
	if l.ready() {
		t.Fatal("ready after a present took the token")
	}
	// A present drawn at once waits for the goroutine waiting.
	l.waitThen(nil)
	go signal()
	l.take()
	if l.token || l.waiting {
		t.Fatalf("after a present drawn at once: token %v, waiting %v", l.token, l.waiting)
	}
	// Signaled when asked: ready at once, with no goroutine.
	signal()
	if !l.ready() || l.waiting {
		t.Fatal("not ready at once when signaled")
	}
	l.take()
	// Every success taken: the count is back to none.
	if l.waitObject(0) {
		t.Fatal("the object's count drifted")
	}
}
