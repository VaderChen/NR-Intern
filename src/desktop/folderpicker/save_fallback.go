//go:build !darwin && !windows

package folderpicker

import "context"

// 其他平台沒有原生存檔面板的實作。前端收到 ErrUnavailable 會退回瀏覽器
// 自己的下載行為——Windows 的 WebView2 有內建下載處理，那條路本來就會動。
func save(_ context.Context, _ string) (string, error) {
	return "", ErrUnavailable
}
