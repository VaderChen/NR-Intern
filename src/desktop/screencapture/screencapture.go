package screencapture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
)

var (
	ErrCanceled         = errors.New("screen capture was canceled")
	ErrUnavailable      = errors.New("native screen capture is unavailable")
	ErrPermissionDenied = errors.New("screen recording permission is required")
)

type Status string

const (
	StatusCopied   Status = "copied"
	StatusLaunched Status = "launched"
)

type Result struct {
	Status Status
	PNG    []byte
}

// Capture 開啟目前作業系統的互動畫面擷取介面，把原圖交給系統剪貼簿，
// 並在平台可提供時回傳 PNG，供桌面 UI 立即進入標註編輯。
func Capture(ctx context.Context) (Result, error) {
	return capture(ctx)
}

// CopyPNGToClipboard 驗證並以原生方式更新系統剪貼簿中的 PNG 圖像。
func CopyPNGToClipboard(ctx context.Context, value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("PNG 圖像不可為空")
	}
	config, err := png.DecodeConfig(bytes.NewReader(value))
	if err != nil {
		return fmt.Errorf("解析 PNG 圖像: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 32768 || config.Height > 32768 {
		return fmt.Errorf("PNG 圖像尺寸超出支援範圍")
	}
	if uint64(config.Width)*uint64(config.Height) > 100_000_000 {
		return fmt.Errorf("PNG 圖像像素總數超出支援範圍")
	}
	return copyPNGToClipboard(ctx, value)
}
