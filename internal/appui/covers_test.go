package appui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func coverTestCache(body string) *CoverCache {
	cc := NewCoverCache()
	cc.hc = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	return cc
}

func TestCoverFetchRejectsOversizedResponse(t *testing.T) {
	cc := coverTestCache(strings.Repeat("x", maxCoverBytes+1))
	defer cc.Close()

	data, err := cc.fetch("memory://oversized")
	if data != nil {
		t.Fatalf("oversized response returned %d bytes", len(data))
	}
	if !errors.Is(err, errCoverTooLarge) {
		t.Fatalf("fetch error = %v, want %v", err, errCoverTooLarge)
	}
}

func TestCoverFetchAcceptsLimitedResponse(t *testing.T) {
	want := []byte("small cover")
	cc := coverTestCache(string(want))
	defer cc.Close()

	got, err := cc.fetch("memory://small")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("fetch = %q, want %q", got, want)
	}
}

func TestCoverInvalidateDropsBitmapAndBlur(t *testing.T) {
	cc := NewCoverCache()
	bmp := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	const url = "http://example.test/cloud/cover/k"
	cc.mu.Lock()
	cc.m[url] = bmp
	cc.raw[url] = []byte("old")
	cc.blur[url] = bmp
	cc.inflight[url] = true
	cc.mu.Unlock()

	cc.Invalidate(url)

	cc.mu.Lock()
	_, hasBitmap := cc.m[url]
	_, hasRaw := cc.raw[url]
	_, hasBlur := cc.blur[url]
	_, busy := cc.inflight[url]
	epoch := cc.epoch[url]
	cc.mu.Unlock()
	if hasBitmap || hasRaw || hasBlur || busy {
		t.Fatalf("失效后仍有缓存：bitmap=%v raw=%v blur=%v busy=%v", hasBitmap, hasRaw, hasBlur, busy)
	}
	if epoch != 1 {
		t.Fatalf("失效代号 = %d，期望 1", epoch)
	}
}

// statusCache 让 fetch 按 URL 返回不同的状态码/内容，用来模拟
// 「1500 档 404、1200 档可用」这类真实 CDN 行为。
func statusCache(status map[string]int, body map[string]string) *CoverCache {
	cc := NewCoverCache()
	cc.hc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		code := status[r.URL.String()]
		if code == 0 {
			code = http.StatusOK
		}
		payload := body[r.URL.String()]
		return &http.Response{
			StatusCode: code,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}
	return cc
}

// TestCoverPickFallsBackFrom1500 正在播放页大封面从 1500 起取，
// 该档缺失时逐级降级，最终命中第一个可用的档位并记住它。
func TestCoverPickFallsBackFrom1500(t *testing.T) {
	sizes := []int{1500, 1200, 800, 500, 300, 150, 90}
	urls := make([]string, 0, len(sizes))
	for _, size := range sizes {
		urls = append(urls, coverURL("ap", "", size))
	}
	missing := map[string]int{urls[0]: http.StatusNotFound, urls[1]: http.StatusNotFound}
	png := string(solidCoverPNG(t, color.RGBA{R: 10, G: 20, B: 30, A: 255}))
	cc := statusCache(missing, map[string]string{urls[2]: png})
	defer cc.Close()

	if bmp := cc.GetBest(urls, nil); bmp != nil {
		t.Fatal("首次调用应异步挑选并返回 nil")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if bmp := cc.GetBest(urls, nil); bmp != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("候选链未在 3s 内定档")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cc.mu.Lock()
	picked := cc.pick[urls[0]]
	cc.mu.Unlock()
	if picked != urls[2] {
		t.Fatalf("命中档位 = %q，期望 800 档 %q", picked, urls[2])
	}
}

// TestCoverPickAllMissingCoolsDown 全部档位都 404 时记冷却，
// 不会每帧重新打一遍 CDN。
func TestCoverPickAllMissingCoolsDown(t *testing.T) {
	urls := []string{"https://x/1500", "https://x/1200"}
	cc := statusCache(map[string]int{urls[0]: http.StatusNotFound, urls[1]: http.StatusNotFound}, nil)
	defer cc.Close()

	cc.GetBest(urls, nil)
	waitCoverPick(t, cc, urls[0])
	if bmp := cc.GetBest(urls, nil); bmp != nil {
		t.Fatal("全部缺失时应返回 nil")
	}
	cc.mu.Lock()
	busy := cc.pickBusy[urls[0]]
	retry := cc.pickRetry[urls[0]]
	cc.mu.Unlock()
	if busy || retry.IsZero() {
		t.Fatalf("应进入冷却：busy=%v retry=%v", busy, retry)
	}
}

// TestCoverPickKeepsBoundedCache 1500 档解码后约 9MB，连播很多首也不能无限
// 攒着：只保留最近 coverKeep 张，旧图从缓存里退出。
func TestCoverPickKeepsBoundedCache(t *testing.T) {
	png := solidCoverPNG(t, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	cc := statusCache(nil, nil)
	defer cc.Close()

	for i := 0; i < coverKeep+3; i++ {
		key := "https://x/cover-" + strconv.Itoa(i) + "/1500"
		url := "https://x/cover-" + strconv.Itoa(i) + "/500"
		cc.hc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := strings.NewReader(string(png))
			if r.URL.String() == key {
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header)}, nil
		})}
		cc.GetBest([]string{key, url}, nil)
		waitCoverPick(t, cc, key)
	}

	cc.mu.Lock()
	kept := len(cc.picked)
	cc.mu.Unlock()
	if kept != coverKeep {
		t.Fatalf("缓存大图 %d 张，期望上限 %d", kept, coverKeep)
	}
}

func waitCoverPick(t *testing.T, cc *CoverCache, key string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		cc.mu.Lock()
		busy := cc.pickBusy[key]
		cc.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("候选链未在 3s 内结束")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
