//go:build !darwin || !cgo

package window

// installSystemEditShortcuts 在非 Cocoa 平台不注入選單。
// 這些平台使用 WebView／Browser 原生的 Ctrl-C/X/V/A/Z responder。
func installSystemEditShortcuts() {}
