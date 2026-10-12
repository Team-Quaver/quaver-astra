package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseChannel(t *testing.T) {
	cases := map[string]Channel{
		"":           Stable,
		"stable":     Stable,
		"Stable":     Stable,
		"whatever":   Stable,
		"snapshot":   Snapshot,
		" SNAPSHOT ": Snapshot,
		"prerelease": Snapshot,
		"nightly":    Snapshot,
	}
	for in, want := range cases {
		if got := ParseChannel(in); got != want {
			t.Errorf("ParseChannel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRelease(t *testing.T) {
	stable := &Release{Tag: "v1.2.3", Name: "Quaver Astra 1.2.3"}
	stable.normalize()
	if stable.Channel != Stable || stable.Version != "1.2.3" || stable.SHA != "" {
		t.Fatalf("stable normalized = %+v", stable)
	}

	snap := &Release{Tag: "snapshot-abcdef123456", Name: "Snapshot 1.2.3-abcdef123456-snapshot"}
	snap.normalize()
	if snap.Channel != Snapshot || snap.Version != "1.2.3-abcdef123456-snapshot" || snap.SHA != "abcdef123456" {
		t.Fatalf("snapshot normalized = %+v", snap)
	}

	// 标题缺失时回落 tag。
	bare := &Release{Tag: "snapshot-deadbeef", Name: "snapshot-deadbeef"}
	bare.normalize()
	if bare.Version != "snapshot-deadbeef" || bare.SHA != "deadbeef" {
		t.Fatalf("bare snapshot normalized = %+v", bare)
	}
}

func TestParseCoreAndSnapshotSHA(t *testing.T) {
	cases := []struct {
		in   string
		want Core
		ok   bool
	}{
		{"v1.2.3", Core{1, 2, 3}, true},
		{"1.2.3-abcdef-snapshot", Core{1, 2, 3}, true},
		{"1.2", Core{1, 2, 0}, true},
		{"2", Core{2, 0, 0}, true},
		{"dev", Core{}, false},
		{"", Core{}, false},
	}
	for _, c := range cases {
		got, ok := ParseCore(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseCore(%q) = %+v,%v want %+v,%v", c.in, got, ok, c.want, c.ok)
		}
	}

	if sha := SnapshotSHA("1.2.3-abcdef123456-snapshot"); sha != "abcdef123456" {
		t.Errorf("SnapshotSHA = %q", sha)
	}
	if sha := SnapshotSHA("1.2.3"); sha != "" {
		t.Errorf("SnapshotSHA(stable) = %q", sha)
	}
	if !IsSnapshot("1.2.3-abcdef-snapshot") || IsSnapshot("1.2.3") {
		t.Error("IsSnapshot mismatch")
	}
}

func TestRelate(t *testing.T) {
	stable := func(v string) *Release {
		r := &Release{Tag: "v" + v, Name: "x"}
		r.normalize()
		return r
	}
	snap := func(v, sha string) *Release {
		r := &Release{Tag: "snapshot-" + sha, Name: "Snapshot " + v}
		r.normalize()
		return r
	}
	cases := []struct {
		name    string
		current string
		target  *Release
		want    Relation
	}{
		{"same stable", "1.2.3", stable("1.2.3"), RelationSame},
		{"newer stable", "1.2.3", stable("1.3.0"), RelationNewer},
		{"older stable", "1.3.0", stable("1.2.3"), RelationOlder},
		{"dev always available", "dev", stable("1.2.3"), RelationNewer},
		{"stable to snapshot same core", "1.2.3", snap("1.2.3-abcdef-snapshot", "abcdef"), RelationSwitch},
		{"snapshot same build", "1.2.3-abcdef-snapshot", snap("1.2.3-abcdef-snapshot", "abcdef"), RelationSame},
		{"snapshot new build same core", "1.2.3-abcdef-snapshot", snap("1.2.3-999999-snapshot", "999999"), RelationSwitch},
		{"snapshot newer core", "1.2.3-abcdef-snapshot", snap("1.3.0-999999-snapshot", "999999"), RelationNewer},
		{"snapshot back to stable", "1.2.3-abcdef-snapshot", stable("1.2.3"), RelationSwitch},
		{"nil target", "1.2.3", nil, RelationUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Relate(c.current, c.target); got != c.want {
				t.Fatalf("Relate(%q) = %v, want %v", c.current, got, c.want)
			}
		})
	}
}

func TestRelationAvailable(t *testing.T) {
	for _, r := range []Relation{RelationNewer, RelationSwitch, RelationOlder} {
		if !r.Available() {
			t.Errorf("%v should be available", r)
		}
	}
	for _, r := range []Relation{RelationSame, RelationUnknown} {
		if r.Available() {
			t.Errorf("%v should not be available", r)
		}
	}
}

func TestAssetFor(t *testing.T) {
	r := &Release{Assets: []Asset{
		{Name: "Quaver-Astra-1.2.3-linux-amd64.AppImage"},
		{Name: "Quaver-Astra-1.2.3-linux-arm64.AppImage"},
		{Name: "Quaver-Astra-1.2.3-linux-loong64.tar.gz"},
		{Name: "Quaver-Astra-1.2.3-windows-amd64.zip"},
		{Name: "Quaver-Astra-1.2.3-windows-arm64.zip"},
		{Name: "Quaver-Astra-1.2.3-macos-arm64.dmg"},
		{Name: "LICENSE"},
	}}
	cases := []struct {
		goos, goarch, want string
		ok                 bool
	}{
		{"linux", "amd64", "Quaver-Astra-1.2.3-linux-amd64.AppImage", true},
		{"linux", "arm64", "Quaver-Astra-1.2.3-linux-arm64.AppImage", true},
		{"linux", "loong64", "Quaver-Astra-1.2.3-linux-loong64.tar.gz", true},
		{"windows", "amd64", "Quaver-Astra-1.2.3-windows-amd64.zip", true},
		{"windows", "arm64", "Quaver-Astra-1.2.3-windows-arm64.zip", true},
		{"darwin", "arm64", "Quaver-Astra-1.2.3-macos-arm64.dmg", true},
		{"darwin", "amd64", "", false},
		{"plan9", "amd64", "", false},
	}
	for _, c := range cases {
		got, ok := r.AssetFor(c.goos, c.goarch)
		if ok != c.ok || got.Name != c.want {
			t.Errorf("AssetFor(%s/%s) = %q,%v want %q,%v", c.goos, c.goarch, got.Name, ok, c.want, c.ok)
		}
	}
}

func TestLatestStable(t *testing.T) {
	c := stubClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/Team-Quaver/quaver-astra/releases/latest" {
			t.Errorf("unexpected path %s", r.URL.Path)
			return httpResponse(http.StatusNotFound, []byte("{}"), nil), nil
		}
		return jsonResponse(http.StatusOK, Release{
			Tag:         "v1.4.0",
			Name:        "Quaver Astra 1.4.0",
			PublishedAt: time.Now(),
			Assets:      []Asset{{Name: "Quaver-Astra-1.4.0-linux-amd64.AppImage"}},
		}), nil
	})
	rel, err := c.Latest(context.Background(), Stable)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.4.0" || rel.Channel != Stable {
		t.Fatalf("rel = %+v", rel)
	}
}

