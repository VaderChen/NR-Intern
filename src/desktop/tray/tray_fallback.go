//go:build !windows

package tray

import (
	"context"
	"fmt"
	"runtime"
)

func run(_ context.Context, _ Options) error {
	return fmt.Errorf("%w: %s platform adapter is not included", ErrUnavailable, runtime.GOOS)
}
