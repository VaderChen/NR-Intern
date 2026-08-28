// Package logging 提供元件共用的 logger 取得方式。
//
// 核心層過去完全沒有 log：出事時只有 Run event log 可看，而它只涵蓋 run 內部
// 且只有被明確 emit 的內容。這裡刻意只提供最小介面，讓各元件以欄位記錄
// 名稱、時間與狀態，不記錄工具參數、工具輸出或訊息內容——那些可能含有
// 憑證與使用者資料，而且體積會淹沒紀錄。
package logging

import (
	"io"
	"log/slog"
)

// Or 讓未注入 logger 的元件仍然可用，測試與零值組裝不需要額外設定。
func Or(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// Discard 回傳不輸出任何內容的 logger，供測試使用。
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
