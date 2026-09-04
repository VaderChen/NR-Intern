//go:build linux

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sys/unix"
)

const linuxRAMDiskKind = "linux-tmpfs"

func newPlatformRAMDisk(_ context.Context, sizeBytes int64, ownerID string, logger *slog.Logger) (*platformRAMDisk, error) {
	const root = "/dev/shm"
	var stats unix.Statfs_t
	if err := unix.Statfs(root, &stats); err != nil {
		return nil, fmt.Errorf("inspect Linux shared memory filesystem: %w", err)
	}
	if uint64(stats.Type) != uint64(unix.TMPFS_MAGIC) {
		return nil, fmt.Errorf("%s is not a tmpfs filesystem", root)
	}
	available := int64(stats.Bavail) * int64(stats.Bsize)
	if available < sizeBytes {
		return nil, fmt.Errorf("Linux shared memory has %d bytes available, need at least %d", available, sizeBytes)
	}
	return newManagedRAMDiskDirectory(root, linuxRAMDiskKind, ownerID, sizeBytes, logger)
}
