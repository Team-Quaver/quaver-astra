package appui

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
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
