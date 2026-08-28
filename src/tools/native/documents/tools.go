package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxDocumentBytes = int64(128 * 1024 * 1024)
	defaultMaxOutputBytes   = 512 * 1024
	maxExpandedEntryBytes   = int64(64 * 1024 * 1024)
)

type InspectTool struct {
	MaxDocumentBytes int64
	MaxOutputBytes   int
}

type ReadTool struct {
	MaxDocumentBytes int64
	MaxOutputBytes   int
}

type documentInspection struct {
	Format         string            `json:"format"`
	MediaType      string            `json:"media_type"`
	SizeBytes      int64             `json:"size_bytes"`
	PageCount      int               `json:"page_count,omitempty"`
	ParagraphCount int               `json:"paragraph_count,omitempty"`
	SheetCount     int               `json:"sheet_count,omitempty"`
	SlideCount     int               `json:"slide_count,omitempty"`
	Sections       []documentSection `json:"sections,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Warnings       []string          `json:"warnings,omitempty"`
}

type documentSection struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Index   int    `json:"index,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Columns int    `json:"columns,omitempty"`
}

type readOptions struct {
	Section        string
	StartPage      int
	EndPage        int
	StartParagraph int
	EndParagraph   int
	StartRow       int
	EndRow         int
	MaxCharacters  int
	IncludeFormula bool
}

type documentReadResult struct {
	Format   string
	Content  string
	Details  map[string]any
	Warnings []string
}

func NewInspectTool(maxDocumentBytes int64, maxOutputBytes int) *InspectTool {
	return &InspectTool{
		MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes),
		MaxOutputBytes:   normalizedOutputLimit(maxOutputBytes),
	}
}

func NewReadTool(maxDocumentBytes int64, maxOutputBytes int) *ReadTool {
	return &ReadTool{
		MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes),
		MaxOutputBytes:   normalizedOutputLimit(maxOutputBytes),
	}
}

func (t *InspectTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "document_inspect",
		Label:        "檢視辦公文件",
		Version:      "1.0.0",
		Category:     "documents",
		Description:  "檢視 Project／Session Sandbox 內的 PDF、Word（DOCX）、Excel（XLSX）或 PowerPoint（PPTX），回傳格式、頁數／工作表／投影片、區段與文件中繼資料。應先用此工具確認大型文件結構，再用 document_read 分段讀取；掃描型 PDF 不包含 OCR。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"document-metadata", "pdf", "docx", "xlsx", "pptx", "workspace-sandbox", "bounded-output"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Sandbox 內的相對或絕對文件路徑"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "document_read",
		Label:        "讀取辦公文件",
		Version:      "1.0.0",
		Category:     "documents",
		Description:  "從 PDF、DOCX、XLSX 或 PPTX 分段抽取可供分析的文字。PDF／PPTX 用 start_page/end_page，DOCX 用 section 與 start_paragraph/end_paragraph，XLSX 用 section（工作表名稱）與 start_row/end_row。大型文件不可一次完整讀取；掃描型 PDF 需另行使用 OCR。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"document-text", "page-range", "paragraph-range", "sheet-row-range", "pdf", "docx", "xlsx", "pptx", "workspace-sandbox", "bounded-output"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":            map[string]any{"type": "string", "description": "Sandbox 內的相對或絕對文件路徑"},
				"section":         map[string]any{"type": "string", "description": "DOCX 區段（預設 body）或 XLSX 工作表名稱；PPTX 可填投影片編號"},
				"start_page":      map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_page":        map[string]any{"type": "integer", "minimum": 1, "description": "PDF／PPTX 結束頁，包含此頁；省略時只讀起始頁"},
				"start_paragraph": map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_paragraph":   map[string]any{"type": "integer", "minimum": 1, "description": "DOCX 結束段落，包含此段；省略時最多讀 200 段"},
				"start_row":       map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_row":         map[string]any{"type": "integer", "minimum": 1, "description": "XLSX 結束列，包含此列；省略時最多讀 200 列"},
				"max_characters":  map[string]any{"type": "integer", "minimum": 1000, "description": "本次最多回傳字元數，不可超過後端工具輸出上限"},
				"include_formulas": map[string]any{"type": "boolean", "default": true,
					"description": "XLSX 是否在儲存格值旁保留公式"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *InspectTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	path, displayPath, info, failure := resolveDocument(invocation, t.MaxDocumentBytes)
	if failure != "" {
		return documentFailure(invocation.Call, failure), nil
	}
	inspection, err := inspectDocument(ctx, path, info)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	encoded, err := json.MarshalIndent(inspection, "", "  ")
	if err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("encode document inspection: %v", err)), nil
	}
	content, truncated := limitUTF8(string(encoded), t.MaxOutputBytes)
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    content,
		Details: map[string]any{
			"path":       displayPath,
			"format":     inspection.Format,
			"truncated":  truncated,
			"size_bytes": info.Size(),
		},
	}, nil
}

