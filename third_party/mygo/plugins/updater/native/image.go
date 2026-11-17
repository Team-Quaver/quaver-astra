package native

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const maxNoteImage = 16 << 20

var noteImageClient = &http.Client{Timeout: 20 * time.Second}

// noteImageLoader loads images of release notes over HTTP or HTTPS, with
// relative URLs resolved against base.
func noteImageLoader(base string) func(string) (ui.ImageSource, error) {
	return func(raw string) (ui.ImageSource, error) {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		if !u.IsAbs() {
			b, err := url.Parse(base)
			if err != nil || b.Scheme == "" {
				return nil, fmt.Errorf("image %q is not absolute and there is no base URL", raw)
			}
			u = b.ResolveReference(u)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
		default:
			return nil, errors.New("release note images must use HTTP or HTTPS")
		}

		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "quaver-astra-updater")
		req.Header.Set("Accept", "image/*")
		resp, err := noteImageClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("loading image: %s", resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxNoteImage+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxNoteImage {
			return nil, fmt.Errorf("image is larger than %d MiB", maxNoteImage>>20)
		}
		return ui.DecodeImage(data)
	}
}
