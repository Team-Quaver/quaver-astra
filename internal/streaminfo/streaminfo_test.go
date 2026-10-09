package streaminfo

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// ===== 合成头构造 =====

// flacHead 造一段 FLAC 文件头：fLaC + STREAMINFO 块（首块，规格保证在头 42 字节内）。
func flacHead(sampleRate, channels, bitDepth int, totalSamples int64) []byte {
	b := make([]byte, 42)
	copy(b, "fLaC")
	b[4] = 0x00 // 块类型 0 = STREAMINFO（isLast 随意，本实现不看这一位）
	// 块体 34 字节从偏移 8 起；采样率/声道/位深打包在 18..21，总样本数 21..25
	s := 18
	b[s] = byte(sampleRate >> 12)
	b[s+1] = byte(sampleRate >> 4)
	b[s+2] = byte(sampleRate&0xf)<<4 | byte(channels-1)<<1 | byte(bitDepth-1)>>4
	b[s+3] = byte((bitDepth-1)&0xf)<<4 | byte((totalSamples>>32)&0xf)
	b[s+4] = byte(totalSamples >> 24)
	b[s+5] = byte(totalSamples >> 16)
	b[s+6] = byte(totalSamples >> 8)
	b[s+7] = byte(totalSamples)
	return b
}

// oggOpus 造一段 Ogg 首页：27 字节页头 + OpusHead（ID 头按规格必须在首页）。
func oggOpus(channels int) []byte {
	b := make([]byte, 64)
	copy(b, "OggS")
	copy(b[28:], "OpusHead")
	b[36] = 1 // version
	b[37] = byte(channels)
	return b
}

// oggVorbis 造一段 Ogg 首页：27 字节页头 + Vorbis 标识头（packet type 1）。
func oggVorbis(channels, sampleRate, nominalBps int) []byte {
	b := make([]byte, 64)
	copy(b, "OggS")
	b[28] = 1
	copy(b[29:], "vorbis")
	b[39] = byte(channels)
	b[40] = byte(sampleRate)
	b[41] = byte(sampleRate >> 8)
	b[42] = byte(sampleRate >> 16)
	b[43] = byte(sampleRate >> 24)
	b[48] = byte(nominalBps)
	b[49] = byte(nominalBps >> 8)
	b[50] = byte(nominalBps >> 16)
	b[51] = byte(nominalBps >> 24)
	return b
}

// mp3Frame 造一个 MPEG1 Layer III 帧头（brIdx/srIdx 见 MP3 帧头位域）。
func mp3Frame(brIdx, srIdx, mode int) []byte {
	b := make([]byte, 4)
	b[0] = 0xff
	b[1] = 0xfb // MPEG1 / Layer III
	b[2] = byte(brIdx<<4 | srIdx<<2)
	b[3] = byte(mode << 6)
	return b
}

// id3Prefix 造 ID3v2 标签头（尺寸 syncsafe），标签体填满到终点。
func id3Prefix(tagSize int, withFooter bool) []byte {
	total := 10 + tagSize
	if withFooter {
		total += 10
	}
	b := make([]byte, total)
	copy(b, "ID3")
	b[5] = 0
	if withFooter {
		b[5] = 0x10
	}
	b[6] = byte(tagSize >> 21)
	b[7] = byte(tagSize >> 14)
	b[8] = byte(tagSize >> 7)
	b[9] = byte(tagSize)
	return b
}

// ===== 解析 =====

func TestParseFlac(t *testing.T) {
	// 44.1 kHz / 2ch / 24bit / 3 秒（132300 样本）：码率由总长÷时长估算
	head := flacHead(44100, 2, 24, 44100*3)
	info := ParseHead(head, 44100*3*2*3, 0) // 793.8 KB ≈ 2117 kbps
	if info == nil {
		t.Fatal("FLAC 头未解析出信息")
	}
	if info.Codec != "FLAC" || info.SampleRate != 44100 || info.BitDepth != 24 || info.Channels != 2 {
		t.Errorf("FLAC = %+v", info)
	}
	if info.Duration != 3 {
		t.Errorf("时长 = %v，期望 3", info.Duration)
	}
	if !info.BitrateApprox || info.Bitrate < 2100 || info.Bitrate > 2130 {
		t.Errorf("码率 = %v(%v)，期望 ≈2117 kbps", info.Bitrate, info.BitrateApprox)
	}

	// 48 kHz / 8ch / 16bit（atmos 档位口径：声道按字段规范放行）
	head = flacHead(48000, 8, 16, 48000)
	if info := ParseHead(head, 0, 0); info == nil ||
		info.SampleRate != 48000 || info.Channels != 8 || info.BitDepth != 16 {
		t.Errorf("FLAC 8ch = %+v", info)
	}

	// 首块不是 STREAMINFO → 不猜
	bad := flacHead(44100, 2, 16, 100)
	bad[4] = 0x04
	if ParseHead(bad, 0, 0) != nil {
		t.Error("首块非 STREAMINFO 应判废")
	}
}

