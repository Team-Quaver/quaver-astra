package audio

import (
	"strconv"
	"strings"

	"github.com/team-quaver/liino"
	"github.com/team-quaver/liino/decode"
	"github.com/team-quaver/liino/output"
)

// Status 是设置页「关于」展示的播放后端自检结果。
//
// Liino 没有外部可执行文件可探测，能自检的是它注册的解码后端与输出驱动：
// 每一项都 Probe 实际加载所需运行库（Linux 上是系统 FFmpeg 的 libav*，
// PipeWire 的 libpipewire-0.3），所以这里的结果就是「点播放能不能出声」。
type Status struct {
	Version string
	OK      bool

	// Decoder / Driver 是当前实际可用的后端名（以 " / " 连接）；空表示全部不可用。
	Decoder    string
	Driver     string
	DecoderAll []string // 每个解码后端 + 可用性说明，按优先级排序
	DriverAll  []string // 每个输出驱动 + 可用性说明
	Detail     string   // 失败原因或补充说明（给设置页直接展示）
}

// NewStatus 探测当前机器的 Liino 能力。它不构造 Player，也不打开设备。
func NewStatus() Status {
	st := Status{Version: liino.Version}

	var decOK []string
	for _, d := range decode.Statuses() {
		if d.Available {
			decOK = append(decOK, d.Name)
			line := d.Name + " 可用"
			if len(d.Codecs) > 0 {
				line += "（" + strings.Join(d.Codecs, " / ") + "）"
			}
			st.DecoderAll = append(st.DecoderAll, line)
			continue
		}
		line := d.Name + " 不可用"
		if d.Note != "" {
			line += "：" + d.Note
		}
		st.DecoderAll = append(st.DecoderAll, line)
	}
	st.Decoder = strings.Join(decOK, " / ")

	var drvOK []string
	for _, d := range output.Statuses() {
		if d.Name == "null" {
			// null 是测试/无头环境的计时器假设备，不该出现在用户可见的
			// 「可用输出」里。
			continue
		}
		if d.Available {
			drvOK = append(drvOK, d.Name)
			line := d.Name + " 可用"
			if d.Devices > 0 {
				line += "（" + strconv.Itoa(d.Devices) + " 个设备）"
			}
			st.DriverAll = append(st.DriverAll, line)
			continue
		}
		line := d.Name + " 不可用"
		if d.Note != "" {
			line += "：" + d.Note
		}
		st.DriverAll = append(st.DriverAll, line)
	}
	st.Driver = strings.Join(drvOK, " / ")

	st.OK = len(decOK) > 0 && len(drvOK) > 0
	switch {
	case len(decOK) == 0:
		st.Detail = "没有可用的解码后端。Linux 需安装 FFmpeg 共享库（libavformat/libavcodec/libavutil/libswresample）；Windows/macOS 请安装系统解码组件。"
	case len(drvOK) == 0:
		st.Detail = "没有可用的音频输出驱动。Linux 需要 PipeWire；Windows/macOS 使用系统音频服务。"
	}
	return st
}
