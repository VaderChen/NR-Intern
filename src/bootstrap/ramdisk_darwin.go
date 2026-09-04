//go:build darwin

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const darwinRAMDiskKind = "darwin-hfs"

var darwinRAMDiskDevicePattern = regexp.MustCompile(`^/dev/disk[0-9]+$`)

func newPlatformRAMDisk(ctx context.Context, sizeBytes int64, ownerID string, logger *slog.Logger) (*platformRAMDisk, error) {
	cleanupStaleDarwinRAMDisks(ctx, logger)
	suffix, err := newRAMDiskSuffix()
	if err != nil {
		return nil, err
	}
	volumeName := fmt.Sprintf("%s%d-%s", ramDiskNamePrefix, os.Getpid(), suffix)
	blocks := sizeBytes / 512
	output, err := runRAMDiskCommand(ctx, "/usr/bin/hdiutil", "attach", "-nomount", fmt.Sprintf("ram://%d", blocks))
	if err != nil {
		return nil, fmt.Errorf("attach macOS RAM disk: %w", err)
	}
	device := ""
	for _, field := range strings.Fields(string(output)) {
		if darwinRAMDiskDevicePattern.MatchString(field) {
			device = field
			break
		}
	}
	if device == "" {
		return nil, fmt.Errorf("attach macOS RAM disk returned no device")
	}
	detachOnError := true
	defer func() {
		if detachOnError {
			_, _ = runRAMDiskCommand(context.Background(), "/usr/bin/hdiutil", "detach", "-force", device)
		}
	}()
	if _, err := runRAMDiskCommand(ctx, "/usr/sbin/diskutil", "eraseVolume", "HFS+", volumeName, device); err != nil {
		return nil, fmt.Errorf("format macOS RAM disk: %w", err)
	}
	root := filepath.Join("/Volumes", volumeName)
	if err := waitForRAMDiskMount(ctx, root); err != nil {
		return nil, err
	}
	marker := ramDiskMarker{
		Magic:     ramDiskMarkerMagic,
		Kind:      darwinRAMDiskKind,
		Name:      volumeName,
		OwnerID:   ownerID,
		Device:    device,
		PID:       os.Getpid(),
		SizeBytes: sizeBytes,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeRAMDiskMarker(root, marker); err != nil {
		return nil, err
	}
	detachOnError = false
	return &platformRAMDisk{
		root:     root,
		mode:     darwinRAMDiskKind,
		volatile: true,
		close: func(closeContext context.Context) error {
			return detachDarwinRAMDisk(closeContext, device)
		},
	}, nil
}

func cleanupStaleDarwinRAMDisks(ctx context.Context, logger *slog.Logger) {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		logger.Warn("failed to scan stale macOS RAM disks", "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ramDiskNamePrefix) {
			continue
		}
		root := filepath.Join("/Volumes", entry.Name())
		marker, err := readRAMDiskMarker(root)
		if err != nil || !validRAMDiskMarker(marker, entry.Name(), darwinRAMDiskKind) || !darwinRAMDiskDevicePattern.MatchString(marker.Device) {
			continue
		}
		if marker.PID > 0 && ramDiskProcessAlive(marker.PID) {
			continue
		}
		if err := detachDarwinRAMDisk(ctx, marker.Device); err != nil {
			logger.Warn("failed to detach stale macOS RAM disk", "device", marker.Device, "error", err)
		}
	}
}

func detachDarwinRAMDisk(ctx context.Context, device string) error {
	if !darwinRAMDiskDevicePattern.MatchString(device) {
		return fmt.Errorf("refuse to detach invalid RAM disk device %q", device)
	}
	if _, err := runRAMDiskCommand(ctx, "/usr/bin/hdiutil", "detach", device); err == nil {
		return nil
	}
	if _, err := runRAMDiskCommand(ctx, "/usr/bin/hdiutil", "detach", "-force", device); err != nil {
		return fmt.Errorf("force detach macOS RAM disk %s: %w", device, err)
	}
	return nil
}

func waitForRAMDiskMount(ctx context.Context, root string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("macOS RAM disk was not mounted at %s", root)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
