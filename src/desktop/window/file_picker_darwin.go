//go:build darwin && cgo

package window

/*
#cgo CFLAGS: -fblocks
int nr_install_file_picker(void *window_handle);
*/
import "C"

import "unsafe"

// installFilePicker 補上有明確生命週期的 WKUIDelegate，供所有 HTML file input 使用。
func installFilePicker(window unsafe.Pointer) bool {
	return C.nr_install_file_picker(window) != 0
}
