package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/team-quaver/liino"
	"github.com/team-quaver/liino/decode"
	"github.com/team-quaver/liino/pcm"
)

// fakeBackend 用合成正弦波代替 FFmpeg：适配器测试不该依赖机器是否装了
// 系统 FFmpeg，也能让 CI 在没有音频库的环境里跑完整条载入/播放/结束链。
type fakeBackend struct{}

type fakeStream struct {
	mu       sync.Mutex
	total    int // 总帧数
	produced int
	seekable bool
	seekErr  error // 非 nil 时只让这条流拒绝 seek（模拟网络 Range 失败）
}

const fakeName = "astrafake"

func (fakeBackend) Name() string          { return fakeName }
func (fakeBackend) Priority() int         { return -1 }
func (fakeBackend) Probe() (bool, string) { return true, "synthetic tone" }

func (fakeBackend) Open(context.Context, decode.Source, decode.Options) (decode.Stream, error) {
	return &fakeStream{total: pcm.DefaultSampleRate * 2, seekable: true}, nil
}

func (s *fakeStream) Format() pcm.Format { return pcm.DefaultFormat() }

func (s *fakeStream) Meta() decode.Meta {
	return decode.Meta{
		Codec:      "fake",
		SampleRate: pcm.DefaultSampleRate,
		Channels:   pcm.DefaultChannels,
		Duration:   2 * time.Second,
		Seekable:   s.seekable,
	}
}

func (s *fakeStream) Read() (pcm.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.produced >= s.total {
		return pcm.Chunk{}, io.EOF
	}
	n := min(pcm.ChunkFrames, s.total-s.produced)
	data := make([]float32, n*pcm.DefaultChannels)
	for i := 0; i < n; i++ {
		v := float32(0.1 * math.Sin(2*math.Pi*220*float64(s.produced+i)/pcm.DefaultSampleRate))
		data[i*2], data[i*2+1] = v, v
	}
	s.produced += n
	return pcm.Chunk{Data: data, Frames: n, Format: pcm.DefaultFormat()}, nil
}

func (s *fakeStream) Seek(t time.Duration) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seekErr != nil {
		return 0, s.seekErr
	}
	if !s.seekable {
		return 0, decode.ErrSeekUnsupported
	}
	f := int(t.Seconds() * pcm.DefaultSampleRate)
	if f < 0 {
		f = 0
	}
	if f > s.total {
		f = s.total
	}
	s.produced = f
	return t, nil
}

func (s *fakeStream) Close() error { return nil }

// seekFailBackend 让第一次 Open 出来的流拒绝 seek，模拟续播起点落在坏点/
// CDN Range 超时。Liino 会在目标位置重建解码器并递增载入代次，适配层不能
// 把新代的事件当成旧载入丢掉。
type seekFailBackend struct {
	mu     sync.Mutex
	opened int
}

const seekFailName = "astrafakeseek"

func (b *seekFailBackend) Name() string          { return seekFailName }
func (b *seekFailBackend) Priority() int         { return -1 }
func (b *seekFailBackend) Probe() (bool, string) { return true, "first seek fails" }

func (b *seekFailBackend) Open(context.Context, decode.Source, decode.Options) (decode.Stream, error) {
	b.mu.Lock()
	b.opened++
	first := b.opened == 1
	b.mu.Unlock()
	s := &fakeStream{total: pcm.DefaultSampleRate * 2, seekable: true}
	if first {
		s.seekErr = errors.New("range read timed out")
	}
	return s, nil
}

func init() {
	decode.Register(fakeBackend{})
	decode.Register(&seekFailBackend{})
}

func newFakeEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := liino.Config{
		Decoder:         fakeName,
		Driver:          "null",
		RingFrames:      pcm.DefaultSampleRate,
		PrerollFrames:   pcm.ChunkFrames,
		PeriodFrames:    pcm.ChunkFrames / 2,
		MonitorInterval: 5 * time.Millisecond,
		DevicePolling:   time.Hour,
		FadeOut:         0,
	}
	e := newEngine(cfg)
	t.Cleanup(e.Close)
	return e
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// TestLoadsPlaysAndEnds 覆盖 player 依赖的完整生命周期：载入成形、真实出声、
// 位置推进、自然播完。
func TestLoadsPlaysAndEnds(t *testing.T) {
	e := newFakeEngine(t)
	if err := e.OpenURL("fake://tone", 0, 2, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "loaded", 2*time.Second, e.Loaded)
	if e.LoadError() != "" {
		t.Fatalf("load error: %s", e.LoadError())
	}
	waitFor(t, "playing", 2*time.Second, e.IsPlaying)
	waitFor(t, "position advancing", 2*time.Second, func() bool { return e.Position() > 0.05 })

	if d := e.Duration(9); math.Abs(d-2) > 0.01 {
		t.Fatalf("duration = %v, want 2s from the container", d)
	}
	waitFor(t, "ended", 6*time.Second, e.Ended)
}

