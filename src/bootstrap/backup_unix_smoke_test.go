//go:build !windows

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBackupRejectsSpecialFileBeforeOpeningSmoke(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "sessions", "blocked.pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := (&Runtime{Config: Config{DataDir: root}}).Backup(context.Background()); err == nil || data != nil {
		t.Fatal("special file accepted")
	}
}
