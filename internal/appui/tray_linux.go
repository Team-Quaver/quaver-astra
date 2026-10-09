//go:build linux

package appui

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// libayatana-appindicator3 的旧入口在 Fedora/部分发行版会主动打弃用警告，
// 但当前 MyGo Linux 托盘仍依赖它。这里只吞掉该库的 warning 级弃用提示，
// 不隐藏 critical/error；等系统提供 libayatana-appindicator-glib 后无需此兼容层。
var (
	appIndicatorLogDomain = []byte("libayatana-appindicator\x00")
	appIndicatorLogNoop   = purego.NewCallback(func(domain, level, message, data uintptr) {})
)

func silenceAppIndicatorDeprecationWarning() {
	lib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	fn, err := purego.Dlsym(lib, "g_log_set_handler")
	if err != nil {
		return
	}
	const gLogLevelWarning = 1 << 4
	purego.SyscallN(fn,
		uintptr(unsafe.Pointer(&appIndicatorLogDomain[0])),
		uintptr(gLogLevelWarning),
		appIndicatorLogNoop,
		0,
	)
}