// TestSeekMovesTheClock 证明 player 的进度条拖动映射到引擎时钟。
func TestSeekMovesTheClock(t *testing.T) {
	e := newFakeEngine(t)
	if err := e.OpenURL("fake://tone", 0, 2, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "playing", 2*time.Second, e.IsPlaying)
	e.SeekTo(0.5, 2)
	waitFor(t, "seek target", 2*time.Second, func() bool { return e.Position() >= 0.95 })
}

// TestPausePlay 检查暂停意图与真实出声状态一致。
func TestPausePlay(t *testing.T) {
	e := newFakeEngine(t)
	if err := e.OpenURL("fake://tone", 0, 2, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "playing", 2*time.Second, e.IsPlaying)

	e.Pause()
	if !e.Paused() {
		t.Fatal("engine should report paused")
	}
	if e.IsPlaying() {
		t.Fatal("paused engine must not report playing audio")
	}
	e.Play()
	waitFor(t, "resumed", 2*time.Second, func() bool { return !e.Paused() && e.IsPlaying() })
}

// failBackend 让 Open 异步失败，用来验证失败原因的致命性分类。
type failBackend struct {
	name string
	err  error
}

func (b failBackend) Name() string          { return b.name }
func (b failBackend) Priority() int         { return -1 }
func (b failBackend) Probe() (bool, string) { return true, "always fails" }
func (b failBackend) Open(context.Context, decode.Source, decode.Options) (decode.Stream, error) {
	return nil, b.err
}

// TestLoadFailureClassification 覆盖异步载入失败：Open 本身不报错，
// 失败由 LoadError 回报。后端不可用是致命的（player 应停止降档），
// 单条流打不开则不是（player 应继续换档）。
func TestLoadFailureClassification(t *testing.T) {
	soft := failBackend{name: "astrasoft", err: errors.New("HTTP 403 Forbidden")}
	fatal := failBackend{name: "astrafatal", err: errors.Join(decode.ErrUnavailable, errors.New("ffmpeg not found"))}
	decode.Register(soft)
	decode.Register(fatal)
	t.Cleanup(func() {
		decode.Unregister(soft.name)
		decode.Unregister(fatal.name)
	})

	for _, tc := range []struct {
		backend failBackend
		fatal   bool
	}{
		{soft, false},
		{fatal, true},
	} {
		e := newEngine(liino.Config{Decoder: tc.backend.name, Driver: "null"})
		if err := e.OpenURL("fake://tone", 0, 2, true); err != nil {
			e.Close()
			t.Fatalf("%s: OpenURL returned a synchronous error: %v", tc.backend.name, err)
		}
		waitFor(t, tc.backend.name+" failure", 2*time.Second, func() bool { return e.LoadError() != "" })
		if e.Loaded() {
			t.Errorf("%s: a failed load must not report Loaded", tc.backend.name)
		}
		if got := e.LoadErrorFatal(); got != tc.fatal {
			t.Errorf("%s: LoadErrorFatal = %v, want %v (%s)", tc.backend.name, got, tc.fatal, e.LoadError())
		}
		e.Close()
	}
}

func TestFatalClassification(t *testing.T) {
	fatal := []string{
		"open x: decode: no decoder backend available",
		"decode: backend unavailable: linux/loong64",
		"output: no audio driver available",
		"liino: player closed",
	}
	for _, msg := range fatal {
		if !fatalError(msg) {
			t.Errorf("fatalError(%q) = false, want true", msg)
		}
	}
	for _, msg := range []string{
		"open http://127.0.0.1/song: ffgo: HTTP 403 Forbidden",
		"decoder read failed: connection reset by peer",
		"seek: range read timed out",
	} {
		if fatalError(msg) {
			t.Errorf("fatalError(%q) = true, want false", msg)
		}
	}
}

