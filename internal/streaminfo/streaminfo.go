// Package streaminfo 探测播放流的音频参数（编码格式 / 采样率 / 采样精度 /
// 码率 / 声道），供正在播放页的音频流信息浮窗展示。
//
// 移植自主项目 ui/src/lib/streaminfo.ts（那里在渲染层做字节探测；这里同样
// 不动后端、不占 resolve 协商的关键路径，打开浮窗才探测一次）。对播放流
// 发一个小 Range 请求取文件头，解析容器头即可——三种容器覆盖全部档位：
//
//	FLAC（STREAMINFO 首块必在文件头 42 字节内）
//	Ogg（Vorbis/Opus 的 ID 头必在首页）
//	MP3（帧头；带 ID3v2 时按头 10 字节里的标签大小跳过标签再取一段）
//
// 各档位实际容器：atmos51 是 FLAC（6ch），atmos71 是 Ogg（7.1.4 = 12ch Opus）
// —— 声道数按字段规范放行，别按「常见值」收口成 8。
//
// 本包零依赖（只用标准库）：解析函数是纯函数，合成头用例直接单测。
package streaminfo

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Info 是一次探测的结果。有损格式没有采样精度（BitDepth 为 0）；头里带不出
// 码率时由总长 ÷ 时长估算（BitrateApprox=true，展示层加 ≈ 号）。
type Info struct {
	Codec         string  // 展示名：FLAC / Vorbis / Opus / MP3
	SampleRate    int     // Hz
	BitDepth      int     // 采样精度（bit），仅无损容器有
	Bitrate       float64 // kbps
	BitrateApprox bool    // true = 平均码率（总长÷时长），非头内标称值
	Channels      int
	Duration      float64 // 秒（FLAC 从 STREAMINFO 精确可得；其余空缺）
}

// ParseHead 解析流头部字节。totalSize 是文件总长（0=未知），
// durationSec 是播放器已知的时长（0=未知，估算码率兜底用）。
// 解析不出返回 nil。
func ParseHead(head []byte, totalSize int64, durationSec float64) *Info {
	if len(head) >= 4 && string(head[:4]) == "fLaC" {
		return parseFlac(head, totalSize)
	}
	if len(head) >= 4 && string(head[:4]) == "OggS" {
		return parseOgg(head, totalSize, durationSec)
	}
	// MP3：裸帧或带 ID3v2（首块里已能看到帧头就直接解）
	if len(head) >= 4 && string(head[:3]) == "ID3" {
		off := Mp3FrameOffset(head)
		if off > 0 && off+4 <= len(head) {
			return parseMp3Frame(head, off, totalSize, durationSec)
		}
		return nil
	}
	return parseMp3Frame(head, 0, totalSize, durationSec)
}

// Mp3FrameOffset 是 ID3v2 标签终点（= 音频帧应起始的偏移）；无 ID3 或头不完整
// 返回 0。尺寸是 syncsafe 整数（每字节 7 位），见 ID3v2.4 spec 6.1；带 footer
// （flag 0x10）再 +10。
func Mp3FrameOffset(head []byte) int {
	if len(head) < 10 || string(head[:3]) != "ID3" {
		return 0
	}
	size := int(head[6]&0x7f)<<21 | int(head[7]&0x7f)<<14 | int(head[8]&0x7f)<<7 | int(head[9]&0x7f)
	if head[5]&0x10 != 0 {
		return 10 + size + 10
	}
	return 10 + size
}

// parseFlac 解析 FLAC STREAMINFO（永远是第一个元数据块，规格保证：
// 4 magic + 4 块头 + 34 字节块体）。块体 10-12 = 采样率(20b，按 12+8 打包)
// + 声道(3b) + 位深(5b)，13-17 = 总样本数(36b)。
//
// 与主项目的一处差异：它要求首块的 isLast 标志为 0（其后还有别的元数据块），
// 这里只要求块类型是 STREAMINFO——只有 STREAMINFO 的合法 FLAC 不该被判废。
func parseFlac(b []byte, totalSize int64) *Info {
	if len(b) < 42 || b[4]&0x7f != 0 {
		return nil
	}
	s := 18
	sampleRate := int(b[s])<<12 | int(b[s+1])<<4 | int(b[s+2])>>4
	channels := int((b[s+2]>>1)&0x7) + 1
	bitDepth := int((b[s+2]&1)<<4|b[s+3]>>4) + 1
	if sampleRate < 8000 || sampleRate > 655350 || bitDepth < 4 || bitDepth > 32 {
		return nil
	}
	totalSamples := int64(b[s+3]&0x0f)<<32 |
		int64(b[s+4])<<24 | int64(b[s+5])<<16 | int64(b[s+6])<<8 | int64(b[s+7])
	duration := 0.0
	if sampleRate > 0 {
		duration = float64(totalSamples) / float64(sampleRate)
	}
	info := &Info{Codec: "FLAC", SampleRate: sampleRate, BitDepth: bitDepth, Channels: channels}
	if duration > 0 {
		info.Duration = duration
	}
	if br := avgBitrate(totalSize, duration); br > 0 {
		info.Bitrate, info.BitrateApprox = br, true
	}
	return info
}

