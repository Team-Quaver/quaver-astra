//go:build !linux && !windows

package smedia

// New 返回 no-op 控制器（macOS 暂无系统媒体中心投影）。
func New(identity string, cmds *Commands) Controller {
	return noop{}
}
