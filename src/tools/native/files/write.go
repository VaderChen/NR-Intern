package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type WriteTool struct {
	MaxInputBytes int
}

func NewWriteTool(maxInputBytes int) *WriteTool {
	if maxInputBytes <= 0 {
		maxInputBytes = 4 * 1024 * 1024
	}
	return &WriteTool{MaxInputBytes: maxInputBytes}
}

func (t *WriteTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "file_write",
		Label:              "寫入檔案",
		Version:            "1.0.0",
		Category:           "files",
		Description:        "在 Project／Session Sandbox 內建立或完整覆寫 UTF-8 文字檔，content 原樣寫入，不加文件模板、不跳脫 HTML。網頁、互動遊戲、HTML/CSS/JavaScript 與其他程式原始碼應使用本工具；不要把原始碼放進 document_create 的文字區塊。預設不覆寫既有檔案，覆寫時使用同目錄暫存檔與原子替換。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"write", "atomic-replace", "workspace-sandbox", "workspace-contained", "bounded-input"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"content":       map[string]any{"type": "string", "description": "完整 UTF-8 檔案內容；網頁請提供原始 HTML/CSS/JavaScript，不包 Markdown 程式碼圍欄，不預先轉成 HTML entities"},
				"overwrite":     map[string]any{"type": "boolean", "default": false},
				"create_parent": map[string]any{"type": "boolean", "default": false},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	requested := toolutil.String(invocation.Call.Arguments, "path")
	if requested == "" {
		return fileFailure(invocation.Call, "path is required"), nil
	}
	content, ok := invocation.Call.Arguments["content"].(string)
	if !ok {
		return fileFailure(invocation.Call, "content must be a string"), nil
	}
	if len([]byte(content)) > t.MaxInputBytes {
		return fileFailure(invocation.Call, fmt.Sprintf("content exceeds %d bytes", t.MaxInputBytes)), nil
	}
	sandboxRoots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(sandboxRoots, requested, false)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	if toolutil.Bool(invocation.Call.Arguments, "create_parent", false) {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fileFailure(invocation.Call, err.Error()), nil
		}
	}
	mode := os.FileMode(0o640)
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return fileFailure(invocation.Call, "path is not a regular file"), nil
		}
		mode = info.Mode().Perm()
	}
	overwrite := toolutil.Bool(invocation.Call.Arguments, "overwrite", false)
	if err := toolutil.AtomicWriteFile(path, []byte(content), mode, overwrite); err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	bytesWritten := len([]byte(content))
	charactersWritten := utf8.RuneCountInString(content)
	lineCount := 0
	if content != "" {
		lineCount = strings.Count(content, "\n") + 1
	}
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    fmt.Sprintf("wrote %d bytes (%d Unicode characters, %d lines) to %s", bytesWritten, charactersWritten, lineCount, toolutil.DisplayPathInRoots(sandboxRoots, path)),
		Details: toolutil.ProducedFiles(map[string]any{
			"path":               toolutil.DisplayPathInRoots(sandboxRoots, path),
			"bytes":              bytesWritten,
			"unicode_characters": charactersWritten,
			"lines":              lineCount,
			"sha256":             fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
			"overwrite":          overwrite,
		}, path),
	}, nil
}
