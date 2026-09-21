package bootstrap

import (
	"AgenticService/src/domain"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var backupDirectories = []string{
	"sessions", "projects", "workspaces", "runs", "events", "plans", "attachments", "memories", "schedules", "notifications",
}

var backupExcludedFiles = []string{
	"providers.json", "provider-oauth-tokens.json", "mcp-servers.json", "netpass.json",
}

const maxRestoreExpandedBytes uint64 = 512 * 1024 * 1024
const maxRestoreEntries = 100_000

type backupManifest struct {
	Version       int      `json:"version"`
	CreatedAt     string   `json:"created_at"`
	Included      []string `json:"included"`
	Excluded      []string `json:"excluded"`
	RestartNeeded bool     `json:"restart_required_after_restore"`
}

func (r *Runtime) DiagnosticsExport(ctx context.Context) ([]byte, error) {
	value, err := r.Diagnostics(ctx)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if config, ok := snapshot["config"].(map[string]any); ok {
		delete(config, "data_dir")
		delete(config, "api_token")
		delete(config, "providers_credentials")
	}
	result := map[string]any{
		"format":      "nr-intern-diagnostics",
		"version":     1,
		"exported_at": time.Now().UTC(),
		"diagnostics": snapshot,
	}
	return json.MarshalIndent(result, "", "  ")
}

func (r *Runtime) Backup(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.configMu.RLock()
	dataDir := r.Config.DataDir
	r.configMu.RUnlock()
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: data directory is unavailable", domain.ErrConflict)
	}
	if err := ensureSafeParents(dataDir, "entry"); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	defer archive.Close()
	budget := backupCopyBudget{ctx: ctx, remaining: int64(maxRestoreExpandedBytes)}
	manifest := backupManifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Excluded: append([]string(nil), backupExcludedFiles...), RestartNeeded: true}
	for _, name := range backupDirectories {
		root := filepath.Join(dataDir, name)
		if info, err := os.Lstat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect backup directory %q: %w", name, err)
		} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("backup root %q must be a real directory", name)
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			nameInArchive := filepath.ToSlash(rel)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("backup entry %q is not a regular file", nameInArchive)
			}
			if len(manifest.Included) >= maxRestoreEntries-1 {
				return fmt.Errorf("%w: 備份項目總數超過還原上限（%d 項），已停止備份", domain.ErrConflict, maxRestoreEntries)
			}
			if !info.IsDir() && info.Size() > budget.remaining {
				return backupSizeLimitError()
			}
			manifest.Included = append(manifest.Included, nameInArchive)
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = nameInArchive
			if entry.IsDir() {
				header.Name += "/"
			} else {
				header.Method = zip.Deflate
			}
			writer, err := archive.CreateHeader(header)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			return budget.copyFile(writer, path, info)
		}); err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("create backup: %w", err)
		}
	}
	// service-settings.json 不含密鑰，但仍列入 manifest，方便還原時知道設定來源。
	settingsPath := filepath.Join(dataDir, "service-settings.json")
	if info, err := os.Lstat(settingsPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("service settings is not a regular file")
	} else if err == nil {
		if len(manifest.Included) >= maxRestoreEntries-1 {
			return nil, fmt.Errorf("%w: 備份項目總數超過還原上限（%d 項），已停止備份", domain.ErrConflict, maxRestoreEntries)
		}
		if info.Size() > budget.remaining {
			return nil, backupSizeLimitError()
		}
		manifest.Included = append(manifest.Included, "service-settings.json")
		header := &zip.FileHeader{Name: "service-settings.json", Method: zip.Deflate}
		header.SetMode(0o600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if err := budget.copyFile(writer, settingsPath, info); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = archive.Close()
		return nil, err
	}
	header := &zip.FileHeader{Name: "manifest.json", Method: zip.Deflate}
	header.SetMode(0o600)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		_ = archive.Close()
		return nil, err
	}
	if _, err := writer.Write(manifestData); err != nil {
		_ = archive.Close()
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// extractRestoreArchive 僅能寫入新建的 staging 目錄，不能直接覆寫運行中的資料。
func extractRestoreArchive(ctx context.Context, data []byte, dataDir string) (domain.RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.RestoreResult{}, err
	}
	if len(data) == 0 {
		return domain.RestoreResult{}, fmt.Errorf("%w: backup is empty", domain.ErrInvalidInput)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return domain.RestoreResult{}, fmt.Errorf("%w: invalid backup archive: %v", domain.ErrInvalidInput, err)
	}
	allowed := map[string]bool{"service-settings.json": true}
	for _, name := range backupDirectories {
		allowed[name] = true
	}
	result := domain.RestoreResult{RestartRequired: true, Excluded: append([]string(nil), backupExcludedFiles...)}
	var expandedBytes uint64
	seen := map[string]bool{}
	if len(reader.File) > maxRestoreEntries {
		return domain.RestoreResult{}, fmt.Errorf("%w: too many archive entries", domain.ErrInvalidInput)
	}
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return domain.RestoreResult{}, err
		}
		name, err := safeArchiveName(file.Name)
		if err != nil {
			return domain.RestoreResult{}, err
		}
		if name == "manifest.json" {
			continue
		}
		if seen[name] {
			return domain.RestoreResult{}, fmt.Errorf("%w: duplicate archive entry %q", domain.ErrInvalidInput, name)
		}
		seen[name] = true
		top := strings.SplitN(name, "/", 2)[0]
		if !allowed[top] {
			return domain.RestoreResult{}, fmt.Errorf("%w: archive entry %q is not restorable", domain.ErrInvalidInput, name)
		}
		if (top == serviceSettingsFilename && (name != top || file.FileInfo().IsDir())) ||
			(top != serviceSettingsFilename && name == top && !file.FileInfo().IsDir()) {
			return domain.RestoreResult{}, fmt.Errorf("%w: invalid archive root type %q", domain.ErrInvalidInput, name)
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return domain.RestoreResult{}, fmt.Errorf("%w: symlink entry %q is not allowed", domain.ErrInvalidInput, name)
		}
		if file.UncompressedSize64 > maxRestoreExpandedBytes || expandedBytes > maxRestoreExpandedBytes-file.UncompressedSize64 {
			return domain.RestoreResult{}, fmt.Errorf("%w: expanded backup exceeds %d bytes", domain.ErrInvalidInput, maxRestoreExpandedBytes)
		}
		expandedBytes += file.UncompressedSize64
		target, err := safeDataTarget(dataDir, name)
		if err != nil {
			return domain.RestoreResult{}, err
		}
		if strings.HasSuffix(name, "/") || file.FileInfo().IsDir() {
			if err := ensureSafeParents(dataDir, name); err != nil {
				return domain.RestoreResult{}, err
			}
			if err := ensureSafeTarget(target, true); err != nil {
				return domain.RestoreResult{}, err
			}
			if err := os.MkdirAll(target, 0o700); err != nil {
				return domain.RestoreResult{}, err
			}
			result.Restored = append(result.Restored, name)
			continue
		}
		if err := ensureSafeParents(dataDir, name); err != nil {
			return domain.RestoreResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return domain.RestoreResult{}, err
		}
		if err := ensureSafeTarget(target, false); err != nil {
			return domain.RestoreResult{}, err
		}
		source, err := file.Open()
		if err != nil {
			return domain.RestoreResult{}, err
		}
		destination, err := os.CreateTemp(filepath.Dir(target), ".restore-file-")
		if err != nil {
			_ = source.Close()
			return domain.RestoreResult{}, err
		}
		tmp := destination.Name()
		if err == nil {
			var copied int64
			copied, err = io.Copy(destination, io.LimitReader(source, int64(file.UncompressedSize64)+1))
			if err == nil && uint64(copied) != file.UncompressedSize64 {
				err = fmt.Errorf("archive entry size mismatch")
			}
			if err == nil {
				err = destination.Sync()
			}
			closeErr := destination.Close()
			if err == nil {
				err = closeErr
			}
		}
		_ = source.Close()
		if err != nil {
			_ = os.Remove(tmp)
			return domain.RestoreResult{}, fmt.Errorf("restore %q: %w", name, err)
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return domain.RestoreResult{}, fmt.Errorf("restore %q: %w", name, err)
		}
		result.Restored = append(result.Restored, name)
	}
	return result, nil
}

func safeArchiveName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: unsafe archive path", domain.ErrInvalidInput)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: unsafe archive path %q", domain.ErrInvalidInput, name)
		}
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%w: unsafe archive path %q", domain.ErrInvalidInput, name)
	}
	return clean, nil
}

func safeDataTarget(dataDir, name string) (string, error) {
	clean, err := safeArchiveName(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dataDir, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dataDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: archive path escapes data directory", domain.ErrInvalidInput)
	}
	return target, nil
}

func ensureSafeParents(dataDir, name string) error {
	if info, err := os.Lstat(dataDir); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: data directory symlink is not allowed", domain.ErrInvalidInput)
	}
	parts := strings.Split(filepath.FromSlash(name), string(os.PathSeparator))
	current := dataDir
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink parent %q is not allowed", domain.ErrInvalidInput, part)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func ensureSafeTarget(target string, directory bool) error {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlink target %q is not allowed", domain.ErrInvalidInput, filepath.Base(target))
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("%w: restore directory conflicts with file %q", domain.ErrInvalidInput, filepath.Base(target))
	}
	if !directory && info.IsDir() {
		return fmt.Errorf("%w: restore file conflicts with directory %q", domain.ErrInvalidInput, filepath.Base(target))
	}
	return nil
}
