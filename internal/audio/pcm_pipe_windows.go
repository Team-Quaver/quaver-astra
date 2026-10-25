//go:build windows

package audio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"sync"
)

func isWindows() bool { return true }

// Windows 没有 mkfifo：mpv 以 fopen 写 \\.\pipe\<name>，这里用 winio 风格的
// 本地管道监听器接收。Go 的 net.Listen("pipe", ...) 在 go1.27 的 windows 后端
// 提供（internal/poll 已支持命名管道 server）。
func makeFIFO(path string) error                      { return nil }
func openFIFOReadWrite(path string) (*os.File, error) { return nil, fmt.Errorf("not used on windows") }

// startWinPipeServer 创建一个命名管道服务端；mpv 连上后回调 onConn。
// Go 标准库从 1.24 起 windows 后端支持 net.Listen("pipe", name)。
func startWinPipeServer(path string, onConn func(net.Conn)) (net.Listener, error) {
	ln, err := net.Listen("pipe", path)
	if err != nil {
		return nil, fmt.Errorf("pcm: 创建命名管道失败: %w", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			onConn(c)
		}
	}()
	return ln, nil
}

var _ = sync.Mutex{}
var _ = rand.Read
var _ = hex.EncodeToString
