//go:build !windows

package audio

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func isWindows() bool { return false }

// makeFIFO 在 path 创建 FIFO（mkfifo 语义，0600）。
func makeFIFO(path string) error {
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return fmt.Errorf("pcm: 创建 FIFO 失败: %w", err)
	}
	return nil
}

// openFIFOReadWrite 以 O_RDWR 打开 FIFO 读端：writer 关闭后不会立刻 EOF，
// 同一代内 mpv 重开写端（seek 重挂用新 FIFO，正常不会发生）也能继续收。
func openFIFOReadWrite(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("pcm: 打开 FIFO 失败: %w", err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

// startWinPipeServer 在非 Windows 上永远不会被调用（open() 走不到该分支），
// 仅为满足编译放一个占位。
func startWinPipeServer(path string, onConn func(net.Conn)) (net.Listener, error) {
	return nil, fmt.Errorf("pcm: named pipes are not available on this platform")
}
