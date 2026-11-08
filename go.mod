module github.com/Team-Quaver/quaver-astra

go 1.27.3

require (
	github.com/ebitengine/purego v0.11.1
	github.com/egoist/mygo v0.7.4
	github.com/godbus/dbus/v5 v5.2.2
	github.com/jixunmoe-go/qrc v0.0.0-20230917162828-866e996416b0
	github.com/team-quaver/liino v0.0.0-00010101000000-000000000000
	github.com/team-quaver/typhoeus-go v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.57.0
	golang.org/x/image v0.46.0
	golang.org/x/sys v0.49.0
)

require (
	github.com/bnema/purego v0.11.0-bnema.3 // indirect
	github.com/bnema/purego-pipewire v0.1.6 // indirect
	github.com/deploymenttheory/go-bindings-macosplatform v0.20.1 // indirect
	github.com/deploymenttheory/go-bindings-win32 v0.5.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/jfreymuth/oggvorbis v1.0.5 // indirect
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	github.com/team-quaver/ffgo v0.0.0 // indirect
	github.com/thesyncim/gopus v0.2.3 // indirect
	github.com/vcaesar/audio-go v0.10.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// Typhoeus-go（QQ 音乐后端）以子模块形式随仓库分发，进程内嵌入。
replace github.com/team-quaver/typhoeus-go => ./third_party/Typhoeus-go

// Liino（原生音频管线）同样以子模块随仓库分发。它是独立 module，其
// go.mod 里的 replace 对下游模块不生效，所以这里必须一并转发它自带的
// 两个本地 fork：ffgo（动态加载系统 FFmpeg）与 purego-pipewire。
replace github.com/team-quaver/liino => ./third_party/Liino

replace github.com/team-quaver/ffgo => ./third_party/Liino/third_party/ffgo

replace github.com/bnema/purego-pipewire => ./third_party/Liino/third_party/purego-pipewire

// ffgo / purego-pipewire 用的是 bnema 的 purego fork，MyGo 与我们自己的 FFI
// 用的是 ebitengine/purego。两份 purego 的 dlfcn_stubs.s 都定义全局符号
// `dlopen`，同时链接会报 duplicated definition。shim 保留 bnema 的导入路径，
// 但把所有符号转发给 ebitengine/purego，二进制里只剩一份汇编桩。
replace github.com/bnema/purego => ./third_party/purego-shim

// MyGo Linux GtkGLArea render callbacks must stay on the native thread that
// owns the current OpenGL context; see third_party/mygo/internal/linux/surface.go.
replace github.com/egoist/mygo => ./third_party/mygo
