//go:build !windows

package bootstrap

import "os"

func replaceConfigFile(source, target string) error {
	return os.Rename(source, target)
}
