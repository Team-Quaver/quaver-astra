package native

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNoteImageLoader(t *testing.T) {
	var pngData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/logo.png" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Accept"); got != "image/*" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngData.Bytes())
	}))
	defer srv.Close()

	src, err := noteImageLoader(srv.URL + "/assets/")("logo.png")
	if err != nil {
		t.Fatal(err)
	}
	bitmap, ok := src.(*ui.Bitmap)
	if !ok {
		t.Fatalf("loaded %T, want *ui.Bitmap", src)
	}
	if w, h := bitmap.Size(); w != 2 || h != 2 {
		t.Fatalf("image is %dx%d, want 2x2", w, h)
	}

	if _, err := noteImageLoader(srv.URL + "/assets/")("missing.png"); err == nil {
		t.Fatal("a 404 image loaded")
	}
	if _, err := noteImageLoader("")("logo.png"); err == nil {
		t.Fatal("a relative image loaded without a base URL")
	}
	if _, err := noteImageLoader(srv.URL)("file:///etc/passwd"); err == nil {
		t.Fatal("a file URL loaded")
	}
}
