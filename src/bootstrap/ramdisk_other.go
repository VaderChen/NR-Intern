//go:build !darwin && !linux && !windows

package bootstrap

import (
	"context"
	"log/slog"
	"os"
)

const otherRAMDiskKind = "temporary-directory"

func newPlatformRAMDisk(_ context.Context, sizeBytes int64, ownerID string, logger *slog.Logger) (*platformRAMDisk, error) {
	return newManagedRAMDiskDirectory(os.TempDir(), otherRAMDiskKind, ownerID, sizeBytes, logger)
}
