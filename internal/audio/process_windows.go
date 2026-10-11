//go:build windows

package audio

import (
	"io"
	"os/exec"

	"golang.org/x/sys/windows"
)

// configureMPVProcess 抑制控制台弹窗：CREATE_NO_WINDOW 让控制台子系统
// 子进程（比如用户经 QAA_MPV 指到的 mpv.com）不再闪黑框。
func configureMPVProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// terminateMPVProcess 终止直接启动的 mpv 进程。
func terminateMPVProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// mpvStderr：windowsgui 主进程通常没有控制台，把无效句柄转发给子进程会让
// CreateProcess 直接失败；mpv 本身 --no-terminal --really-quiet，无需转发。
func mpvStderr() io.Writer { return nil }
