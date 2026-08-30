package tray

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("system tray is unavailable")

type Options struct {
	Title       string
	URL         string
	OpenOnStart bool
}

// Run 建立常駐 Tray 並阻塞到使用者選擇結束或 ctx 被取消。
// Tray 是桌面生命週期的一部分，不依賴 Browser 是否仍開著。
func Run(ctx context.Context, options Options) error {
	return run(ctx, options)
}
