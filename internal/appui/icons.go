package appui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// 图标：主体路径移植自主项目 ui/src/lib/icons.ts / QML 版 Icons.js
// （24 网格线性风格，stroke-width 1.8）。COL 占位符替换为 currentColor，
// 使图标随文本色变化。
var iconBodies = map[string]string{
	"play":      `<path d="M8 5.5v13l10-6.5z" fill="COL" stroke="none"/>`,
	"pause":     `<rect x="7" y="5" width="3.4" height="14" rx="1" fill="COL" stroke="none"/><rect x="13.6" y="5" width="3.4" height="14" rx="1" fill="COL" stroke="none"/>`,
	"prev":      `<path d="M7 5v14M20 5.5v13L10 12z" fill="COL" stroke="COL" stroke-width="1.6"/>`,
	"next":      `<path d="M17 5v14M4 5.5v13L14 12z" fill="COL" stroke="COL" stroke-width="1.6"/>`,
	"home":      `<path d="M4 11.2 12 4.5l8 6.7"/><path d="M6 10v9.5h12V10"/>`,
	"guess":     `<path d="M12 3.5c.6 3.8 2.7 5.9 6.5 6.5-3.8.6-5.9 2.7-6.5 6.5-.6-3.8-2.7-5.9-6.5-6.5 3.8-.6 5.9-2.7 6.5-6.5z" fill="COL" stroke="none"/>`,
	"daily":     `<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="2" fill="COL" stroke="none"/>`,
	"heart":     `<path d="M12 20s-7-4.6-9-9c-1.3-3 .8-6.5 4-6.5 2 0 3.5 1.2 5 3 1.5-1.8 3-3 5-3 3.2 0 5.3 3.5 4 6.5-2 4.4-9 9-9 9z"/>`,
	"heartFill": `<path d="M12 20s-7-4.6-9-9c-1.3-3 .8-6.5 4-6.5 2 0 3.5 1.2 5 3 1.5-1.8 3-3 5-3 3.2 0 5.3 3.5 4 6.5-2 4.4-9 9-9 9z" fill="COL" stroke="COL"/>`,
	"star":      `<path d="M12 3.6l2.6 5.3 5.8.85-4.2 4.1 1 5.78L12 16.9l-5.2 2.73 1-5.78-4.2-4.1 5.8-.85z"/>`,
	"search":    `<circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.4-4.4"/>`,
	"back":      `<path d="M14.5 5.5 8 12l6.5 6.5"/>`,
	"settings":  `<circle cx="12" cy="12" r="3.1"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`,
	"collapse":  `<path d="M11.5 6.5 6 12l5.5 5.5"/><path d="M18 6.5 12.5 12l5.5 5.5"/>`,
	"expand":    `<path d="M6 6.5 11.5 12 6 17.5"/><path d="M12.5 6.5 18 12l-5.5 5.5"/>`,
	"loopOff":   `<path opacity=".45" d="M17 2l4 4-4 4"/><path d="M3 11V9a4 4 0 0 1 4-4h14"/><path opacity=".45" d="M7 22l-4-4 4-4"/><path d="M21 13v2a4 4 0 0 1-4 4H3"/><path d="M4 4l16 16"/>`,
	"loopAll":   `<path d="M17 2l4 4-4 4"/><path d="M3 11V9a4 4 0 0 1 4-4h14"/><path d="M7 22l-4-4 4-4"/><path d="M21 13v2a4 4 0 0 1-4 4H3"/>`,
	// loopOne 的 “1” 用路径绘制（MyGo SVG 不支持 <text>）
	"loopOne":    `<path d="M17 2l4 4-4 4"/><path d="M3 11V9a4 4 0 0 1 4-4h14"/><path d="M7 22l-4-4 4-4"/><path d="M21 13v2a4 4 0 0 1-4 4H3"/><path d="M10.8 9.8l1.6-1.2v6.8"/>`,
	"shuffle":    `<path d="M17 3l4 4-4 4"/><path d="M21 7H7a4 4 0 0 0-4 4v1"/><path d="M17 21l4-4-4-4"/><path d="M3 17h2.5a4 4 0 0 0 3.3-1.8l5.4-8.4A4 4 0 0 1 17.5 5H21"/>`,
	"queue":      `<path d="M4 6h11M4 11h11M4 16h7"/><path d="M17 13.5v6l4.5-3z" fill="COL" stroke="none"/>`,
	"more":       `<circle cx="12" cy="5.5" r="1.7" fill="COL" stroke="none"/><circle cx="12" cy="12" r="1.7" fill="COL" stroke="none"/><circle cx="12" cy="18.5" r="1.7" fill="COL" stroke="none"/>`,
	"clear":      `<path d="M6 6l12 12M18 6 6 18"/>`,
	"note":       `<circle cx="8" cy="17.5" r="3"/><path d="M11 17.5V5.5l8-2v11"/><circle cx="16" cy="14.5" r="3"/>`,
	"user":       `<circle cx="12" cy="8.5" r="4"/><path d="M4.5 20c1.2-3.6 4.1-5.5 7.5-5.5s6.3 1.9 7.5 5.5"/>`,
	"volLow":     `<path d="M4 9v6h4l5 4V5L8 9H4z" fill="COL" stroke="none"/>`,
	"volMid":     `<path d="M4 9v6h4l5 4V5L8 9H4z" fill="COL" stroke="none"/><path d="M16 9a4.5 4.5 0 0 1 0 6"/>`,
	"volHigh":    `<path d="M4 9v6h4l5 4V5L8 9H4z" fill="COL" stroke="none"/><path d="M16 9a4.5 4.5 0 0 1 0 6"/><path d="M18.6 6.4a8 8 0 0 1 0 11.2"/>`,
	"volMute":    `<path d="M4 9v6h4l5 4V5L8 9H4z" fill="COL" stroke="none"/><path d="M16.5 9.5l5 5M21.5 9.5l-5 5"/>`,
	"winMin":     `<path d="M5.5 12h13"/>`,
	"winMax":     `<rect x="6" y="6" width="12" height="12" rx="1.6"/>`,
	"winRestore": `<rect x="6" y="8" width="10" height="10" rx="1.6"/><path d="M9 8V6.5A1.5 1.5 0 0 1 10.5 5h7A1.5 1.5 0 0 1 19 6.5v7a1.5 1.5 0 0 1-1.5 1.5H16"/>`,
	"winClose":   `<path d="M6.5 6.5l11 11M17.5 6.5l-11 11"/>`,
	"spinner":    `<circle cx="12" cy="12" r="8" opacity=".25"/><path d="M12 4a8 8 0 0 1 8 8"/>`,
	"retry":      `<path d="M20 12a8 8 0 1 1-2.34-5.66"/><path d="M20 4v4h-4"/>`,
	"trash":      `<path d="M4 7h16"/><path d="M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7"/><path d="M6.3 7l.7 11.2A1.8 1.8 0 0 0 8.8 20h6.4a1.8 1.8 0 0 0 1.8-1.8L17.7 7"/><path d="M10 11v5.5M14 11v5.5"/>`,
}

// Icons 是解析好的图标集（进程内一次构建）。
var Icons = func() map[string]*ui.SVG {
	out := make(map[string]*ui.SVG, len(iconBodies))
	for name, body := range iconBodies {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` +
			strings.ReplaceAll(body, "COL", "currentColor") + `</svg>`
		out[name] = ui.MustParseSVG([]byte(svg))
	}
	return out
}()
