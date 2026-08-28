package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type DirectoryListTool struct {
	MaxOutputBytes int
	MaxEntries     int
}

type DirectoryCreateTool struct{}

type directoryEntry struct {
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
}

func NewDirectoryListTool(maxOutputBytes, maxEntries int) *DirectoryListTool {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 512 * 1024
	}
	if maxEntries <= 0 {
		maxEntries = 10_000
	}
	return &DirectoryListTool{MaxOutputBytes: maxOutputBytes, MaxEntries: maxEntries}
}

func NewDirectoryCreateTool() *DirectoryCreateTool { return &DirectoryCreateTool{} }

func (t *DirectoryListTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "directory_list",
		Label:        "列出目錄",
		Version:      "1.0.0",
		Category:     "files",
		Description:  "列出 Project／Session Sandbox 內的目錄內容。大型範圍先用非遞迴或 max_depth 1–2、max_entries 200 以內做淺層盤點，再用搜尋縮小範圍；不追蹤目錄 symlink。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"directory", "recursive", "metadata", "workspace-sandbox", "bounded-output"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "default": "."},
				"recursive":   map[string]any{"type": "boolean", "default": false},
				"max_depth":   map[string]any{"type": "integer", "minimum": 1, "maximum": 64, "default": 2},
				"max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": t.MaxEntries, "default": 200},
			},
		},
	}
}

func (t *DirectoryListTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	sandboxRoots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(sandboxRoots, toolutil.String(invocation.Call.Arguments, "path"), true)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fileFailure(invocation.Call, "path is not a directory"), nil
	}
	recursive := toolutil.Bool(invocation.Call.Arguments, "recursive", false)
	maxDepth := toolutil.Int(invocation.Call.Arguments, "max_depth", 2, 1, 64)
	limit := toolutil.Int(invocation.Call.Arguments, "max_entries", 200, 1, t.MaxEntries)
	values := make([]directoryEntry, 0, min(limit, 200))
	truncated := false
	var visit func(string, int) error
	visit = func(directory string, depth int) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(values) >= limit {
				truncated = true
				return nil
			}
			entryPath := filepath.Join(directory, entry.Name())
			entryInfo, err := entry.Info()
			if err != nil {
				return err
			}
			entryType := "file"
			switch {
			case entry.Type()&os.ModeSymlink != 0:
				entryType = "symlink"
			case entryInfo.IsDir():
				entryType = "directory"
			case !entryInfo.Mode().IsRegular():
				entryType = "other"
			}
			values = append(values, directoryEntry{
				Path:       toolutil.DisplayPathInRoots(sandboxRoots, entryPath),
				Type:       entryType,
				Size:       entryInfo.Size(),
				ModifiedAt: entryInfo.ModTime().UTC(),
			})
			if recursive && entryInfo.IsDir() && entry.Type()&os.ModeSymlink == 0 && depth < maxDepth {
				if err := visit(entryPath, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(path, 1); err != nil {
		if ctx.Err() != nil {
			return domain.ToolExecution{}, ctx.Err()
		}
		return fileFailure(invocation.Call, err.Error()), nil
	}
	payload := map[string]any{"path": toolutil.DisplayPathInRoots(sandboxRoots, path), "entries": values, "count": len(values), "truncated": truncated}
	if truncated {
		payload["next_step"] = "結果已達項目上限；請縮小 path/max_depth，或改用 file_search 定位相關檔案，不要逐檔展開整個目錄。"
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return domain.ToolExecution{}, err
	}
	for len(data) > t.MaxOutputBytes && len(values) > 0 {
		truncated = true
		values = values[:len(values)-1]
		payload["entries"] = values
		payload["count"] = len(values)
		payload["truncated"] = true
		payload["next_step"] = "結果已達輸出上限；請縮小 path/max_depth，或改用 file_search 定位相關檔案，不要逐檔展開整個目錄。"
		data, err = json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return domain.ToolExecution{}, err
		}
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: string(data), Details: map[string]any{"count": len(values), "truncated": truncated}}, nil
}

func (t *DirectoryCreateTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "directory_create",
		Label:              "建立目錄",
		Version:            "1.0.0",
		Category:           "files",
		Description:        "在 Project／Session Sandbox 內建立目錄，可一併建立缺少的父目錄。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"directory", "create", "workspace-sandbox"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"parents": map[string]any{"type": "boolean", "default": true},
			},
			"required": []string{"path"},
		},
	}
}

func (t *DirectoryCreateTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	requested := toolutil.String(invocation.Call.Arguments, "path")
	if requested == "" {
		return fileFailure(invocation.Call, "path is required"), nil
	}
	sandboxRoots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(sandboxRoots, requested, false)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	if toolutil.Bool(invocation.Call.Arguments, "parents", true) {
		err = os.MkdirAll(path, 0o750)
	} else {
		err = os.Mkdir(path, 0o750)
	}
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: fmt.Sprintf("created directory %s", toolutil.DisplayPathInRoots(sandboxRoots, path))}, nil
}
