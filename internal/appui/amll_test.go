package appui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/conf"
)

func TestFetchAMLLUsesQQMidPathAndReportsNotFound(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/qq-lyrics/001abc.ttml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(testTTML))
	}))
	defer srv.Close()

	src, err := fetchAMLLTTML(srv.URL, "001abc")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/qq-lyrics/001abc.ttml" || src == "" {
		t.Fatalf("path=%q source bytes=%d", gotPath, len(src))
	}
	if _, err := fetchAMLLTTML(srv.URL, "missing"); !errors.Is(err, errAMLLNotFound) {
		t.Fatalf("missing file error = %v, want errAMLLNotFound", err)
	}
}

func TestLoadAMLLPrefersLocalCache(t *testing.T) {
	app := &App{Conf: conf.Open(t.TempDir())}
	path := app.amllCachePath("001abc")
	if path == "" {
		t.Fatal("cache path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testTTML), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, source, err := app.loadAMLL("001abc")
	if err != nil {
		t.Fatal(err)
	}
	if source != "cache" || len(lines) != 2 {
		t.Fatalf("loaded source=%q lines=%d", source, len(lines))
	}
}
