//go:build !linux && !windows && !darwin

package smedia

// New 返回 no-op 控制器（该平台没有系统媒体控制中心投影）。
func New(identity string, cmds *Commands) Controller {
	return noop{}
}
