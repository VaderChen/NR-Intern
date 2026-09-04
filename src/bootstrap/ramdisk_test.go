package bootstrap

import (
	"AgenticService/src/internal/systeminfo"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedRAMDiskDirectoryLifecycle(t *testing.T) {
	base := t.TempDir()
	value, err := newManagedRAMDiskDirectory(base, "test-temporary", "project_test", 64*1024*1024, slog.Default())
	if err != nil {
		t.Fatalf("newManagedRAMDiskDirectory: %v", err)
	}
	if !isManagedRAMDiskPath(base, value.root) {
		t.Fatalf("root %q is outside %q", value.root, base)
	}
	marker, err := readRAMDiskMarker(value.root)
	if err != nil {
		t.Fatalf("readRAMDiskMarker: %v", err)
	}
	if !validRAMDiskMarker(marker, filepath.Base(value.root), "test-temporary") || marker.PID != os.Getpid() {
		t.Fatalf("marker = %+v", marker)
	}
	if err := value.close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(value.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace still exists after close: %v", err)
	}
}

func TestCleanupManagedRAMDiskDirectoriesOnlyRemovesOwnedStaleEntries(t *testing.T) {
	base := t.TempDir()
	stale := filepath.Join(base, ramDiskNamePrefix+"stale")
	active := filepath.Join(base, ramDiskNamePrefix+"active")
	unowned := filepath.Join(base, ramDiskNamePrefix+"unowned")
	for _, root := range []string{stale, active, unowned} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatalf("Mkdir(%s): %v", root, err)
		}
	}
	if err := writeRAMDiskMarker(stale, ramDiskMarker{
		Magic: ramDiskMarkerMagic, Kind: "test", Name: filepath.Base(stale), PID: 2_147_483_647, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write stale marker: %v", err)
	}
	if err := writeRAMDiskMarker(active, ramDiskMarker{
		Magic: ramDiskMarkerMagic, Kind: "test", Name: filepath.Base(active), PID: os.Getpid(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write active marker: %v", err)
	}
	if err := cleanupManagedRAMDiskDirectories(base, "test", slog.Default()); err != nil {
		t.Fatalf("cleanupManagedRAMDiskDirectories: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale workspace was not removed: %v", err)
	}
	for _, root := range []string{active, unowned} {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("protected workspace %q was removed: %v", root, err)
		}
	}
}

func TestRAMDiskCloseCanRetryAndIsIdempotent(t *testing.T) {
	attempts := 0
	disk := &RAMDisk{closeFn: func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("busy")
		}
		return nil
	}}
	if err := disk.Close(context.Background()); err == nil {
		t.Fatal("first close unexpectedly succeeded")
	}
	if err := disk.Close(context.Background()); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if err := disk.Close(context.Background()); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("close attempts = %d, want 2", attempts)
	}
}

func TestDisabledRAMDiskDoesNotCreateWorkspace(t *testing.T) {
	disk, err := newRAMDisk(context.Background(), RAMDiskConfig{Enabled: false, SizeMB: DefaultRAMDiskSizeMB}, "project_test", slog.Default())
	if err != nil {
		t.Fatalf("newRAMDisk: %v", err)
	}
	if disk != nil {
		t.Fatalf("disabled RAM disk = %+v", disk)
	}
}

func TestRAMDiskPoolStagesFileInsideProjectDisk(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "來源.txt")
	if err := os.WriteFile(source, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	pool := NewRAMDiskPool(RAMDiskConfig{Enabled: true, SizeMB: DefaultRAMDiskSizeMB}, slog.Default())
	pool.disks["project_1"] = &RAMDisk{root: root}
	staged, err := pool.StageFile(context.Background(), "project_1", source, "attachment_1.txt")
	if err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	// 附件必須落在沙箱內，Agent 才讀得到；同時不能落在後端資料區，
	// 否則 Agent 就能看到自己的 transcript 與計畫。
	disk := pool.disks["project_1"]
	if !strings.HasPrefix(staged, disk.WorkspaceRoot()+string(filepath.Separator)) {
		t.Fatalf("staged path %q 不在沙箱 %q 內", staged, disk.WorkspaceRoot())
	}
	if strings.HasPrefix(staged, disk.StoreRoot()+string(filepath.Separator)) {
		t.Fatalf("staged path %q 落在後端資料區", staged)
	}
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "content" {
		t.Fatalf("staged content = %q", content)
	}
}

func TestRAMDiskPoolRejectsCapacityOverThreeQuartersOfSystemMemory(t *testing.T) {
	totalBytes, err := systeminfo.TotalMemoryBytes()
	if err != nil {
		t.Fatalf("TotalMemoryBytes: %v", err)
	}
	maximumMB := int((totalBytes * 3 / 4) / (1024 * 1024))
	pool := NewRAMDiskPool(RAMDiskConfig{Enabled: true, SizeMB: DefaultRAMDiskSizeMB}, slog.Default())
	if _, err := pool.Prepare(context.Background(), "project_too_large", maximumMB+1); err == nil {
		t.Fatal("Prepare unexpectedly accepted more than 75% of physical memory")
	}
}