// parseOgg 解析 Ogg 首页的 ID 头（Vorbis 30B / Opus 19B，按规格必须在第一页）。
//
// 声道上界按**字段规范**放行到 255，不按 8 收口：OpusHead 的 Channel Count
// 字段（RFC 7845 §4）是 1-255，8 只是绝大多数内容的实际取值。上游「臻品
// 全景声 7.1」（Q003，7.1.4 = 7 主 + LFE + 4 顶 = 12 声道）就落在区间里——
// 早前按 `> 8` 判废，结果只有这一个档位的流信息恒为「不可用」，而 6 声道/
// 立体声的全景声、FLAC 各档全正常，症状看起来像「随机失效」实则是写死的上界。
//
// 首字节是 OggS 但认不出编码时也照样给一条 Ogg 记录：浮窗是只读展示，宁可
// 少几行也不要掉进「流信息不可用」的死胡同（未知封装不该让整个面板变空）。
func parseOgg(b []byte, totalSize int64, durationSec float64) *Info {
	limit := min(len(b)-8, 200)
	for i := 27; i < limit; i++ {
		if i+10 <= len(b) && string(b[i:i+8]) == "OpusHead" {
			// Opus 解码输出恒为 48 kHz（头里的 input samplerate 是原始录音率，
			// 不是播放采样率）
			channels := int(b[i+9])
			if channels < 1 {
				return nil
			}
			info := &Info{Codec: "Opus", SampleRate: 48000, Channels: channels}
			if br := avgBitrate(totalSize, durationSec); br > 0 {
				info.Bitrate, info.BitrateApprox = br, true
			}
			return info
		}
		if i+24 <= len(b) && b[i] == 1 && string(b[i+1:i+7]) == "vorbis" {
			channels := int(b[i+11])
			sampleRate := int(le32(b, i+12))
			// 标称码率（有符号，<=0 视为缺）
			nominal := int64(int32(le32(b, i+20)))
			if channels < 1 || sampleRate < 8000 || sampleRate > 655350 {
				return nil
			}
			info := &Info{Codec: "Vorbis", SampleRate: sampleRate, Channels: channels}
			if nominal > 0 {
				info.Bitrate = float64(nominal) / 1000
			} else if br := avgBitrate(totalSize, durationSec); br > 0 {
				info.Bitrate, info.BitrateApprox = br, true
			}
			return info
		}
	}
	info := &Info{Codec: "Ogg"}
	if br := avgBitrate(totalSize, durationSec); br > 0 {
		info.Bitrate, info.BitrateApprox = br, true
	}
	return info
}

// parseMp3Frame 解析 MP3 帧头 4 字节：11 位帧同步 + 版本/层/位率/采样率/声道模式。
func parseMp3Frame(b []byte, off int, totalSize int64, durationSec float64) *Info {
	if off+4 > len(b) || b[off] != 0xff || b[off+1]&0xe0 != 0xe0 {
		return nil
	}
	verBits := int(b[off+1]>>3) & 3   // 3=MPEG1 2=MPEG2 0=MPEG2.5（1=保留）
	layerBits := int(b[off+1]>>1) & 3 // 1=Layer III 2=Layer II 3=Layer I（0=保留）
	brIdx := int(b[off+2]>>4) & 0xf   // 0=free 15=坏
	srIdx := int(b[off+2]>>2) & 3     // 3=保留
	mode := int(b[off+3]>>6) & 3      // 3=单声道，其余按 2 声道算
	if verBits == 1 || layerBits == 0 || brIdx == 0 || brIdx == 15 || srIdx == 3 {
		return nil
	}
	mpeg := 25
	switch verBits {
	case 3:
		mpeg = 1
	case 2:
		mpeg = 2
	}
	sampleRates := map[int][3]int{
		1:  {44100, 48000, 32000},
		2:  {22050, 24000, 16000},
		25: {11025, 12000, 8000},
	}
	sampleRate := sampleRates[mpeg][srIdx]
	channels := 2
	if mode == 3 {
		channels = 1
	}
	codec := "MP3"
	if layerBits != 1 {
		m := strconv.Itoa(mpeg)
		if mpeg == 25 {
			m = "2.5"
		}
		codec = "MPEG" + m + " L" + strconv.Itoa(4-layerBits)
	}
	info := &Info{Codec: codec, SampleRate: sampleRate, Channels: channels}
	if layerBits == 1 {
		// Layer III 位率表（kbps）：MPEG1 与 MPEG2/2.5 两张
		var table [16]int
		if mpeg == 1 {
			table = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
		} else {
			table = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
		}
		if kbps := table[brIdx]; kbps > 0 {
			info.Bitrate = float64(kbps)
		}
	}
	if info.Bitrate <= 0 {
		if br := avgBitrate(totalSize, durationSec); br > 0 {
			info.Bitrate, info.BitrateApprox = br, true
		}
	}
	return info
}

