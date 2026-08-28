//go:build !darwin

package folderpicker

import "context"

func pick(_ context.Context) ([]string, error) {
	return nil, ErrUnavailable
}
