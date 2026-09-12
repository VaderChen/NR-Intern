package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"
)

type EditTool struct {
	MaxFileBytes int
}

func NewEditTool(maxFileBytes int) *EditTool {
	if maxFileBytes <= 0 {
		maxFileBytes = 8 * 1024 * 1024
	}
	return &EditTool{MaxFileBytes: maxFileBytes}
}

func (t *EditTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "file_edit",
		Label:              "精確編輯檔案",
		Version:            "1.0.0",
		Category:           "files",
		Description:        "在 workspace 文字檔中精確替換 old_text。預設只替換第一處，可指定全部替換與預期替換數，寫回時採原子替換。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"exact-replace", "precondition", "atomic-replace", "workspace-sandbox", "workspace-contained"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":                  map[string]any{"type": "string"},
				"old_text":              map[string]any{"type": "string"},
				"new_text":              map[string]any{"type": "string"},
				"replace_all":           map[string]any{"type": "boolean", "default": false},
				"expected_replacements": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000, "description": "預期替換數，不代表檔案版本；版本檢查請用 expected_sha256"},
				"expected_sha256":       map[string]any{"type": "string", "description": "可選的完整檔案 SHA-256；與目前內容不同則拒絕修改"},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	requested := toolutil.String(invocation.Call.Arguments, "path")
	oldText, oldOK := invocation.Call.Arguments["old_text"].(string)
	newText, newOK := invocation.Call.Arguments["new_text"].(string)
	if requested == "" || !oldOK || oldText == "" || !newOK {
		return fileFailure(invocation.Call, "path, non-empty old_text and string new_text are required"), nil
	}
	sandboxRoots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(sandboxRoots, requested, true)
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	replacements := 1
	data, updatedData, err := toolutil.AtomicUpdateFile(path, t.MaxFileBytes, func(data []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if expected, exists := invocation.Call.Arguments["expected_sha256"]; exists {
			hash, ok := expected.(string)
			if !ok || !strings.EqualFold(strings.TrimSpace(hash), fmt.Sprintf("%x", sha256.Sum256(data))) {
				return nil, fmt.Errorf("file version precondition failed; read the current file before editing")
			}
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("file is not valid UTF-8 text")
		}
		content := string(data)
		occurrences := strings.Count(content, oldText)
		if occurrences == 0 {
			return nil, fmt.Errorf("old_text was not found; file was not changed")
		}
		if toolutil.Bool(invocation.Call.Arguments, "replace_all", false) {
			replacements = occurrences
		}
		if _, exists := invocation.Call.Arguments["expected_replacements"]; exists {
			expected := toolutil.Int(invocation.Call.Arguments, "expected_replacements", 1, 1, 10000)
			if replacements != expected {
				return nil, fmt.Errorf("replacement precondition failed: expected %d, would replace %d", expected, replacements)
			}
		}
		if growth := len(newText) - len(oldText); growth > 0 && replacements > (t.MaxFileBytes-len(content))/growth {
			return nil, fmt.Errorf("edited file would exceed %d bytes", t.MaxFileBytes)
		}
		return []byte(strings.Replace(content, oldText, newText, replacements)), nil
	})
	if err != nil {
		return fileFailure(invocation.Call, err.Error()), nil
	}
	updated := string(updatedData)
	updatedBytes := len([]byte(updated))
	updatedCharacters := utf8.RuneCountInString(updated)
	updatedLines := 0
	if updated != "" {
		updatedLines = strings.Count(updated, "\n") + 1
	}
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    fmt.Sprintf("replaced %d occurrence(s) in %s; result is %d bytes (%d Unicode characters, %d lines)", replacements, toolutil.DisplayPathInRoots(sandboxRoots, path), updatedBytes, updatedCharacters, updatedLines),
		Details: toolutil.ProducedFiles(map[string]any{
			"path":               toolutil.DisplayPathInRoots(sandboxRoots, path),
			"replacements":       replacements,
			"bytes":              updatedBytes,
			"unicode_characters": updatedCharacters,
			"lines":              updatedLines,
			"before_sha256":      fmt.Sprintf("%x", sha256.Sum256(data)),
			"after_sha256":       fmt.Sprintf("%x", sha256.Sum256([]byte(updated))),
		}, path),
	}, nil
}
