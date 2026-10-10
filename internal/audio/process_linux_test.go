//go:build linux

package audio

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestConfigureMPVProcess(t *testing.T) {
	cmd := exec.Command("mpv")
	configureMPVProcess(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid = false, want true")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}
