//go:build linux && (amd64 || arm64)

package audio

import (
	"sync"

	"github.com/ebitengine/purego"
)

// avLogFatal 是 FFmpeg 的 AV_LOG_FATAL。只保留真正的致命级日志：
// 帧重同步告警是 AV_LOG_ERROR（16），会被压掉。
const avLogFatal = 8

var silenceOnce sync.Once

// silenceFFmpegLogs 关掉 FFmpeg 的默认 stderr 日志。
//
// 为什么需要：拖动进度时 FLAC 会从帧中间重同步，FFmpeg 会打
//
//	[flac @ 0x...] invalid sync code / invalid frame header / decode_frame() failed
//
// 这是预期内的解码器告警（Liino 的 native 后端会跳过损坏帧继续读），不是
// 播放失败；但 FFmpeg 默认把它写到进程 stderr，拖一次进度刷一次，看起来像
// 出错。真正的失败仍由 Liino 的事件/返回错误上报，不依赖 stderr。
//
// ffgo 的 SetLogLevel 要额外的 libffshim，默认不随包分发；这里直接对已加载
// 的 libavutil 调 av_log_set_level。ffgo 用 RTLD_GLOBAL 加载 libavutil，先
// 用同一组版本名拿到句柄即可；失败时保持原样（最多继续有日志，不影响播放）。
func silenceFFmpegLogs() {
	silenceOnce.Do(func() {
		for _, name := range []string{
			"libavutil.so.60", "libavutil.so.59", "libavutil.so.58",
			"libavutil.so.57", "libavutil.so.56", "libavutil.so",
		} {
			h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				continue
			}
			sym, err := purego.Dlsym(h, "av_log_set_level")
			if err != nil {
				continue
			}
			var setLevel func(int32)
			purego.RegisterFunc(&setLevel, sym)
			setLevel(avLogFatal)
			return
		}
	})
}
