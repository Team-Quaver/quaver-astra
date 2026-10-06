// Package backend 把 Typhoeus-go（QQ 音乐 HTTP API）进程内嵌：
// 同一个二进制里起一个只监听 127.0.0.1 随机端口的 quaver-server。
//
// 凭证持久化：复用后端 external 模式的 QCRED1 交接协议——用 os.Pipe 接管
// 后端看到的 stdin/stdout（仅限构造窗口），启动时注入 vault 里保存的凭证，
// 登录/自动刷新/登出时后端回写 QCRED1 行，由排水协程解密落盘/清除。
// 明文凭证只在内存与 vault 密文之间流转，绝不直接落盘。
package backend

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/vault"

	quaverserver "github.com/team-quaver/typhoeus-go/server"
)

// Server 是内嵌的 Typhoeus-go 服务。
type Server struct {
	srv       *http.Server
	ln        net.Listener
	stdoutR   *os.File
	stdoutW   *os.File
	drainDone chan struct{}
}

// Start 在 127.0.0.1 的随机端口上启动内嵌后端。
//
// 必须在调用前设好配置目录：quaver-server 在 NewApp() 时读取
// QUAVER_CONFIG_DIR 并在那里落设备指纹。凭证走 external 模式 +
// 管道交接（vault 为 nil 时按"无已存凭证"处理，登录态仍会在登录后
// 交给 vault 保存——v 为 nil 时不落盘，行为退化为 memory）。
func Start(configDir string, v *vault.Vault) (*Server, error) {
	if configDir != "" {
		_ = os.Setenv("QUAVER_CONFIG_DIR", configDir)
	}
	// external 模式只在 NewSession() 构造时读取 os.Stdin/os.Stdout：
	// 先换上管道，构造完立刻还原，应用自身的 stdio 不受影响。
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	// 注入首行：有保存的凭证给凭证，否则 null（保持未登录）。
	inject := "QCRED1 null\n"
	if v != nil {
		if raw, err := v.Load(); err == nil {
			inject = "QCRED1 " + string(raw) + "\n"
		}
	}
	if _, err := stdinW.WriteString(inject); err != nil {
		return nil, err
	}
	_ = stdinW.Close() // 首行读完后 stdin 不再被使用

	savedStdin, savedStdout := os.Stdin, os.Stdout
	_ = os.Setenv("QUAVER_CREDENTIAL_MODE", "external")
	os.Stdin, os.Stdout = stdinR, stdoutW
	app, appErr := quaverserver.NewApp()
	os.Stdin, os.Stdout = savedStdin, savedStdout
	_ = os.Unsetenv("QUAVER_CREDENTIAL_MODE")
	if appErr != nil {
		return nil, appErr
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		srv:       &http.Server{Handler: app.Routes(), ReadHeaderTimeout: 10 * time.Second},
		ln:        ln,
		stdoutR:   stdoutR,
		stdoutW:   stdoutW,
		drainDone: make(chan struct{}),
	}
	// 排水：后端在登录/自动刷新/登出时回写 QCRED1 行
	go s.drain(v)
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// drain 消费后端 stdout 管道上的凭证交接行。
func (s *Server) drain(v *vault.Vault) {
	defer close(s.drainDone)
	sc := bufio.NewScanner(s.stdoutR)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, quaverserver.HandoffPrefix) {
			continue // 其余输出按日志丢弃
		}
		if v == nil {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, quaverserver.HandoffPrefix))
		if payload == "" || payload == "null" {
			_ = v.Clear()
			continue
		}
		if err := v.Save([]byte(payload)); err != nil {
			os.Stderr.WriteString("quaver-astra: 凭证保存失败: " + err.Error() + "\n")
		}
	}
}

// BaseURL 返回 http://127.0.0.1:<port>。
func (s *Server) BaseURL() string { return "http://" + s.ln.Addr().String() }

// Shutdown 停掉内嵌 HTTP 服务并收掉管道。
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	_ = s.stdoutW.Close()
	select {
	case <-s.drainDone:
	case <-time.After(time.Second):
	}
}
