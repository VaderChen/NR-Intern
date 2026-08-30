package window

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("native desktop window is unavailable")

type Options struct {
	Title   string
	URL     string
	Width   int
	Height  int
	Debug   bool
	OnReady func()
	Restore <-chan struct{}
}

// Run 會阻塞直到使用者關閉視窗或 context 被取消。
func Run(ctx context.Context, options Options) error {
	if options.Title == "" {
		options.Title = "NR-Intern"
	}
	if options.Width <= 0 {
		options.Width = 1280
	}
	if options.Height <= 0 {
		options.Height = 820
	}
	return run(ctx, options)
}
