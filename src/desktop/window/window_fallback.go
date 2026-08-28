//go:build !darwin || !cgo

package window

import (
	"context"
	"fmt"
	"runtime"
)

func run(_ context.Context, _ Options) error {
	return fmt.Errorf("%w: %s requires a platform window adapter", ErrUnavailable, runtime.GOOS)
}