func TestParseOgg(t *testing.T) {
	// Opus：7.1.4 = 12 声道（Q003 全景声 7.1 的口径），解码输出恒 48 kHz
	info := ParseHead(oggOpus(12), 0, 0)
	if info == nil || info.Codec != "Opus" || info.Channels != 12 || info.SampleRate != 48000 {
		t.Errorf("Ogg/Opus = %+v", info)
	}

	// Vorbis：标称码率 320000 bps → 320 kbps（头内标称值，不加 ≈）
	info = ParseHead(oggVorbis(2, 44100, 320000), 0, 0)
	if info == nil || info.Codec != "Vorbis" || info.SampleRate != 44100 || info.Channels != 2 {
		t.Errorf("Ogg/Vorbis = %+v", info)
	}
	if info.Bitrate != 320 || info.BitrateApprox {
		t.Errorf("Vorbis 码率 = %v(%v)，期望 320（标称）", info.Bitrate, info.BitrateApprox)
	}

	// Vorbis 标称码率缺失（0）→ 用总长÷时长估算
	info = ParseHead(oggVorbis(2, 44100, 0), 1_000_000, 25)
	if info == nil || !info.BitrateApprox || info.Bitrate < 310 || info.Bitrate > 330 {
		t.Errorf("Vorbis 估算码率 = %+v，期望 ≈320 kbps", info)
	}

	// 是 Ogg 但认不出编码（如腾讯自研封装）→ 只报容器，不猜编码
	bare := make([]byte, 64)
	copy(bare, "OggS")
	info = ParseHead(bare, 0, 0)
	if info == nil || info.Codec != "Ogg" {
		t.Errorf("未知 Ogg = %+v，期望容器级记录", info)
	}
}

func TestParseMp3(t *testing.T) {
	// MPEG1 L3 / 320 kbps（brIdx 14）/ 44.1 kHz（srIdx 0）/ 立体声
	info := ParseHead(mp3Frame(14, 0, 0), 0, 0)
	if info == nil || info.Codec != "MP3" || info.SampleRate != 44100 || info.Channels != 2 {
		t.Errorf("MP3 = %+v", info)
	}
	if info.Bitrate != 320 || info.BitrateApprox {
		t.Errorf("MP3 码率 = %v(%v)，期望 320（帧头标称）", info.Bitrate, info.BitrateApprox)
	}

	// 单声道 / MPEG2 的低码率表
	info = ParseHead(mp3Frame(1, 0, 3), 0, 0)
	if info == nil || info.Channels != 1 {
		t.Errorf("MP3 单声道 = %+v", info)
	}

	// 带 ID3v2：帧头藏在标签后，ParseHead 首块看不到帧 → 二段探测由
	// Fetch 负责；单看首块应判废（这里钉住 ParseHead 不瞎猜）
	tag := id3Prefix(200, false)
	tag = append(tag, mp3Frame(14, 0, 0)...)
	if info := ParseHead(tag[:210], 0, 0); info != nil {
		t.Errorf("ID3 后无帧头不应解析出信息，实测 %+v", info)
	}
	// 标签终点之后是帧头 → 能解
	if info := ParseHead(tag, 0, 0); info == nil || info.Bitrate != 320 {
		t.Errorf("ID3 + 帧头 = %+v", info)
	}

	// 非法帧头（free format / 坏位率索引）
	if ParseHead([]byte{0xff, 0xfb, 0x00, 0x00}, 0, 0) != nil {
		t.Error("brIdx=0（free format）应判废")
	}
	if ParseHead([]byte{0xff, 0xff, 0xff, 0xff}, 0, 0) != nil {
		t.Error("layer=0 应判废")
	}
}

func TestMp3FrameOffset(t *testing.T) {
	if off := Mp3FrameOffset(id3Prefix(100, false)); off != 110 {
		t.Errorf("标签终点 = %d，期望 110", off)
	}
	if off := Mp3FrameOffset(id3Prefix(100, true)); off != 120 {
		t.Errorf("带 footer 的标签终点 = %d，期望 120", off)
	}
	if off := Mp3FrameOffset(mp3Frame(14, 0, 0)); off != 0 {
		t.Errorf("无 ID3 应为 0，实测 %d", off)
	}
}

