package audio

import (
	"bytes"
	"io"
	"math"

	flac "github.com/mewkiz/flac"
	"os"
	"testing"
)

// readAll 从引擎解码链路里读出全部 s16 PCM（绕过 oto：直接驱动 cnt 的源）。
// 这里用小包装直接走 oggReader/flacReader 的 Read，模拟 oto 的消费方式。
func drainReader(r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if len(out) > 64<<20 {
			return out, io.ErrShortBuffer
		}
	}
}

// pcmStats 计算 PCM 的峰值与 RMS（s16le 交错，任意声道数按数值算）。
func pcmStats(pcm []byte) (peak, rms float64) {
	if len(pcm) < 2 {
		return 0, 0
	}
	var sum float64
	n := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		a := math.Abs(v)
		if a > peak {
			peak = a
		}
		sum += v * v
		n++
	}
	return peak, math.Sqrt(sum / float64(n))
}

func TestOggDecodeSane(t *testing.T) {
	data, err := os.ReadFile("testdata/test.ogg")
	if err != nil {
		t.Fatal(err)
	}
	e := New()
	if err := e.Load(data); err != nil {
		t.Fatalf("Load ogg: %v", err)
	}
	e.Detach() // 关闭 oto 播放器（混音协程仍会读源填充缓冲）
	if e.srcRate <= 0 {
		t.Fatalf("sample rate = %d", e.srcRate)
	}
	pcm, err := drainReader(e)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pcm) == 0 || len(pcm)%4 != 0 {
		t.Fatalf("pcm size %d", len(pcm))
	}
	peak, rms := pcmStats(pcm)
	if peak < 1000 {
		t.Errorf("peak too small (%.0f) — decode produced silence?", peak)
	}
	// 真音频的 RMS 不会是满幅白噪音；放宽到区间判定
	if rms < 30 || rms > 32700 {
		t.Errorf("rms out of sane range: %.0f", rms)
	}
	t.Logf("ogg: rate=%d pcm=%dB peak=%.0f rms=%.0f", e.srcRate, len(pcm), peak, rms)
}

func TestOggSeek(t *testing.T) {
	data, _ := os.ReadFile("testdata/test.ogg")
	e := New()
	if err := e.Load(data); err != nil {
		t.Fatal(err)
	}
	e.Detach()
	// 引擎级 seek 到 60%
	e.SeekTo(0.6, 10) // dur 无所谓：PCM 路径按字节偏移
	after, err := drainReader(e)
	if err != nil {
		t.Fatalf("read after seek: %v", err)
	}
	if len(after) == 0 {
		t.Fatal("no data after seek to 60%")
	}
	total := pcmTotal("testdata/test.ogg")
	if total <= 0 {
		t.Skip("length unknown")
	}
	want := int(total*4/10 - total*4/10%4)
	if len(after) < want*9/10 || len(after) > want*11/10+4 {
		t.Errorf("after-seek bytes = %d, want ≈%d", len(after), want)
	}
}

