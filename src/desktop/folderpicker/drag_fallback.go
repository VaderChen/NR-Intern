//go:build !darwin || !cgo

package folderpicker

import "context"

func dropped(_ context.Context) ([]string, error) {
	return nil, ErrUnavailable
}
