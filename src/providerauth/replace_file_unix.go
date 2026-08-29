//go:build !windows

package providerauth

import "os"

func replaceTokenFile(source, target string) error {
	return os.Rename(source, target)
}
