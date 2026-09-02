package folderpicker

import (
	"context"
	"errors"
)

var (
	ErrUnavailable = errors.New("native folder picker is unavailable")
	ErrCanceled    = errors.New("folder selection was canceled")
)

// Pick 使用目前作業系統的原生介面選擇一個或多個資料夾。
func Pick(ctx context.Context) ([]string, error) {
	return pick(ctx)
}

// Dropped 讀取目前由作業系統拖入 Desktop 視窗的資料夾路徑。
func Dropped(ctx context.Context) ([]string, error) {
	return dropped(ctx)
}

// DroppedFiles 讀取目前由作業系統拖入 Desktop 視窗的檔案路徑。
// 路徑只供 Desktop 層立即讀取，不直接回傳給前端。
func DroppedFiles(ctx context.Context) ([]string, error) {
	return droppedFiles(ctx)
}