// TestNativeDecodesWAV 走真实的 Liino native 后端（ffgo → 系统 FFmpeg）。
// 机器没装 FFmpeg 共享库时跳过：适配层逻辑已由合成后端覆盖，这里只验证
// Native 模式在这台机器上确实能解出 PCM。
func TestNativeDecodesWAV(t *testing.T) {
	available := false
	for _, st := range decode.Statuses() {
		if st.Name == "ffgo" && st.Available {
			available = true
		}
	}
	if !available {
		t.Skip("ffgo (系统 FFmpeg) 不可用，跳过 native 后端集成测试")
	}

	wav := writeTestWAV(t)
	cfg := liino.Config{
		Decoder:         "ffgo",
		Driver:          "null",
		RingFrames:      pcm.DefaultSampleRate,
		PrerollFrames:   pcm.ChunkFrames,
		PeriodFrames:    pcm.ChunkFrames / 2,
		MonitorInterval: 5 * time.Millisecond,
		DevicePolling:   time.Hour,
		FadeOut:         0,
	}
	e := newEngine(cfg)
	t.Cleanup(e.Close)

	if err := e.OpenURL(wav, 0, 1, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "native load", 5*time.Second, func() bool { return e.Loaded() || e.LoadError() != "" })
	if msg := e.LoadError(); msg != "" {
		t.Fatalf("native backend failed: %s", msg)
	}
	waitFor(t, "native playback", 5*time.Second, e.IsPlaying)
	waitFor(t, "native end", 8*time.Second, e.Ended)
	if d := e.Duration(9); math.Abs(d-1) > 0.05 {
		t.Fatalf("duration = %v, want ~1s", d)
	}
}

// TestRealOutput 走默认输出驱动（Linux 是 PipeWire）真跑一遍设备链路。
// 默认跳过：CI 与无声卡的机器不该因为设备不存在而失败。本地自检：
//
//	QAA_TEST_AUDIO=1 go test -run TestRealOutput -v ./internal/audio/
//
// 音量置 0，只验证设备会话能建立、时钟会随消费前进。
func TestRealOutput(t *testing.T) {
	if os.Getenv("QAA_TEST_AUDIO") != "1" {
		t.Skip("设置 QAA_TEST_AUDIO=1 才跑真实音频设备")
	}
	e := New()
	t.Cleanup(e.Close)
	e.SetVolume(0)

	wav := writeTestWAV(t)
	if err := e.OpenURL(wav, 0, 1, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "load", 5*time.Second, func() bool { return e.Loaded() || e.LoadError() != "" })
	if msg := e.LoadError(); msg != "" {
		t.Fatalf("load failed: %s", msg)
	}
	waitFor(t, "audio device playing", 8*time.Second, e.IsPlaying)
	waitFor(t, "device clock advancing", 5*time.Second, func() bool { return e.Position() > 0.2 })
}

// writeTestWAV 写一个 1 秒 48 kHz 立体声 16 位 PCM 的正弦波。
func writeTestWAV(t *testing.T) string {
	t.Helper()
	const (
		rate    = 48000
		seconds = 1
		ch      = 2
	)
	frames := rate * seconds
	data := make([]byte, frames*ch*2)
	for i := 0; i < frames; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/rate))
		binary.LittleEndian.PutUint16(data[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(data[i*4+2:], uint16(v))
	}
	var buf bytes.Buffer
	byteRate := rate * ch * 2
	fmt.Fprint(&buf, "RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(data)))
	fmt.Fprint(&buf, "WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))       // PCM header size
	binary.Write(&buf, binary.LittleEndian, uint16(1))        // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(ch))       // channels
	binary.Write(&buf, binary.LittleEndian, uint32(rate))     // sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(ch*2))     // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))       // bits
	fmt.Fprint(&buf, "data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)

	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInitialSeekRebuild 覆盖「带进度续播时首跳失败」：Liino 会重建解码器
// 并换一代，适配层必须继续认这次载入，而不是等到超时误判成坏档。
func TestInitialSeekRebuild(t *testing.T) {
	cfg := liino.Config{
		Decoder:         seekFailName,
		Driver:          "null",
		RingFrames:      pcm.DefaultSampleRate,
		PrerollFrames:   pcm.ChunkFrames,
		PeriodFrames:    pcm.ChunkFrames / 2,
		MonitorInterval: 5 * time.Millisecond,
		DevicePolling:   time.Hour,
		FadeOut:         0,
	}
	e := newEngine(cfg)
	t.Cleanup(e.Close)

	if err := e.OpenURL("fake://tone", 0.5, 2, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "loaded after seek rebuild", 3*time.Second, func() bool { return e.Loaded() || e.LoadError() != "" })
	if msg := e.LoadError(); msg != "" {
		t.Fatalf("rebuild path reported an error: %s", msg)
	}
	waitFor(t, "playing at the rebuilt position", 3*time.Second, func() bool { return e.Position() > 0.9 })
}

func TestStopClearsLoaded(t *testing.T) {
	e := newFakeEngine(t)
	if err := e.OpenURL("fake://tone", 0, 2, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "loaded", 2*time.Second, e.Loaded)
	e.Stop()
	if e.Loaded() {
		t.Fatal("Stop must clear the loaded flag")
	}
	if e.LoadError() != "" {
		t.Fatalf("Stop left a load error: %s", e.LoadError())
	}
}
