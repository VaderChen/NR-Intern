package files

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

type CompareTool struct {
	MaxFileBytes int64
	MaxDiffBytes int
}

func NewCompareTool(maxFileBytes int64, maxDiffBytes int) *CompareTool {
	if maxFileBytes <= 0 {
		maxFileBytes = 4 * 1024 * 1024
	}
	if maxDiffBytes <= 0 {
		maxDiffBytes = 512 * 1024
	}
	return &CompareTool{MaxFileBytes: maxFileBytes, MaxDiffBytes: maxDiffBytes}
}

func (t *CompareTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "file_compare",
		Label:        "比對檔案",
		Version:      "1.0.0",
		Category:     "files",
		Description:  "比對 Project／Session Sandbox 內兩個檔案；文字檔回傳 unified diff，二進位檔回傳大小與 SHA-256。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"text-diff", "binary-compare", "sha256", "workspace-sandbox"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"left_path":     map[string]any{"type": "string"},
				"right_path":    map[string]any{"type": "string"},
				"context_lines": map[string]any{"type": "integer", "minimum": 0, "maximum": 20, "default": 3},
			},
			"required": []string{"left_path", "right_path"},
		},
	}
}

func (t *CompareTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	sandboxRoots := invocation.SandboxRoots()
	left, err := toolutil.ResolvePathInRoots(sandboxRoots, toolutil.String(invocation.Call.Arguments, "left_path"), true)
	if err != nil {
		return fileFailure(invocation.Call, "left_path: "+err.Error()), nil
	}
	right, err := toolutil.ResolvePathInRoots(sandboxRoots, toolutil.String(invocation.Call.Arguments, "right_path"), true)
	if err != nil {
		return fileFailure(invocation.Call, "right_path: "+err.Error()), nil
	}
	leftData, err := readBoundedFile(left, t.MaxFileBytes)
	if err != nil {
		return fileFailure(invocation.Call, "left_path: "+err.Error()), nil
	}
	rightData, err := readBoundedFile(right, t.MaxFileBytes)
	if err != nil {
		return fileFailure(invocation.Call, "right_path: "+err.Error()), nil
	}
	equal := bytes.Equal(leftData, rightData)
	leftHash := fmt.Sprintf("%x", sha256.Sum256(leftData))
	rightHash := fmt.Sprintf("%x", sha256.Sum256(rightData))
	details := map[string]any{
		"equal":        equal,
		"left_path":    toolutil.DisplayPathInRoots(sandboxRoots, left),
		"right_path":   toolutil.DisplayPathInRoots(sandboxRoots, right),
		"left_size":    len(leftData),
		"right_size":   len(rightData),
		"left_sha256":  leftHash,
		"right_sha256": rightHash,
	}
	if equal {
		return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: "files are identical", Details: details}, nil
	}
	if isBinary(leftData) || isBinary(rightData) {
		details["binary"] = true
		return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: fmt.Sprintf("binary files differ\nleft sha256: %s\nright sha256: %s", leftHash, rightHash), Details: details}, nil
	}
	contextLines := toolutil.Int(invocation.Call.Arguments, "context_lines", 3, 0, 20)
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(leftData)),
		B:        difflib.SplitLines(string(rightData)),
		FromFile: toolutil.DisplayPathInRoots(sandboxRoots, left),
		ToFile:   toolutil.DisplayPathInRoots(sandboxRoots, right),
		Context:  contextLines,
	})
	if err != nil {
		return fileFailure(invocation.Call, fmt.Sprintf("create diff: %v", err)), nil
	}
	truncated := false
	if len(diff) > t.MaxDiffBytes {
		diff = diff[:t.MaxDiffBytes] + "\n[diff truncated]"
		truncated = true
	}
	details["binary"] = false
	details["truncated"] = truncated
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: diff, Details: details}, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d-byte comparison limit", limit)
	}
	return os.ReadFile(path)
}

func isBinary(value []byte) bool {
	sample := value
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	return bytes.IndexByte(sample, 0) >= 0 || !utf8.Valid(sample)
}
