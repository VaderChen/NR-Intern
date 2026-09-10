//go:build windows && cgo

package window

import (
	"syscall"
	"unsafe"
)

// Windows 的視窗生命週期輔助。
//
// macOS 那邊要處理 Dock、App Nap 與關閉鈕攔截；Windows 這幾件事的語意不同，
// 因此只實作真正需要的兩個動作——顯示與隱藏主視窗，讓 Tray 的「開啟」與
// Console 的「擷取畫面時隱藏視窗」有東西可以呼叫。

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procIsIconic        = user32.NewProc("IsIconic")
	procSetActiveWindow = user32.NewProc("SetActiveWindow")
)

const (
	swHide    = 0
	swShow    = 5
	swRestore = 9
)

// mainWindow 記住 webview 建立的視窗，供 Tray 與前端回呼使用。
// 只有一個原生視窗，不需要更複雜的登錄機制。
var mainWindow uintptr

// installSystemEditShortcuts 在 Windows 不注入選單：WebView2 本身就處理
// Ctrl-C/X/V/A/Z，另外掛一份只會兩邊搶同一組快捷鍵。
func installSystemEditShortcuts() {}

func installWindowLifecycle(window unsafe.Pointer) {
	mainWindow = uintptr(window)
}

func uninstallWindowLifecycle() {
	mainWindow = 0
}

// restoreWindowFrame 目前不還原上次的位置與大小。
//
// macOS 版存的是 NSWindow frame；Windows 對應的是 WINDOWPLACEMENT，要另外
// 決定存在哪、以及多螢幕變動時如何處理。先讓視窗以預設大小開啟，寧可少一個
// 功能，也不要把視窗還原到已經不存在的螢幕座標上。
func restoreWindowFrame(_ unsafe.Pointer) {}

// setConversationActive 在 Windows 沒有對應動作。
// macOS 用它避免 App Nap 在對話進行中降低背景執行緒優先度；Windows 沒有
// 等價機制，硬做一個只會多出無人維護的程式碼。
func setConversationActive(_ bool) {}

func activateApplication() {
	if mainWindow == 0 {
		return
	}
	procSetForeground.Call(mainWindow)
	procSetActiveWindow.Call(mainWindow)
}

func hideApplicationWindow() {
	if mainWindow == 0 {
		return
	}
	procShowWindow.Call(mainWindow, swHide)
}

func restoreApplicationWindow() {
	if mainWindow == 0 {
		return
	}
	// 最小化時要用 SW_RESTORE，SW_SHOW 只會讓它留在工作列。
	if minimised, _, _ := procIsIconic.Call(mainWindow); minimised != 0 {
		procShowWindow.Call(mainWindow, swRestore)
	} else {
		procShowWindow.Call(mainWindow, swShow)
	}
	procSetForeground.Call(mainWindow)
}
