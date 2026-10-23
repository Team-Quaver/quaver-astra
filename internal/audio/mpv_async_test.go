package audio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestOpenURLWaitsForBackgroundStart 用一个慢启动的假 mpv 证明：
// New() 立即返回，而第一次 OpenURL 会等后台 IPC 启动完成后再下发命令。
func TestOpenURLWaitsForBackgroundStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the mpv executable")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mpv")
	// 模拟驱动/杀软启动延迟；mpv 本身不会在这个测试里被调用。
	script := "#!/bin/sh\nsleep 0.4\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QAA_MPV", bin)
	t.Setenv("QAA_MPV_DIR", "")

	start := time.Now()
	e := New()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		e.Close()
		t.Fatalf("New() waited for the slow mpv: %v", elapsed)
	}
	t.Cleanup(e.Close)

	start = time.Now()
	if err := e.OpenURL("file:///nonexistent", 0, 1, true); err == nil {
		t.Fatal("OpenURL should fail when the fake mpv exits")
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("OpenURL returned before the background start finished: %v", elapsed)
	}
}
