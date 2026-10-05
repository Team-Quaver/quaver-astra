// Package backend 把 Typhoeus-go（QQ 音乐 HTTP API）进程内嵌：
// 同一个二进制里起一个只监听 127.0.0.1 随机端口的 quaver-server。
package backend

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	quaverserver "github.com/team-quaver/typhoeus-go/server"
)

// Server 是内嵌的 Typhoeus-go 服务。
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Start 在 127.0.0.1 的随机端口上启动内嵌后端。
//
// 必须在调用前设好配置目录：quaver-server 在 NewApp() 时读取
// QUAVER_CONFIG_DIR 并在那里落设备指纹。凭证模式保持默认的 memory
// （绝不设 QUAVER_CREDENTIAL_MODE=external —— 那会独占 stdin/stdout），
// 登录态只驻内存，重启后需重新扫码。
func Start(configDir string) (*Server, error) {
	if configDir != "" {
		_ = os.Setenv("QUAVER_CONFIG_DIR", configDir)
	}
	app, err := quaverserver.NewApp()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		srv: &http.Server{Handler: app.Routes(), ReadHeaderTimeout: 10 * time.Second},
		ln:  ln,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// BaseURL 返回 http://127.0.0.1:<port>。
func (s *Server) BaseURL() string { return "http://" + s.ln.Addr().String() }

// Shutdown 停掉内嵌 HTTP 服务。
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}
