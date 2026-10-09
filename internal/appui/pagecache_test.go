package appui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOrCreatePageEvictsLeastRecentlyUsed(t *testing.T) {
	m := map[int]int{}
	var order []int
	for i := 1; i <= pageCacheLimit+1; i++ {
		value := i
		getOrCreatePage(m, &order, i, func() int { return value })
	}
	if len(m) != pageCacheLimit {
		t.Fatalf("cache size = %d, want %d", len(m), pageCacheLimit)
	}
	if _, ok := m[1]; ok {
		t.Fatal("oldest page was not evicted")
	}

	getOrCreatePage(m, &order, 2, func() int { t.Fatal("unexpected create"); return 0 })
	getOrCreatePage(m, &order, 3, func() int { t.Fatal("unexpected create"); return 0 })
	// Inserting a new page now should evict 4, not the recently touched 2/3.
	getOrCreatePage(m, &order, 99, func() int { return 99 })
	if _, ok := m[4]; ok {
		t.Fatal("least recently used page was not evicted")
	}
	for _, key := range []int{2, 3, 99} {
		if _, ok := m[key]; !ok {
			t.Fatalf("recent page %d was evicted", key)
		}
	}
}

func TestWithAPILimitCapsConcurrentRequests(t *testing.T) {
	var app App
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < uiRequestLimit*3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.withAPILimit(func() {
				now := active.Add(1)
				defer active.Add(-1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				time.Sleep(time.Millisecond)
			})
		}()
	}
	wg.Wait()

	if got := peak.Load(); got > uiRequestLimit {
		t.Fatalf("peak concurrent requests = %d, limit = %d", got, uiRequestLimit)
	}
	if got := peak.Load(); got == 0 {
		t.Fatal("request limiter never ran a call")
	}
}
