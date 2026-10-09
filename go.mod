module github.com/Team-Quaver/quaver-astra

go 1.27.1

require (
	github.com/egoist/mygo v0.2.15
	github.com/jixunmoe-go/qrc v0.0.0-20230917162828-866e996416b0
	github.com/team-quaver/typhoeus-go v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.57.0
	golang.org/x/image v0.46.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// Typhoeus-go（QQ 音乐后端）以子模块形式随仓库分发，进程内嵌入。
replace github.com/team-quaver/typhoeus-go => ./third_party/Typhoeus-go
