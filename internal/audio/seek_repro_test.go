package audio

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/team-quaver/liino"
	"github.com/team-quaver/liino/pcm"
)

// TestSeekFlacHTTP 复现「拖动进度后 FLAC 报 invalid sync code」。
// 仅本地诊断：QAA_TEST_SEEK=1 go test -run TestSeekFlacHTTP -v ./internal/audio/
func TestSeekFlacHTTP(t *testing.T) {
	if os.Getenv("QAA_TEST_SEEK") != "1" {
		t.Skip("诊断用")
	}
	if os.Getenv("QAA_TEST_SEEK_SILENT") == "1" {
		silenceFFmpegLogs()
	}
	dir := t.TempDir()
	flac := filepath.Join(dir, "tone.flac")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i",
		"sine=frequency=440:sample_rate=44100:duration=180", "-c:a", "flac", flac)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	cfg := liino.Config{
		Decoder:         "ffgo",
		Driver:          "null",
		RingFrames:      pcm.DefaultSampleRate * 2,
		PrerollFrames:   pcm.ChunkFrames,
		PeriodFrames:    pcm.ChunkFrames / 2,
		MonitorInterval: 5 * time.Millisecond,
		DevicePolling:   time.Hour,
		FadeOut:         0,
		Log:             func(s string) { t.Log(s) },
	}
	e := newEngine(cfg)
	t.Cleanup(e.Close)
	if err := e.OpenURL(srv.URL+"/tone.flac", 0, 180, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "loaded", 5*time.Second, func() bool { return e.Loaded() || e.LoadError() != "" })
	if msg := e.LoadError(); msg != "" {
		t.Fatalf("load: %s", msg)
	}
	waitFor(t, "playing", 5*time.Second, e.IsPlaying)

	for _, target := range []float64{0.9, 0.1, 0.5, 0.75, 0.3, 0.95, 0.05} {
		e.SeekTo(target, 180)
		time.Sleep(700 * time.Millisecond)
		p := e.Position()
		t.Logf("seek %.2f -> position %.2f state paused=%v playing=%v ended=%v err=%q", target, p, e.Paused(), e.IsPlaying(), e.Ended(), e.LoadError())
		if e.LoadError() != "" {
			t.Fatalf("seek %.2f left a load error: %s", target, e.LoadError())
		}
		if d := p/180 - target; d > 0.15 || d < -0.15 {
			t.Errorf("seek %.2f landed at %.2f (delta %.2f)", target, p, d)
		}
	}
}
