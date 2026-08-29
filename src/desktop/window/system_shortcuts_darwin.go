//go:build darwin && cgo

package window

/*
void nr_install_standard_menus(void);
void nr_activate_application(void);
*/
import "C"

// installSystemEditShortcuts 只補 Cocoa/WKWebView 不會自動建立的標準 Edit menu。
// Windows WebView2、Linux 瀏覽器與外部 Browser 由各自引擎處理 Ctrl 快捷鍵。
func installSystemEditShortcuts() {
	C.nr_install_standard_menus()
}

// activateApplication 確保由啟動腳本或 LaunchServices 建立的視窗切到前景。
func activateApplication() {
	C.nr_activate_application()
}
