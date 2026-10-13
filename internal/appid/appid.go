// Package appid 持有系统侧应用标识符（反向 DNS），是 Windows 资源、macOS
// bundle 与 Linux XDG / MPRIS 注册共用的单一来源。改这里就改所有平台。
package appid

// Identifier 是应用的反向 DNS 标识符：macOS 的 CFBundleIdentifier、Linux
// 的 desktop-entry id（含 .desktop 后缀前的末段）都用它。
const Identifier = "red.0w0.quaver-astra"