func TestLatestStableNone(t *testing.T) {
	c := stubClient(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
	})
	if _, err := c.Latest(context.Background(), Stable); err != ErrNoRelease {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
}

func TestLatestSnapshotPicksNewest(t *testing.T) {
	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	list := []Release{
		{Tag: "snapshot-old", Name: "Snapshot 1.0.0-old-snapshot", Prerelease: true, PublishedAt: older},
		{Tag: "v1.2.3", Name: "Quaver Astra 1.2.3", PublishedAt: newer}, // 稳定版混在列表里
		{Tag: "snapshot-new", Name: "Snapshot 1.2.3-new-snapshot", Prerelease: true, PublishedAt: newer},
	}
	c := stubClient(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.Path, "/repos/Team-Quaver/quaver-astra/releases") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, list), nil
	})
	rel, err := c.Latest(context.Background(), Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "snapshot-new" || rel.SHA != "new" {
		t.Fatalf("snapshot = %+v", rel)
	}
}

func TestLatestSnapshotNone(t *testing.T) {
	c := stubClient(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, []Release{{Tag: "v1.2.3", Name: "Quaver Astra 1.2.3"}}), nil
	})
	if _, err := c.Latest(context.Background(), Snapshot); err != ErrNoRelease {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
}

func TestDownloadVerifiesDigest(t *testing.T) {
	payload := []byte("fake-appimage-bytes")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	c := stubClient(func(r *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, payload, nil), nil
	})

	dst := filepath.Join(t.TempDir(), "out.AppImage")
	a := Asset{URL: "https://example.test/asset", Size: int64(len(payload)), Digest: digest}
	var last int64
	if err := c.Download(context.Background(), a, dst, func(done, total int64) {
		last = done
		if total != int64(len(payload)) {
			t.Errorf("total = %d", total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(payload)) {
		t.Errorf("progress last = %d", last)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded %q", got)
	}
}

func TestDownloadDigestMismatch(t *testing.T) {
	payload := []byte("tampered")
	c := stubClient(func(r *http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, payload, nil), nil
	})

	dst := filepath.Join(t.TempDir(), "out.AppImage")
	a := Asset{URL: "https://example.test/asset", Size: int64(len(payload)), Digest: "sha256:" + strings.Repeat("0", 64)}
	if err := c.Download(context.Background(), a, dst, nil); err == nil {
		t.Fatal("expected digest mismatch error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("output should not exist after failed download")
	}
}

// ===== 测试桩：不占端口，用可注入的 RoundTripper 伪造 GitHub 响应 =====

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubClient(rt roundTripFunc) *Client {
	return &Client{Repo: Repo, APIBase: "https://api.test", HTTP: &http.Client{Transport: rt}}
}

func jsonResponse(status int, v any) *http.Response {
	body, _ := json.Marshal(v)
	return httpResponse(status, body, map[string]string{"Content-Type": "application/json"})
}

func httpResponse(status int, body []byte, extra map[string]string) *http.Response {
	header := http.Header{}
	for k, v := range extra {
		header.Set(k, v)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
