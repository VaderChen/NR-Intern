package documents

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

	"github.com/pmezard/go-difflib/difflib"
)

type CompareTool struct {
	MaxDocumentBytes int64
	MaxOutputBytes   int
}

func NewCompareTool(maxDocumentBytes int64, maxOutputBytes int) *CompareTool {
	return &CompareTool{MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes), MaxOutputBytes: normalizedOutputLimit(maxOutputBytes)}
}

func (t *CompareTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "document_compare",
		Label:        "比對辦公文件",
		Version:      "1.0.0",
		Category:     "documents",
		Description:  "以相同的頁面、段落、工作表列或投影片範圍抽取兩份 PDF、DOCX、XLSX、PPTX 的可見文字並產生 unified diff，同時回傳原始檔 SHA-256；適合內容審閱，不把文字相同誤稱為版面相同。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"document-compare", "semantic-text-diff", "sha256", "pdf", "docx", "xlsx", "pptx", "workspace-sandbox", "bounded-output"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"left_path":        map[string]any{"type": "string"},
				"right_path":       map[string]any{"type": "string"},
				"section":          map[string]any{"type": "string", "description": "DOCX 區段或 XLSX 工作表；兩邊使用相同選擇"},
				"start_page":       map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_page":         map[string]any{"type": "integer", "minimum": 1},
				"start_paragraph":  map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_paragraph":    map[string]any{"type": "integer", "minimum": 1},
				"start_row":        map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_row":          map[string]any{"type": "integer", "minimum": 1},
				"include_formulas": map[string]any{"type": "boolean", "default": true},
				"context_lines":    map[string]any{"type": "integer", "minimum": 0, "maximum": 20, "default": 3},
				"max_characters":   map[string]any{"type": "integer", "minimum": 1000, "description": "每份文件的文字抽取上限"},
			},
			"required": []string{"left_path", "right_path"},
		},
	}
}

func (t *CompareTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	leftPath, leftDisplay, leftInfo, err := t.resolveComparePath(invocation.SandboxRoots(), toolutil.String(invocation.Call.Arguments, "left_path"))
	if err != nil {
		return documentFailure(invocation.Call, "left_path: "+err.Error()), nil
	}
	rightPath, rightDisplay, rightInfo, err := t.resolveComparePath(invocation.SandboxRoots(), toolutil.String(invocation.Call.Arguments, "right_path"))
	if err != nil {
		return documentFailure(invocation.Call, "right_path: "+err.Error()), nil
	}
	maxCharacters := toolutil.Int(invocation.Call.Arguments, "max_characters", minInt(t.MaxOutputBytes*2, 2*1024*1024), 1_000, 2*1024*1024)
	options := readOptions{
		Section:        toolutil.String(invocation.Call.Arguments, "section"),
		StartPage:      toolutil.Int(invocation.Call.Arguments, "start_page", 1, 1, 1_000_000),
		EndPage:        toolutil.Int(invocation.Call.Arguments, "end_page", 0, 0, 1_000_000),
		StartParagraph: toolutil.Int(invocation.Call.Arguments, "start_paragraph", 1, 1, 10_000_000),
		EndParagraph:   toolutil.Int(invocation.Call.Arguments, "end_paragraph", 0, 0, 10_000_000),
		StartRow:       toolutil.Int(invocation.Call.Arguments, "start_row", 1, 1, 10_000_000),
		EndRow:         toolutil.Int(invocation.Call.Arguments, "end_row", 0, 0, 10_000_000),
		MaxCharacters:  maxCharacters,
		IncludeFormula: toolutil.Bool(invocation.Call.Arguments, "include_formulas", true),
	}
	leftResult, err := readDocument(ctx, leftPath, leftInfo, options)
	if err != nil {
		return documentFailure(invocation.Call, "read left document: "+err.Error()), nil
	}
	rightResult, err := readDocument(ctx, rightPath, rightInfo, options)
	if err != nil {
		return documentFailure(invocation.Call, "read right document: "+err.Error()), nil
	}
	leftData, err := readRegularFile(leftPath, t.MaxDocumentBytes)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	rightData, err := readRegularFile(rightPath, t.MaxDocumentBytes)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	textEqual := leftResult.Content == rightResult.Content
	binaryEqual := bytes.Equal(leftData, rightData)
	diff := "document text is identical"
	truncated := false
	if !textEqual {
		diff, err = difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(leftResult.Content), B: difflib.SplitLines(rightResult.Content), FromFile: leftDisplay, ToFile: rightDisplay, Context: toolutil.Int(invocation.Call.Arguments, "context_lines", 3, 0, 20)})
		if err != nil {
			return documentFailure(invocation.Call, fmt.Sprintf("create document diff: %v", err)), nil
		}
		diff, truncated = limitUTF8(diff, t.MaxOutputBytes)
		if truncated {
			diff += "\n[document diff truncated; narrow the selected range]"
		}
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: diff, Details: map[string]any{
		"text_equal": textEqual, "binary_equal": binaryEqual, "left_path": leftDisplay, "right_path": rightDisplay,
		"left_format": leftResult.Format, "right_format": rightResult.Format,
		"left_sha256": fmt.Sprintf("%x", sha256.Sum256(leftData)), "right_sha256": fmt.Sprintf("%x", sha256.Sum256(rightData)),
		"truncated": truncated, "visual_check": "not_run",
	}}, nil
}

func (t *CompareTool) resolveComparePath(roots []string, requested string) (string, string, os.FileInfo, error) {
	path, err := toolutil.ResolvePathInRoots(roots, requested, true)
	if err != nil {
		return "", "", nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > t.MaxDocumentBytes {
		return "", "", nil, fmt.Errorf("document exceeds the %d byte safety limit", t.MaxDocumentBytes)
	}
	return path, toolutil.DisplayPathInRoots(roots, path), info, nil
}
