//go:build !windows

package filestore

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
