//go:build !darwin && !windows

package screencapture

import "context"

func capture(_ context.Context) (Result, error) {
	return Result{}, ErrUnavailable
}

func copyPNGToClipboard(_ context.Context, _ []byte) error {
	return ErrUnavailable
}
