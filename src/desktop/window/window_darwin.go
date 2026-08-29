//go:build darwin && cgo

package window

import (
	"context"
	"fmt"
	"sync"
	"time"

	webview "github.com/webview/webview_go"
)

const nativeStartupReadyTimeout = 20 * time.Second

func run(ctx context.Context, options Options) error {
	view := webview.New(options.Debug)
	if view == nil || view.Window() == nil {
		return fmt.Errorf("%w: cannot create macOS WebKit window", ErrUnavailable)
	}
	defer view.Destroy()
	installWindowLifecycle(view.Window())
	defer uninstallWindowLifecycle()

	installSystemEditShortcuts()
	view.SetTitle(options.Title)
	view.SetSize(options.Width, options.Height, webview.HintNone)

	var ready sync.Once
	notifyReady := func() {
		ready.Do(func() {
			if options.OnReady != nil {
				options.OnReady()
			}
		})
	}
	if options.OnReady != nil {
		if err := view.Bind("nrInternStartupReady", func() string {
			notifyReady()
			return ""
		}); err != nil {
			return fmt.Errorf("bind startup ready callback: %w", err)
		}
	}
	if err := view.Bind("nrInternSetConversationActive", func(active bool) string {
		view.Dispatch(func() {
			setConversationActive(active)
		})
		return ""
	}); err != nil {
		return fmt.Errorf("bind conversation activity callback: %w", err)
	}
	readyTimer := time.AfterFunc(nativeStartupReadyTimeout, notifyReady)
	defer readyTimer.Stop()

	view.Navigate(options.URL)
	activateApplication()

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
