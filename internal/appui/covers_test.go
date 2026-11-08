package appui

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"strings"
	"testing"

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
