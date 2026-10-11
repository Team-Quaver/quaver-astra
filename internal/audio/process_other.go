//go:build !linux && !windows

package audio

import (
	"io"
	"os"
	"os/exec"
)

// configureMPVProcess 在其他平台保留 exec.Cmd 默认进程模型。
func configureMPVProcess(*exec.Cmd) {}

// terminateMPVProcess 终止直接启动的 mpv 进程。
func terminateMPVProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// mpvStderr 转发 mpv 的诊断输出，便于终端裸跑时排查启动问题。
func mpvStderr() io.Writer { return os.Stderr }
