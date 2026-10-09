package appui

import "testing"

type fakeSleepInhibitor struct {
	acquires  int
	releases  int
	acquireOK bool
	err       error
}

func (f *fakeSleepInhibitor) Acquire() (bool, error) {
	f.acquires++
	if f.err != nil {
		return true, f.err
	}
	f.acquireOK = true
	return true, nil
}

func (f *fakeSleepInhibitor) Release() {
	f.releases++
	f.acquireOK = false
}

func TestSleepBlockerIsIdempotent(t *testing.T) {
	inhibitor := &fakeSleepInhibitor{}
	b := newSleepBlocker(inhibitor)

	b.set(true)
	b.set(true)
	if inhibitor.acquires != 1 || inhibitor.releases != 0 {
		t.Fatalf("重复持有: acquires=%d releases=%d", inhibitor.acquires, inhibitor.releases)
	}
	b.set(false)
	b.set(false)
	if inhibitor.acquires != 1 || inhibitor.releases != 1 {
		t.Fatalf("重复释放: acquires=%d releases=%d", inhibitor.acquires, inhibitor.releases)
	}
	b.set(true)
	if inhibitor.acquires != 2 || inhibitor.releases != 1 {
		t.Fatalf("恢复后应可重新持有: acquires=%d releases=%d", inhibitor.acquires, inhibitor.releases)
	}
	b.close()
	b.set(true)
	if inhibitor.acquires != 2 || inhibitor.releases != 2 {
		t.Fatalf("close 后不应重新持有: acquires=%d releases=%d", inhibitor.acquires, inhibitor.releases)
	}
}
