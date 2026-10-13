// Package appicon embeds Quaver Astra's product branding assets.
package appicon

import _ "embed"

//go:embed quaver-astra.svg
var QuaverAstraSVG []byte

//go:embed app-icon.png
var QuaverAstraPNG []byte

// QuaverAstraStarlessSVG 是启动画面用的产品图标（去掉星星与底板的版本）：
// 先淡入它，再让星星单独亮起。
//
//go:embed splash-logo.svg
var QuaverAstraStarlessSVG []byte

// QuaverAstraStarSVG 是产品图标里的星星，与无星版共用 512 视图，
// 叠加时对位精确。两个文件都由 quaver-astra.svg 拆出，改母版后要重新生成。
//
//go:embed splash-star.svg
var QuaverAstraStarSVG []byte

// QuaverWordmarkSVG 是启动画面的 “Quaver” 字标（Comfortaa Bold 转曲），
// 以 Icon 方式绘制、跟随文字颜色。
//
//go:embed wordmark-quaver.svg
var QuaverWordmarkSVG []byte

// AstraWordmarkSVG 是启动画面的 “Astra” 手写体字标（Allura 转曲），
// 以 Icon 方式绘制、跟随文字颜色。
//
//go:embed wordmark-astra.svg
var AstraWordmarkSVG []byte
