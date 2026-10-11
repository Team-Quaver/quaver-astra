//go:build !windows

package audio

import (
	"net"
	"os"
	"path/filepath"
)

// Unix/macOS：常规 AF_UNIX socket，mpv 与 Go 两端语义一致。
func ipcSetup() (string, func() (ipcConn, error), func(), error) {
	dir, err := os.MkdirTemp("", "quaver-astra-mpv-")
	if err != nil {
		base, berr := os.UserCacheDir()
		if berr != nil {
			return "", nil, nil, err
		}
		if mkerr := os.MkdirAll(base, 0o700); mkerr != nil {
			return "", nil, nil, mkerr
		}
		dir, err = os.MkdirTemp(base, "quaver-astra-mpv-")
		if err != nil {
			return "", nil, nil, err
		}
	}
	sock := filepath.Join(dir, "ipc.sock")
	dial := func() (ipcConn, error) {
		c, derr := net.Dial("unix", sock)
		if derr != nil {
			return nil, derr
		}
		return c, nil
	}
	return sock, dial, func() { os.RemoveAll(dir) }, nil
}
