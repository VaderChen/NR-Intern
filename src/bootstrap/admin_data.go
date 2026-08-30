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
	r.configMu.RLock()
	dataDir := r.Config.DataDir
	r.configMu.RUnlock()
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: data directory is unavailable", domain.ErrConflict)
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	manifest := backupManifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Excluded: append([]string(nil), backupExcludedFiles...), RestartNeeded: true}
	for _, name := range backupDirectories {
		root := filepath.Join(dataDir, name)
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect backup directory %q: %w", name, err)
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
			manifest.Included = append(manifest.Included, nameInArchive)
			info, err := entry.Info()
			if err != nil {
				return err
			}
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
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
			return err
		}); err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("create backup: %w", err)
		}
	}
	// service-settings.json 不含密鑰，但仍列入 manifest，方便還原時知道設定來源。
	settingsPath := filepath.Join(dataDir, "service-settings.json")
	if info, err := os.Stat(settingsPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("service settings is not a regular file")
	} else if err == nil {
		manifest.Included = append(manifest.Included, "service-settings.json")
		header := &zip.FileHeader{Name: "service-settings.json", Method: zip.Deflate}
		header.SetMode(0o600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		file, err := os.Open(settingsPath)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		_, copyErr := io.Copy(writer, file)
		_ = file.Close()
		if copyErr != nil {
			_ = archive.Close()
			return nil, copyErr
		}
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
	return buffer.Bytes(), nil
}

func (r *Runtime) Restore(ctx context.Context, data []byte) (domain.RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.RestoreResult{}, err
	}
	if len(data) == 0 {
		return domain.RestoreResult{}, fmt.Errorf("%w: backup is empty", domain.ErrInvalidInput)
	}
	// 保留目前資料的可復原快照；Backup 本身不會包含 credentials。
	backup, err := r.Backup(ctx)
	if err != nil {
		return domain.RestoreResult{}, err
	}
	r.configMu.RLock()
	dataDir := r.Config.DataDir
	r.configMu.RUnlock()
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return domain.RestoreResult{}, err
	}
	backupPath := filepath.Join(backupDir, "pre-restore-"+time.Now().UTC().Format("20060102-150405")+".zip")
	if err := os.WriteFile(backupPath, backup, 0o600); err != nil {
		return domain.RestoreResult{}, fmt.Errorf("save pre-restore backup: %w", err)
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
	for _, file := range reader.File {
		name, err := safeArchiveName(file.Name)
		if err != nil {
			return domain.RestoreResult{}, err
		}
		if name == "manifest.json" {
			continue
		}
		top := strings.SplitN(name, "/", 2)[0]
		if !allowed[top] {
			return domain.RestoreResult{}, fmt.Errorf("%w: archive entry %q is not restorable", domain.ErrInvalidInput, name)
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
		tmp := target + ".restore-tmp"
		if err := ensureSafeTarget(tmp, false); err != nil {
			_ = source.Close()
			return domain.RestoreResult{}, err
		}
		destination, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = io.Copy(destination, source)
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
