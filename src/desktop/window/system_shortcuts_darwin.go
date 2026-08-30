//go:build darwin && cgo

package window

/*
void nr_install_standard_menus(void);
void nr_activate_application(void);
void nr_install_window_lifecycle(void *window_handle);
void nr_restore_application_window(void);
void nr_hide_application_window(void);
void nr_set_conversation_active(int active);
void nr_uninstall_window_lifecycle(void);
*/
import "C"

import "unsafe"

// installSystemEditShortcuts 只補 Cocoa/WKWebView 不會自動建立的標準 Edit menu。
// Windows WebView2、Linux 瀏覽器與外部 Browser 由各自引擎處理 Ctrl 快捷鍵。
func installSystemEditShortcuts() {
	C.nr_install_standard_menus()
}

// activateApplication 確保由啟動腳本或 LaunchServices 建立的視窗切到前景。
func activateApplication() {
	C.nr_activate_application()
}

// installWindowLifecycle 讓 macOS 關閉按鈕在工作進行中只隱藏 UI，並保留
// webview event loop 與後端程序；沒有工作時仍交回 webview 的原生關閉流程。
func installWindowLifecycle(window unsafe.Pointer) {
	C.nr_install_window_lifecycle(window)
}

func restoreApplicationWindow() {
	C.nr_restore_application_window()
}

func hideApplicationWindow() {
	C.nr_hide_application_window()
}

func setConversationActive(active bool) {
	value := C.int(0)
	if active {
		value = 1
	}
	C.nr_set_conversation_active(value)
}

func uninstallWindowLifecycle() {
	C.nr_uninstall_window_lifecycle()
}
