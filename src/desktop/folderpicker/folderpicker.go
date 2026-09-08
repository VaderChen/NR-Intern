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

// Save 以原生存檔面板詢問儲存位置，回傳使用者選定的路徑。
//
// 存在的理由：桌面端的 WebView 沒有下載處理，網頁的 <a download> 會被靜默丟掉，
// 使用者按了按鈕什麼都不會發生。瀏覽器不走這條路——那邊的下載本來就正常。
func Save(ctx context.Context, suggestedName string) (string, error) {
	return save(ctx, suggestedName)
}

// DroppedFiles 讀取目前由作業系統拖入 Desktop 視窗的檔案路徑。
// 路徑只供 Desktop 層立即讀取，不直接回傳給前端。
func DroppedFiles(ctx context.Context) ([]string, error) {
	return droppedFiles(ctx)
}
