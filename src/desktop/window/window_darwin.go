//go:build darwin && cgo

package window

import (
	"context"
	"fmt"

	webview "github.com/webview/webview_go"
)

func run(ctx context.Context, options Options) error {
	view := webview.New(options.Debug)
	if view == nil || view.Window() == nil {
		return fmt.Errorf("%w: cannot create macOS WebKit window", ErrUnavailable)
	}
	defer view.Destroy()

	installSystemEditShortcuts()
	view.SetTitle(options.Title)
	view.SetSize(options.Width, options.Height, webview.HintNone)
	view.Navigate(options.URL)

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			view.Terminate()
		case <-finished:
		}
	}()

	view.Run()
	return nil
}
