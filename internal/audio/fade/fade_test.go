package fade

import (
	"sync"
	"testing"
	"time"
)

func TestPresetDurations(t *testing.T) {
	if got := Normalize("bogus"); got != DefaultPreset {
		t.Fatalf("Normalize(bogus) = %q, want %q", got, DefaultPreset)
	}
	cases := []struct {
		key string
		in  time.Duration
		out time.Duration
	}{
		{"off", 0, 0},
		{"short", 150 * time.Millisecond, 120 * time.Millisecond},
		{"normal", 400 * time.Millisecond, 250 * time.Millisecond},
		{"long", 800 * time.Millisecond, 500 * time.Millisecond},
	}
	for _, c := range cases {
		in, out := Durations(c.key)
		if in != c.in || out != c.out {
			t.Errorf("%s = (%v, %v), want (%v, %v)", c.key, in, out, c.in, c.out)
		}
	}
	labels := make([]string, 0, len(Presets))
	for _, p := range Presets {
		labels = append(labels, p.Label)
	}
	want := []string{"0s", "0.15s", "0.4s", "0.8s"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("labels = %v, want %v", labels, want)
		}
	}
}

func TestEnvelopeRampAndPending(t *testing.T) {
	var mu sync.Mutex
	applied := 1.0
	env := New(func(v float64) {
		mu.Lock()
		applied = v
		mu.Unlock()
	})
	defer env.Close()

	done := make(chan struct{}, 1)
	env.RampTo(0, 120*time.Millisecond, func() { done <- struct{}{} })

	time.Sleep(50 * time.Millisecond)
	if got := env.Level(); got <= 0.05 || got >= 0.95 {
		t.Fatalf("level mid-fade = %v, want between 0.05 and 0.95", got)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pending action never ran")
	}
	if got := env.Level(); got != 0 {
		t.Fatalf("level after fade = %v, want 0", got)
	}
	mu.Lock()
	last := applied
	mu.Unlock()
	if last != 0 {
		t.Fatalf("applied gain = %v, want 0", last)
	}
}

func TestEnvelopeNewIntentCancelsPending(t *testing.T) {
	env := New(nil)
	defer env.Close()

	fired := make(chan struct{}, 1)
	env.RampTo(0, 120*time.Millisecond, func() { fired <- struct{}{} })
	env.RampTo(1, 0, nil)

	select {
	case <-fired:
		t.Fatal("stale fade-out action ran after a new intent")
	case <-time.After(250 * time.Millisecond):
	}
	if got := env.Level(); got != 1 {
		t.Fatalf("level = %v, want 1", got)
	}
}

func TestEnvelopeBeginLoadClearsPendingStop(t *testing.T) {
	env := New(nil)
	defer env.Close()

	fired := make(chan struct{}, 1)
	env.RampTo(0, 120*time.Millisecond, func() { fired <- struct{}{} })
	time.Sleep(40 * time.Millisecond)
	env.BeginLoad()

	select {
	case <-fired:
		t.Fatal("the delayed stop must not run after a new load begins")
	case <-time.After(250 * time.Millisecond):
	}
	if got := env.Target(); got != 0 {
		t.Fatalf("target after load = %v, want 0", got)
	}
}

func TestEnvelopeFinishFadeOutRunsPendingNow(t *testing.T) {
	env := New(nil)
	defer env.Close()

	done := make(chan struct{}, 1)
	env.RampTo(0, time.Second, func() { done <- struct{}{} })
	time.Sleep(30 * time.Millisecond)
	env.FinishFadeOut()

	select {
	case <-done:
	default:
		t.Fatal("finish fade-out did not run the pending action")
	}
	if got := env.Level(); got != 0 {
		t.Fatalf("level = %v, want 0", got)
	}
}
