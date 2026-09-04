//go:build !darwin && !linux && !windows

package systeminfo

import "fmt"

func TotalMemoryBytes() (uint64, error) {
	return 0, fmt.Errorf("system memory discovery is unavailable on this platform")
}