// pcmTotal 返回 testdata ogg 解码后的 PCM 字节数（-1 = 未知）。
func pcmTotal(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	vals, format, err := decodeOgg(data)
	if err != nil || format == nil {
		return -1
	}
	return len(interleaveS16(vals, max(1, format.Channels)))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestFlacDecodeSane(t *testing.T) {
	data, err := os.ReadFile("testdata/19875.flac")
	if err != nil {
		t.Fatal(err)
	}
	e := New()
	if err := e.Load(data); err != nil {
		t.Fatalf("Load flac: %v", err)
	}
	e.Detach()
	st, err := flac.NewSeek(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := drainReader(e)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := int(st.Info.NSamples) * 4 // 2ch s16（单声道已上混）
	if len(pcm) != want {
		t.Errorf("pcm bytes = %d, want %d", len(pcm), want)
	}
	peak, rms := pcmStats(pcm)
	if peak < 500 {
		t.Errorf("peak too small (%.0f)", peak)
	}
	if rms < 30 || rms > 32700 {
		t.Errorf("rms out of sane range: %.0f", rms)
	}
	// 立体声反相关错误的特征：两声道几乎不相关/一边全是差值噪音。
	// 这里只查左右声道 RMS 差不悬殊（真实音乐通常同量级）。
	l := pcmStatsLR(pcm)
	if l.rmsL > 0 && l.rmsR > 0 {
		ratio := l.rmsL / l.rmsR
		if ratio > 12 || ratio < 1.0/12 {
			t.Errorf("L/R rms ratio %.1f — stereo decorrelation looks broken", ratio)
		}
	}
	t.Logf("flac: rate=%d ch=%d bits=%d peak=%.0f rmsL=%.0f rmsR=%.0f",
		st.Info.SampleRate, st.Info.NChannels, st.Info.BitsPerSample, peak, l.rmsL, l.rmsR)
}

func pcmStatsLR(pcm []byte) (out struct{ rmsL, rmsR float64 }) {
	var sl, sr float64
	n := 0
	for i := 0; i+3 < len(pcm); i += 4 {
		l := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		r := float64(int16(uint16(pcm[i+2]) | uint16(pcm[i+3])<<8))
		sl += l * l
		sr += r * r
		n++
	}
	if n > 0 {
		out.rmsL = math.Sqrt(sl / float64(n))
		out.rmsR = math.Sqrt(sr / float64(n))
	}
	return
}

func TestSniffRejectsGarbage(t *testing.T) {
	e := New()
	if err := e.Load([]byte("this is definitely not audio")); err == nil {
		t.Fatal("garbage must be rejected, not fed to the mp3 decoder")
	}
	// OggS 开头但内容损坏：必须报 ogg 错误而不是 mp3 静音/噪音
	if err := e.Load(append([]byte("OggS"), make([]byte, 128)...)); err == nil {
		t.Fatal("corrupt ogg must error")
	}
}

func TestResamplerSine(t *testing.T) {
	// 1kHz 正弦 44100 → 48000，输出 RMS 应接近输入且无爆点
	const inRate, outRate = 44100.0, 48000.0
	n := inRate // 1 秒
	src := make([]byte, int(n)*4)
	for i := 0; i < int(n); i++ {
		v := int32(16000 * math.Sin(2*math.Pi*1000*float64(i)/inRate))
		src[i*4] = byte(v)
		src[i*4+1] = byte(v >> 8)
		src[i*4+2] = byte(v)
		src[i*4+3] = byte(v >> 8)
	}
	r := &resampler{src: bytes.NewReader(src), ratio: inRate / outRate}
	out, err := drainReader(r)
	if err != nil {
		t.Fatalf("resample: %v", err)
	}
	// 时长应接近 1s（前后插值边缘允许偏差）
	wantSec := float64(len(out)/4) / outRate
	if math.Abs(wantSec-1) > 0.01 {
		t.Errorf("output duration %.4fs, want ~1s", wantSec)
	}
	peak, rms := pcmStats(out)
	if peak > 16300 || peak < 14000 {
		t.Errorf("peak %.0f, want ~16000 (interpolation must not overshoot)", peak)
	}
	if rms < 10000 || rms > 12500 {
		t.Errorf("rms %.0f, want ~11000 (sine 16000 amplitude)", rms)
	}
}

func TestResamplerTail(t *testing.T) {
	// 输入比一段 oto 读取更短：必须把尾部样本吐干净后干净 EOF
	src := make([]byte, 400) // 100 帧
	for i := range src {
		src[i] = byte(i)
	}
	r := &resampler{src: bytes.NewReader(src), ratio: 44100.0 / 48000.0}
	out, err := drainReader(r)
	if err != nil {
		t.Fatalf("tail read: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("tail produced no samples")
	}
	if len(out)%4 != 0 {
		t.Errorf("output not frame-aligned: %d", len(out))
	}
}
