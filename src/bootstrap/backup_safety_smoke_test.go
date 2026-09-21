package bootstrap

import (
	"AgenticService/src/domain"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCancellationAndBudgetSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime := &Runtime{Config: Config{DataDir: t.TempDir()}}
	if data, err := runtime.Backup(ctx); !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("canceled backup: bytes=%d err=%v", len(data), err)
	}
	var dst bytes.Buffer
	budget := backupCopyBudget{ctx: context.Background(), remaining: 3}
	w := backupBudgetWriter{dst: &dst, budget: &budget}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("write: %d %v", n, err)
	}
	if n, err := w.Write([]byte("d")); n != 0 || !errors.Is(err, domain.ErrConflict) {
		t.Fatal("growing source exceeded byte budget")
	}
	budget.ctx = ctx
	budget.remaining = 100
	if _, err := w.Write([]byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatal("copy ignored cancellation")
	}
}

func TestBackupRejectsSettingsSymlinkSmoke(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "synthetic.txt")
	if err := os.WriteFile(external, []byte("synthetic-private-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, serviceSettingsFilename)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data, err := (&Runtime{Config: Config{DataDir: root}}).Backup(context.Background())
	if err == nil || data != nil {
		t.Fatal("backup followed service settings symlink")
	}
}

func TestBackupRoundTripIncludesEmptyRootsSmoke(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, serviceSettingsFilename), []byte(`{"service_name":"Smoke"}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := (&Runtime{Config: Config{DataDir: root}}).Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"sessions/": false, serviceSettingsFilename: false, "manifest.json": false}
	for _, entry := range reader.File {
		if _, ok := wanted[entry.Name]; !ok {
			t.Fatalf("unexpected entry %s", entry.Name)
		}
		wanted[entry.Name] = true
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("missing %s", name)
		}
	}
	runtime := &Runtime{Config: Config{DataDir: t.TempDir()}}
	if _, err := runtime.Restore(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if applied, err := applyPendingRestore(runtime.Config.DataDir); err != nil || !applied {
		t.Fatalf("apply=%v err=%v", applied, err)
	}
}
