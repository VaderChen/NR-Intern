//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const linuxRAMDiskKind = "linux-tmpfs"

func newPlatformRAMDisk(ctx context.Context, sizeBytes int64, ownerID string, logger *slog.Logger) (*platformRAMDisk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	const root = "/dev/shm"
	var stats unix.Statfs_t
	if err := unix.Statfs(root, &stats); err != nil {
		return nil, fmt.Errorf("inspect Linux shared memory filesystem: %w", err)
	}
	if uint64(stats.Type) != uint64(unix.TMPFS_MAGIC) {
		return nil, fmt.Errorf("%s is not a tmpfs filesystem", root)
	}
	// 獨立掛載不消耗父 tmpfs 的容量配額，不能再以 /dev/shm 的剩餘量限制它。
	// 總配置額度由 Pool 控制；實際記憶體壓力仍由核心在寫入時處理。
	if err := cleanupLinuxRAMDisks(root, logger); err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(root, strings.ToLower(ramDiskNamePrefix))
	if err != nil {
		return nil, fmt.Errorf("create Linux RAM disk mount point: %w", err)
	}
	marker := ramDiskMarker{
		Magic: ramDiskMarkerMagic, Kind: linuxRAMDiskKind, Name: filepath.Base(path),
		OwnerID: ownerID, PID: os.Getpid(), SizeBytes: sizeBytes, CreatedAt: time.Now().UTC(),
	}
	// 父目錄也在 tmpfs；另留底層標記，卸載成功但目錄清理失敗時才能安全重試。
	if err := writeRAMDiskMarker(path, marker); err != nil {
		_ = os.RemoveAll(path)
		return nil, err
	}
	// 共享 tmpfs 的子目錄沒有配額；獨立掛載才會由核心強制執行 size 上限。
	options := fmt.Sprintf("size=%d,mode=0700", sizeBytes)
	if err := unix.Mount("tmpfs", path, "tmpfs", unix.MS_NODEV|unix.MS_NOSUID, options); err != nil {
		_ = os.RemoveAll(path)
		return nil, fmt.Errorf("mount size-limited Linux RAM disk (requires CAP_SYS_ADMIN in the mount namespace): %w", err)
	}
	if err := writeRAMDiskMarker(path, marker); err != nil {
		if unmountErr := unix.Unmount(path, 0); unmountErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback Linux RAM disk %s: %w", path, unmountErr))
		}
		return nil, errors.Join(err, os.RemoveAll(path))
	}
	return &platformRAMDisk{
		root: path, mode: linuxRAMDiskKind, volatile: true,
		close: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return removeLinuxRAMDisk(root, path)
		},
	}, nil
}

func cleanupLinuxRAMDisks(base string, logger *slog.Logger) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("scan Linux RAM disks: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		if !entry.IsDir() || !isManagedRAMDiskPath(base, path) {
			continue
		}
		marker, err := readRAMDiskMarker(path)
		if err != nil || !validRAMDiskMarker(marker, entry.Name(), linuxRAMDiskKind) {
			continue
		}
		if marker.PID > 0 && ramDiskProcessAlive(marker.PID) {
			continue
		}
		if err := removeLinuxRAMDisk(base, path); err != nil {
			logger.Warn("failed to remove stale Linux RAM disk", "path", path, "error", err)
		}
	}
	return nil
}

func removeLinuxRAMDisk(base, path string) error {
	if !isManagedRAMDiskPath(base, path) {
		return fmt.Errorf("refuse to remove unmanaged Linux RAM disk")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove non-directory Linux RAM disk")
	}
	marker, err := readRAMDiskMarker(path)
	if err != nil || !validRAMDiskMarker(marker, filepath.Base(path), linuxRAMDiskKind) {
		return fmt.Errorf("refuse to remove Linux RAM disk without ownership marker")
	}
	// EINVAL 表示舊版的未掛載子目錄；EBUSY 等錯誤不能以遞迴刪除繞過。
	if err := unix.Unmount(path, 0); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("unmount Linux RAM disk: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		// RemoveAll 可能先移除標記再失敗；重建標記只用在已驗證過的同一個真實目錄。
		if current, statErr := os.Lstat(path); statErr == nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 {
			if _, markerErr := os.Lstat(filepath.Join(path, ramDiskMarkerName)); errors.Is(markerErr, os.ErrNotExist) {
				_ = writeRAMDiskMarker(path, marker)
			}
		}
		return fmt.Errorf("remove Linux RAM disk mount point: %w", err)
	}
	return nil
}
