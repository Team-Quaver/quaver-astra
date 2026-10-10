//go:build linux

package audio

import (
	"os/exec"
	"syscall"
)

// configureMPVProcess 让 mpv 随宿主死亡，并把 AppRun 与它派生的真正
// worker 放进同一个进程组。前者兜住 SIGABRT 等绕过 defer 的异常退出；
// 后者保证随包 mpv 的 launcher 自身 exec/派生子进程时也不会残留。
func configureMPVProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
}

// terminateMPVProcess 终止整个 mpv 进程组；失败时至少终止直接子进程。
func terminateMPVProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
