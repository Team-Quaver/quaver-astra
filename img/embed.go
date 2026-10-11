// Package appicon embeds Quaver Astra's product branding assets.
package appicon

import _ "embed"

//go:embed quaver-astra.svg
var QuaverAstraSVG []byte

//go:embed app-icon.png
var QuaverAstraPNG []byte