// ===== 格式化 =====

func TestFormatters(t *testing.T) {
	cases := []struct {
		info           *Info
		sr, bd, br, ch string
	}{
		{&Info{SampleRate: 44100, BitDepth: 24, Bitrate: 320, Channels: 2},
			"44.1 kHz", "24 bit", "320 kbps", "2（立体声）"},
		{&Info{SampleRate: 48000, BitDepth: 0, Bitrate: 132.4, BitrateApprox: true, Channels: 12},
			"48 kHz", "—", "≈132 kbps", "12（7.1.4）"},
		{&Info{SampleRate: 11025, Channels: 1}, "11.0 kHz", "—", "—", "1（单声道）"},
		{&Info{Channels: 9}, "—", "—", "—", "9 声道"},
		{nil, "—", "—", "—", "—"},
	}
	for i, c := range cases {
		if got := c.info.FmtSampleRate(); got != c.sr {
			t.Errorf("case %d 采样率 = %q，期望 %q", i, got, c.sr)
		}
		if got := c.info.FmtBitDepth(); got != c.bd {
			t.Errorf("case %d 采样精度 = %q，期望 %q", i, got, c.bd)
		}
		if got := c.info.FmtBitrate(); got != c.br {
			t.Errorf("case %d 码率 = %q，期望 %q", i, got, c.br)
		}
		if got := c.info.FmtChannels(); got != c.ch {
			t.Errorf("case %d 声道 = %q，期望 %q", i, got, c.ch)
		}
	}
}

// ===== 探测 =====

// rangeServer 起一个支持 Range 的假流服务器（同本地中继的口径：
// 透传 Range、回 Content-Range）。
func rangeServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if !strings.HasPrefix(rng, "bytes=") {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}
		var a, b int
		if err := sscanfRange(rng, &a, &b); err != nil {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if b >= len(body) {
			b = len(body) - 1
		}
		w.Header().Set("Content-Range",
			"bytes "+strconv.Itoa(a)+"-"+strconv.Itoa(b)+"/"+strconv.Itoa(len(body)))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[a : b+1])
	}))
}

// sscanfRange 解析 "bytes=a-b"。
func sscanfRange(rng string, a, b *int) error {
	rng = strings.TrimPrefix(rng, "bytes=")
	parts := strings.SplitN(rng, "-", 2)
	if len(parts) != 2 {
		return strconv.ErrSyntax
	}
	var err error
	*a, err = strconv.Atoi(parts[0])
	if err != nil {
		return err
	}
	*b, err = strconv.Atoi(parts[1])
	return err
}

func TestFetchFlac(t *testing.T) {
	head := flacHead(44100, 2, 16, 44100*2)
	body := append(head, make([]byte, 1000)...)
	srv := rangeServer(t, body)
	defer srv.Close()

	info := Fetch(srv.URL, 0, 0)
	if info == nil || info.Codec != "FLAC" || info.BitDepth != 16 {
		t.Fatalf("Fetch FLAC = %+v", info)
	}
	// 总长来自 Content-Range（1042 字节 / 2 秒 → ≈4 kbps）
	if !info.BitrateApprox || info.Bitrate < 3 || info.Bitrate < 0 {
		t.Errorf("估算码率 = %+v", info)
	}
}

func TestFetchMp3WithID3(t *testing.T) {
	// 标签 300 字节：首块 4096 里看不到帧头 → 走二段探测（按标签终点再取）
	body := id3Prefix(300, false)
	body = append(body, mp3Frame(14, 0, 0)...)
	body = append(body, make([]byte, 500)...)
	srv := rangeServer(t, body)
	defer srv.Close()

	info := Fetch(srv.URL, 0, 0)
	if info == nil || info.Codec != "MP3" || info.Bitrate != 320 {
		t.Fatalf("Fetch ID3+MP3 = %+v（应按标签终点二段探测）", info)
	}
}

func TestFetchUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if info := Fetch(srv.URL, 0, 0); info != nil {
		t.Errorf("404 应返回 nil，实测 %+v", info)
	}
	if info := Fetch("http://127.0.0.1:1", 0, 0); info != nil {
		t.Errorf("拒连应返回 nil，实测 %+v", info)
	}
	// 容器认不出（不是 FLAC/Ogg/MP3）→ nil
	body := []byte("XXXX" + strings.Repeat("\x00", 100))
	srv2 := rangeServer(t, body)
	defer srv2.Close()
	if info := Fetch(srv2.URL, 0, 0); info != nil {
		t.Errorf("未知容器应返回 nil，实测 %+v", info)
	}
}
