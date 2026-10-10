//go:build !linux

package audio

import "os/exec"

// configureMPVProcess 在其他平台保留 exec.Cmd 默认进程模型。
func configureMPVProcess(*exec.Cmd) {}

// terminateMPVProcess 终止直接启动的 mpv 进程。
func terminateMPVProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
