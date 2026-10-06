module github.com/Team-Quaver/quaver-astra

go 1.27.1

require (
	github.com/ebitengine/oto/v3 v3.5.1
	github.com/egoist/mygo v0.2.12
	github.com/hajimehoshi/go-mp3 v0.3.4
	github.com/mewkiz/flac v1.0.14
	github.com/team-quaver/typhoeus-go v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.57.0
	golang.org/x/image v0.46.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// Typhoeus-go（QQ 音乐后端）以子模块形式随仓库分发，进程内嵌入。
replace github.com/team-quaver/typhoeus-go => ./third_party/Typhoeus-go
