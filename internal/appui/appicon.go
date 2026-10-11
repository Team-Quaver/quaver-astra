package appui

import (
	"github.com/Team-Quaver/quaver-astra/img"

	"github.com/egoist/mygo/ui"
)

// appIconSVG 是设置页「关于」使用的矢量产品图标。它和系统窗口图标来自
// 同一份品牌资源；托盘图标是另一套单独资源，不在这里复用。
var appIconSVG = ui.MustParseSVG(appicon.QuaverAstraSVG)

// AppIconPNG 返回系统窗口/应用图标使用的 PNG。MyGo 的桌面 API 只接收
// PNG，矢量源仍保留在 img/quaver-astra.svg。
func AppIconPNG() []byte { return appicon.QuaverAstraPNG }
