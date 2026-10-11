//go:build windows

package audio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procWaitNamedPipe = kernel32.NewProc("WaitNamedPipeW")
)

// Windows 上 mpv 只为 --input-ipc-server 创建命名管道：给裸名字时 mpv 自动
// 挂到 \\.\pipe\<name> 下（见 mpv 手册 input-ipc-server 一节）。AF_UNIX 在
// 这条路径上不可用，dial 一个文件路径必然失败——这也是此前随包 mpv 明明
// 存在却「认不出来」的根因。这里改用 CreateFile 连命名管道。
func ipcSetup() (string, func() (ipcConn, error), func(), error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, nil, err
	}
	name := "quaver-astra-mpv-" + hex.EncodeToString(raw[:])
	dial := func() (ipcConn, error) { return dialNamedPipe(name) }
	return name, dial, func() {}, nil
}

func dialNamedPipe(name string) (ipcConn, error) {
	path := `\\.\pipe\` + name
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		h, err := windows.CreateFile(p16,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			return pipeConn{File: os.NewFile(uintptr(h), path)}, nil
		}
		if err == windows.ERROR_PIPE_BUSY {
			// 管道实例全被占用时等服务端释放；x/sys/windows 没包这层薄壳。
			procWaitNamedPipe.Call(uintptr(unsafe.Pointer(p16)), 100)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("连接 mpv 命名管道失败: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pipeConn 适配 ipcConn；命名管道没有超时语义，deadline 置空即可。
type pipeConn struct{ *os.File }

func (pipeConn) SetReadDeadline(time.Time) error { return nil }
