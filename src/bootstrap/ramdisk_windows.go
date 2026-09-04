//go:build windows

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

const windowsRAMDiskKind = "windows-imdisk"

func newPlatformRAMDisk(ctx context.Context, sizeBytes int64, ownerID string, logger *slog.Logger) (*platformRAMDisk, error) {
	executable, err := exec.LookPath("imdisk.exe")
	if err != nil {
		return nil, fmt.Errorf("Windows RAM disk requires ImDisk: imdisk.exe was not found in PATH")
	}
	cleanupStaleWindowsRAMDisks(ctx, executable, logger)
	drive, err := unusedWindowsDrive()
	if err != nil {
		return nil, err
	}
	if _, err := runRAMDiskCommand(ctx, executable,
		"-a", "-t", "vm", "-s", fmt.Sprintf("%dM", sizeBytes/(1024*1024)),
		"-m", drive, "-p", "/fs:ntfs /q /y /v:NRInternRAM",
	); err != nil {
		return nil, fmt.Errorf("create Windows ImDisk RAM disk: %w", err)
	}
	detachOnError := true
	defer func() {
		if detachOnError {
			_ = detachWindowsRAMDisk(context.Background(), executable, drive)
		}
	}()
	root := drive + `\`
	if err := waitForWindowsRAMDisk(ctx, root); err != nil {
		return nil, err
	}
	marker := ramDiskMarker{
		Magic:     ramDiskMarkerMagic,
		Kind:      windowsRAMDiskKind,
		Name:      drive,
		OwnerID:   ownerID,
		Device:    drive,
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
		mode:     windowsRAMDiskKind,
		volatile: true,
		close: func(closeContext context.Context) error {
			return detachWindowsRAMDisk(closeContext, executable, drive)
		},
	}, nil
}

func cleanupStaleWindowsRAMDisks(ctx context.Context, executable string, logger *slog.Logger) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		logger.Warn("failed to scan Windows drive letters for stale ImDisk volumes", "error", err)
		return
	}
	for letter := byte('D'); letter <= 'Z'; letter++ {
		if mask&(1<<uint(letter-'A')) == 0 {
			continue
		}
		drive := string([]byte{letter, ':'})
		marker, err := readRAMDiskMarker(drive + `\`)
		if err != nil || !validRAMDiskMarker(marker, drive, windowsRAMDiskKind) || marker.Device != drive {
			continue
		}
		if marker.PID > 0 && ramDiskProcessAlive(marker.PID) {
			continue
		}
		if err := detachWindowsRAMDisk(ctx, executable, drive); err != nil {
			logger.Warn("failed to detach stale Windows ImDisk volume", "drive", drive, "error", err)
		}
	}
}

func unusedWindowsDrive() (string, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return "", fmt.Errorf("list Windows drive letters: %w", err)
	}
	// 優先使用較少被實體磁碟與網路磁碟占用的尾端代號；仍以系統 bitmask 為準，
	// 不硬編碼單一代號，避免覆蓋使用者既有磁碟。
	for _, letter := range []byte("RSTUVWXYZQPONMLKJIHGFED") {
		if mask&(1<<uint(letter-'A')) == 0 {
			return string([]byte{letter, ':'}), nil
		}
	}
	return "", fmt.Errorf("no unused Windows drive letter is available for ImDisk")
}

func detachWindowsRAMDisk(ctx context.Context, executable, drive string) error {
	if len(drive) != 2 || drive[1] != ':' || drive[0] < 'D' || drive[0] > 'Z' {
		return fmt.Errorf("refuse to detach invalid Windows drive %q", drive)
	}
	if _, err := runRAMDiskCommand(ctx, executable, "-d", "-m", drive); err == nil {
		return nil
	}
	if _, err := runRAMDiskCommand(ctx, executable, "-D", "-m", drive); err != nil {
		return fmt.Errorf("force detach Windows ImDisk volume %s: %w", drive, err)
	}
	return nil
}

func waitForWindowsRAMDisk(ctx context.Context, root string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := os.Stat(filepath.Clean(root))
		if err == nil && info.IsDir() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Windows ImDisk RAM disk was not mounted at %s", root)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