func (t *ReadTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	path, displayPath, info, failure := resolveDocument(invocation, t.MaxDocumentBytes)
	if failure != "" {
		return documentFailure(invocation.Call, failure), nil
	}
	arguments := invocation.Call.Arguments
	maxCharacters := toolutil.Int(arguments, "max_characters", t.MaxOutputBytes, 1_000, t.MaxOutputBytes)
	options := readOptions{
		Section:        toolutil.String(arguments, "section"),
		StartPage:      toolutil.Int(arguments, "start_page", 1, 1, 1_000_000),
		EndPage:        toolutil.Int(arguments, "end_page", 0, 0, 1_000_000),
		StartParagraph: toolutil.Int(arguments, "start_paragraph", 1, 1, 10_000_000),
		EndParagraph:   toolutil.Int(arguments, "end_paragraph", 0, 0, 10_000_000),
		StartRow:       toolutil.Int(arguments, "start_row", 1, 1, 10_000_000),
		EndRow:         toolutil.Int(arguments, "end_row", 0, 0, 10_000_000),
		MaxCharacters:  maxCharacters,
		IncludeFormula: toolutil.Bool(arguments, "include_formulas", true),
	}
	result, err := readDocument(ctx, path, info, options)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	content, truncated := limitUTF8(result.Content, maxCharacters)
	if truncated {
		content += "\n[document output truncated; narrow the page, paragraph or row range]"
	}
	details := result.Details
	if details == nil {
		details = map[string]any{}
	}
	details["path"] = displayPath
	details["format"] = result.Format
	details["truncated"] = truncated
	if len(result.Warnings) > 0 {
		details["warnings"] = result.Warnings
	}
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    content,
		Details:    details,
	}, nil
}

func resolveDocument(invocation tools.Invocation, maxDocumentBytes int64) (string, string, os.FileInfo, string) {
	requested := toolutil.String(invocation.Call.Arguments, "path")
	if requested == "" {
		return "", "", nil, "path is required"
	}
	roots := invocation.SandboxRoots()
	path, err := toolutil.ResolvePathInRoots(roots, requested, true)
	if err != nil {
		return "", "", nil, err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", nil, err.Error()
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, "path is not a regular file"
	}
	if info.Size() > maxDocumentBytes {
		return "", "", nil, fmt.Sprintf("document exceeds the %d byte safety limit", maxDocumentBytes)
	}
	return path, toolutil.DisplayPathInRoots(roots, path), info, ""
}

func normalizedDocumentLimit(value int64) int64 {
	if value <= 0 {
		return defaultMaxDocumentBytes
	}
	return value
}

func normalizedOutputLimit(value int) int {
	if value <= 0 {
		return defaultMaxOutputBytes
	}
	return value
}

func documentFailure(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    strings.TrimSpace(message),
		IsError:    true,
	}
}

func limitUTF8(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value, true
}
