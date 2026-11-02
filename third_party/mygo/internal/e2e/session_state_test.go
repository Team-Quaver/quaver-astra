//go:build !js

package e2e

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"path"
	"runtime"
	"testing"
	"unicode/utf16"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/internal/wait"
)

// sessionPage serves app://localhost/session/<name>: a page titled name,
// taller than its window, with a field at the top.
func sessionPage(w http.ResponseWriter, r *http.Request) {
	name := html.EscapeString(path.Base(r.URL.Path))
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!doctype html><title>%s</title><body style="margin:0"><input id=field name=field><div style="height:5000px">%[1]s</div>`, name)
}

// visit loads a session page, types into its field and scrolls it to y.
func visit(t *testing.T, w *mygo.Window, name, typed string, y int) {
	t.Helper()
	if err := w.Page().LoadURL("app://localhost/session/" + name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, w, fmt.Sprintf(`document.title === %q && document.readyState === "complete"`, name))
	mustEval(t, w, fmt.Sprintf(`field.value = %q; scrollTo(0, %d)`, typed, y))
	waitFor(t, w, fmt.Sprintf(`scrollY === %d`, y))
}

// holds reports whether a session state holds s, in UTF-8 or UTF-16 as
// WebKit may encode it.
func holds(state []byte, s string) bool {
	u := make([]byte, 0, 2*len(s))
	for _, c := range utf16.Encode([]rune(s)) {
		u = append(u, byte(c), byte(c>>8))
	}
	return bytes.Contains(state, []byte(s)) || bytes.Contains(state, u)
}

// A page's session state brings back its back/forward list in a new
// window, with what WebKit saved: the page's scroll position and form
// field as its window was hidden, and the entry before's scroll position
// as the page left it. On Windows it brings back the page alone.
func TestSessionState(t *testing.T) {
	w := newWindow(t, mygo.WindowOptions{Title: "Session", Width: 300, Height: 200})
	visit(t, w, "one", "typed first", 700)
	visit(t, w, "two", "typed second", 1200)
	state, err := w.Page().SessionState()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// WebKit saves the form fields of the page it shows as the page
		// goes out of view, and tells it to the UI process later.
		w.Hide()
		wait.Until(t, "WebKit saving the page hidden", func() bool {
			state, err = w.Page().SessionState()
			return err == nil && holds(state, "typed second")
		})
	}
	w.Destroy()

	r := newWindow(t, mygo.WindowOptions{Title: "Session restored", Width: 300, Height: 200, URL: "app://localhost/session/wrong",
		Page: mygo.PageOptions{SessionState: state}})
	waitFor(t, r, `document.title === "two" && document.readyState === "complete"`)
	if runtime.GOOS == "windows" {
		if r.Page().CanGoBack() {
			t.Error("CanGoBack = true where WebView2 brings back the URL alone")
		}
		return
	}
	t.Logf("restored: %v", mustEval(t, r, `JSON.stringify([scrollY, field.value, history.length, innerHeight, document.body.scrollHeight, document.visibilityState])`))
	if !r.Page().CanGoBack() {
		t.Fatal("CanGoBack = false in the page restored: it loaded the URL alone")
	}
	waitFor(t, r, `scrollY === 1200`)
	waitFor(t, r, `field.value === "typed second"`)
	r.Page().GoBack()
	waitFor(t, r, `document.title === "one" && document.readyState === "complete"`)
	waitFor(t, r, `scrollY === 700`)
	if !r.Page().CanGoForward() {
		t.Error("CanGoForward = false back in the page restored")
	}
}
