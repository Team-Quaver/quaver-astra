package backend

// 实况诊断（不入 CI）：QAA_LIVE=1 go test -run TestLive -v ./internal/backend/
// 用真实凭证解析 320K mp3 流，验证：全量缓冲解码与 Range 流式解码的 PCM
// 逐字节一致、统计健康（无白噪音特征）。需要登录态（320 对匿名不可用）。

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/audio"
	"github.com/Team-Quaver/quaver-astra/internal/vault"
)

func TestLiveMp3StreamDecode(t *testing.T) {
	if os.Getenv("QAA_LIVE") != "1" {
		t.Skip("需要 QAA_LIVE=1")
	}
	dir := os.Getenv("QAA_CONFIG")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "quaver-astra")
	}
	s, err := Start(dir, vault.Open(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	time.Sleep(300 * time.Millisecond)

	c := NewClient(s.BaseURL())
	res, err := c.RecommendNewsong()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Songs) == 0 {
		t.Fatal("no songs")
	}
	song := res.Songs[0]
	r, err := c.ResolveStream(song.Mid, "", song.Type, "320", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resolved: tier=%s size=%d degraded=%v", r.Tier, r.Size, r.Degraded)

	url := s.BaseURL() + r.Path
	resp, err := StreamClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	full, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(full) < 1024 {
		t.Fatalf("stream too small: %d", len(full))
	}
	t.Logf("downloaded %d bytes, head=% x", len(full), full[:4])

	drain := func(e *audio.Engine) []byte {
		var out []byte
		buf := make([]byte, 8192)
		for {
			n, err := e.Read(buf)
			out = append(out, buf[:n]...)
			if err != nil {
				break
			}
			if len(out) > 256<<20 {
				t.Fatal("pcm too large")
			}
		}
		return out
	}
	stats := func(pcm []byte) (peak, rms float64) {
		var sum float64
		n := 0
		for i := 0; i+1 < len(pcm); i += 2 {
			v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
			if a := math.Abs(v); a > peak {
				peak = a
			}
			sum += v * v
			n++
		}
		return peak, math.Sqrt(sum / math.Max(1, float64(n)))
	}

	// 1) 全量缓冲解码（autoplay=false → Detach 后从 0 开始读）
	byteSrcf := func(off int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(full[off:])), nil
	}
	e1 := audio.New()
	decErr := e1.OpenStream(byteSrcf, int64(len(full)), 0, 0, false)
	e1.Detach()
	pcm1 := drain(e1)
	t.Logf("full-buffer decode err=%v, pcm=%dB", decErr, len(pcm1))

	// 按秒窗口看 RMS：定位噪音段（正常音乐每秒 RMS 同量级，噪音段接近满幅）
	if len(pcm1) > 0 {
		sec := 44100 * 4 * 2 // 假设 44.1k 双声道 16bit 的字节数按实际率换算
		_ = sec
		wsize := 48000 * 4 // 输出 48k 立体声 s16 = 192KB/s
		for start := 0; start < len(pcm1); start += wsize {
			end := start + wsize
			if end > len(pcm1) {
				end = len(pcm1)
			}
			peak, rms := stats(pcm1[start:end])
			if rms > 20000 || peak >= 32700 {
				t.Logf("  t=%4.1fs rms=%.0f peak=%.0f  <== 疑似噪音", float64(start)/192000.0, rms, peak)
			}
		}
	}
	// 原始字节：出错位置附近
	if decErr != nil {
		t.Logf("decode error: %v", decErr)
		const pos = 7644972
		lo, hi := pos-32, pos+48
		if hi > len(full) {
			hi = len(full)
		}
		t.Logf("raw around %d: % x", pos, full[lo:hi])
	}

	// 双份下载对比：判断服务端每次 GET 的内容是否一致
	resp2, err := StreamClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	full2, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("download1 len=%d md5=%x", len(full), md5sum(full))
	t.Logf("download2 len=%d md5=%x", len(full2), md5sum(full2))
	if !bytes.Equal(full, full2) {
		t.Error("两次 GET 的字节不一致 —— 服务端/CDN 在变！")
	}

	// 2) Range 流式解码（包装 MD5 计数器：若喂给解码器的流 md5 == 文件 md5
	// 且总长一致，则服务端干净、问题在 rangeReader/解码器）
	var gotMD5 struct {
		mu sync.Mutex
		h  hash.Hash
		n  int64
	}
	netSrcf := func(off int64) (io.ReadCloser, error) {
		req, _ := http.NewRequest("GET", url, nil)
		if off > 0 {
			req.Header.Set("Range", "bytes="+itoa64(off)+"-")
		}
		resp, err := StreamClient.Do(req)
		if err != nil {
			return nil, err
		}
		gotMD5.mu.Lock()
		gotMD5.h = md5.New()
		gotMD5.n = 0
		gotMD5.mu.Unlock()
		return &countingHashBody{body: resp.Body, slot: &gotMD5}, nil
	}
	e2 := audio.New()
	if err := e2.OpenStream(netSrcf, int64(len(full)), 0, 0, false); err != nil {
		// 出错位置附近与全量下载对比：字节不同 = 流路径拿到脏数据
		var pos int64
		if _, err := fmt.Sscanf(err.Error(), "mp3: free bitrate format is not supported. Header word is %*v at position %d", &pos); err == nil && pos+32 < int64(len(full)) {
			lo, hi := pos-16, pos+32
			t.Logf("full bytes @%d: % x", pos, full[lo:hi])
		}
		t.Fatalf("open stream: %v", err)
	}
	e2.Detach()
	pcm2 := drain(e2)
	gotMD5.mu.Lock()
	gotSum, gotN := gotMD5.h.Sum(nil), gotMD5.n
	gotMD5.mu.Unlock()
	t.Logf("stream fed: md5=%x bytes=%d (want md5=%x bytes=%d)", gotSum, gotN, md5.Sum(full), len(full))

	peak, rms := stats(pcm1)
	t.Logf("full-buffer: pcm=%dB peak=%.0f rms=%.0f", len(pcm1), peak, rms)
	if peak < 1000 {
		t.Errorf("peak too small (%.0f) — silence?", peak)
	}
	if rms < 30 || rms > 32700 {
		t.Errorf("rms out of sane range: %.0f", rms)
	}
	if len(pcm1) != len(pcm2) {
		t.Errorf("stream pcm size %d != full %d", len(pcm2), len(pcm1))
	} else if !bytes.Equal(pcm1, pcm2) {
		diff := 0
		for i := range pcm1 {
			if pcm1[i] != pcm2[i] {
				diff++
			}
		}
		t.Errorf("stream decode differs: %d/%d bytes", diff, len(pcm1))
	} else {
		t.Log("stream decode == full-buffer decode (byte-identical)")
	}
}

func md5sum(b []byte) [16]byte { return md5.Sum(b) }

// countingHashBody 统计流经的字节并算 md5（并发安全）。
type countingHashBody struct {
	body io.ReadCloser
	slot *struct {
		mu sync.Mutex
		h  hash.Hash
		n  int64
	}
}

func (c *countingHashBody) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	if n > 0 {
		c.slot.mu.Lock()
		c.slot.h.Write(p[:n])
		c.slot.n += int64(n)
		c.slot.mu.Unlock()
	}
	return n, err
}

func (c *countingHashBody) Close() error { return c.body.Close() }

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