// avgBitrate 平均码率（kbps）：总长与时长都已知才算。
func avgBitrate(totalSize int64, durationSec float64) float64 {
	if totalSize <= 0 || durationSec <= 0 {
		return 0
	}
	return float64(totalSize) * 8 / durationSec / 1000
}

// ===== 探测 =====

// fetchBytes 是 HTTP Range 请求的字节上限（只要头，够解析即可）。
const (
	fetchFirst = 4096
	fetchGap   = 256
	fetchMax   = 4_000_000 // ID3v2 标签可能很大，再大就别追了
)

// Fetch 对播放流做一次头字节探测。totalHint 是调用方已知的文件总长
// （resolve 给的 Size，Content-Range 缺失时的兜底），durationSec 供估算码率。
// 解析不出（网络失败、未知容器）返回 nil。
//
// 带 ID3v2 的 MP3 帧头可能藏在标签后（标签可到几百 KB，首页看不到）→
// 按标签终点再取一小段。
func Fetch(url string, totalHint int64, durationSec float64) *Info {
	first, total := fetchRange(url, 0, fetchFirst, totalHint)
	if first == nil {
		return nil
	}
	if info := ParseHead(first, total, durationSec); info != nil {
		return info
	}
	off := Mp3FrameOffset(first)
	if off <= 0 || off > fetchMax {
		return nil
	}
	second, total := fetchRange(url, int64(off), int64(off)+fetchGap, totalHint)
	if second == nil {
		return nil
	}
	return ParseHead(second, total, durationSec)
}

var contentRangeTotal = regexp.MustCompile(`bytes\s+\d+-\d+/(\d+)`)
var contentLength = regexp.MustCompile(`(\d+)`)

// fetchRange 取 [start,end] 闭区间字节；total 从 Content-Range
// （回落 Content-Length、再回落 totalHint）解析。失败返回 nil。
func fetchRange(url string, start, end, totalHint int64) ([]byte, int64) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, 0
	}
	total := totalHint
	if m := contentRangeTotal.FindStringSubmatch(resp.Header.Get("Content-Range")); m != nil {
		total, _ = strconv.ParseInt(m[1], 10, 64)
	} else if m := contentLength.FindStringSubmatch(resp.Header.Get("Content-Length")); m != nil {
		total, _ = strconv.ParseInt(m[1], 10, 64)
	}
	// 只要头几个字节，服务器若无视 Range 回了整段（200）也立刻弃。
	need := end - start + 1
	if need <= 0 {
		return nil, 0
	}
	buf := make([]byte, need)
	n, err := io.ReadFull(resp.Body, buf)
	if n == 0 && err != nil {
		return nil, 0
	}
	return buf[:n], total
}

// ===== 展示格式化（浮窗直接用） =====

// chLayout 是声道布局名。只列常见包围盒排布（数 = LFE + 主 + 高度/后置），
// 表外直接落「N 声道」——宁可少个布局名，也不要把 12ch 硬套成 7.1。
var chLayout = map[int]string{
	1: "单声道", 2: "立体声", 3: "3.0", 4: "4.0", 5: "5.0",
	6: "5.1", 7: "6.1", 8: "7.1", 10: "5.1.4", 12: "7.1.4",
}

// FmtSampleRate 形如 "44.1 kHz"。
func (i *Info) FmtSampleRate() string {
	hz := 0
	if i != nil {
		hz = i.SampleRate
	}
	if hz <= 0 {
		return "—"
	}
	if hz%1000 != 0 {
		return strconv.FormatFloat(float64(hz)/1000, 'f', 1, 64) + " kHz"
	}
	return strconv.Itoa(hz/1000) + " kHz"
}

// FmtBitDepth 形如 "24 bit"；有损格式没有采样精度，落 "—"。
func (i *Info) FmtBitDepth() string {
	bits := 0
	if i != nil {
		bits = i.BitDepth
	}
	if bits <= 0 {
		return "—"
	}
	return strconv.Itoa(bits) + " bit"
}

// FmtBitrate 形如 "320 kbps"；平均码率加 ≈ 前缀。
func (i *Info) FmtBitrate() string {
	if i == nil || i.Bitrate <= 0 {
		return "—"
	}
	pre := ""
	if i.BitrateApprox {
		pre = "≈"
	}
	return pre + strconv.Itoa(int(i.Bitrate+0.5)) + " kbps"
}

// FmtChannels 形如 "2（立体声）"。
func (i *Info) FmtChannels() string {
	ch := 0
	if i != nil {
		ch = i.Channels
	}
	if ch <= 0 {
		return "—"
	}
	if name, ok := chLayout[ch]; ok {
		return strconv.Itoa(ch) + "（" + name + "）"
	}
	return strconv.Itoa(ch) + " 声道"
}

// le32 读小端 4 字节。
func le32(b []byte, at int) uint32 {
	return uint32(b[at]) | uint32(b[at+1])<<8 | uint32(b[at+2])<<16 | uint32(b[at+3])<<24
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
