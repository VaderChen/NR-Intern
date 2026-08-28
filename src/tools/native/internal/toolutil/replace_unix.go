//go:build !windows

package toolutil

import "os"

func replacePath(source, target string) error {
	return os.Rename(source, target)
}
