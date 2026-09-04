package bootstrap

import (
	"AgenticService/src/internal/systeminfo"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// RAMDiskPool 以 Project ID 管理獨立磁碟。不能共用單一 RAM Disk，否則不同專案
// 雖然在介面上分開，實際仍能透過同一根目錄看到彼此的工作檔案。
type RAMDiskPool struct {
	config RAMDiskConfig
	logger *slog.Logger
	mu     sync.Mutex
	disks  map[string]*RAMDisk
}

func (pool *RAMDiskPool) StageFile(ctx context.Context, projectID, sourcePath, fileName string) (string, error) {
	if pool == nil {
		return "", fmt.Errorf("RAM disk support is unavailable")
	}
	projectID = strings.TrimSpace(projectID)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	disk := pool.disks[projectID]
	if disk == nil {
		return "", fmt.Errorf("RAM disk for project %q is not prepared", projectID)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	source, err := os.Open(filepath.Clean(sourcePath))
	if err != nil {
		return "", fmt.Errorf("open staged file: %w", err)
	}
	defer source.Close()
	directory := filepath.Join(disk.Root(), ".nr-intern-attachments")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create staged attachment directory: %w", err)
	}
	name := filepath.Base(strings.TrimSpace(fileName))
	if name == "." || name == "" {
		return "", fmt.Errorf("staged file name is required")
	}
	destinationPath := filepath.Join(directory, name)
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("create staged file: %w", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		_ = os.Remove(destinationPath)
		return "", fmt.Errorf("stage file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(destinationPath)
		return "", fmt.Errorf("close staged file: %w", closeErr)
	}
	return destinationPath, nil
}

func NewRAMDiskPool(config RAMDiskConfig, logger *slog.Logger) *RAMDiskPool {
	if logger == nil {
		logger = slog.Default()
	}
	return &RAMDiskPool{config: config, logger: logger, disks: map[string]*RAMDisk{}}
}

func (pool *RAMDiskPool) Prepare(ctx context.Context, projectID string, sizeMB int) (string, error) {
	if pool == nil || !pool.config.Enabled {
		return "", fmt.Errorf("RAM disk support is disabled")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("project id is required")
	}
	maximumSizeMB := maxRAMDiskSizeMB
	if totalBytes, err := systeminfo.TotalMemoryBytes(); err == nil && totalBytes > 0 {
		maximumSizeMB = int((totalBytes * 3 / 4) / (1024 * 1024))
	}
	if sizeMB < minRAMDiskSizeMB || sizeMB > maximumSizeMB {
		return "", fmt.Errorf("RAM disk size must be between %d and %d MB", minRAMDiskSizeMB, maximumSizeMB)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if disk := pool.disks[projectID]; disk != nil {
		return disk.Root(), nil
	}
	disk, err := newRAMDisk(ctx, RAMDiskConfig{Enabled: true, SizeMB: sizeMB}, projectID, pool.logger.With("project_id", projectID))
	if err != nil {
		return "", err
	}
	pool.disks[projectID] = disk
	return disk.Root(), nil
}

func (pool *RAMDiskPool) Release(ctx context.Context, projectID string) error {
	if pool == nil {
		return nil
	}
	projectID = strings.TrimSpace(projectID)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	disk := pool.disks[projectID]
	if disk == nil {
		return nil
	}
	if err := disk.Close(ctx); err != nil {
		return err
	}
	delete(pool.disks, projectID)
	return nil
}

func (pool *RAMDiskPool) Close(ctx context.Context) error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	ids := make([]string, 0, len(pool.disks))
	for id := range pool.disks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result error
	for _, id := range ids {
		if err := pool.disks[id].Close(ctx); err != nil {
			if result == nil {
				result = err
			}
			continue
		}
		delete(pool.disks, id)
	}
	return result
}
