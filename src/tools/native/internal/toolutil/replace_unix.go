//go:build !windows

package toolutil

import "os"

func replacePath(source, target string) error {
	return os.Rename(source, target)
}

// 同目錄硬連結會原子發布完整檔案，既有 target（含 symlink）一律拒絕。
// 不支援硬連結的檔案系統明確回報錯誤，不退回可能曝光半成品的寫法。
func publishNewPath(source, target string) error {
	return os.Link(source, target)
}
