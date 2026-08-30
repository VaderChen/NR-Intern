package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxAuthoredDocumentBytes = int64(128 * 1024 * 1024)

type CreateTool struct {
	MaxInputBytes    int
	MaxDocumentBytes int64
}

type EditTool struct {
	MaxInputBytes    int
	MaxDocumentBytes int64
}

type documentCreateRequest struct {
	Path         string                   `json:"path"`
	Format       string                   `json:"format"`
	TemplatePath string                   `json:"template_path"`
	Title        string                   `json:"title"`
	Subject      string                   `json:"subject"`
	Author       string                   `json:"author"`
	Overwrite    bool                     `json:"overwrite"`
	CreateParent bool                     `json:"create_parent"`
	FontPath     string                   `json:"font_path"`
	Blocks       []documentBlock          `json:"blocks"`
	Sheets       []spreadsheetSheet       `json:"sheets"`
	Slides       []presentationSlideInput `json:"slides"`
	Replacements []textReplacement        `json:"replacements"`
	CellUpdates  []cellUpdate             `json:"cell_updates"`
	Annotations  []pdfAnnotation          `json:"annotations"`
}

type documentEditRequest struct {
	Path         string            `json:"path"`
	OutputPath   string            `json:"output_path"`
	Overwrite    bool              `json:"overwrite"`
	CreateParent bool              `json:"create_parent"`
	FontPath     string            `json:"font_path"`
	Replacements []textReplacement `json:"replacements"`
	CellUpdates  []cellUpdate      `json:"cell_updates"`
	Annotations  []pdfAnnotation   `json:"annotations"`
}

type documentBlock struct {
	Type  string     `json:"type"`
	Text  string     `json:"text"`
	Level int        `json:"level"`
	Rows  [][]string `json:"rows"`
}

type spreadsheetSheet struct {
	Name         string             `json:"name"`
	Rows         [][]any            `json:"rows"`
	Formulas     map[string]string  `json:"formulas"`
	ColumnWidths map[string]float64 `json:"column_widths"`
	HeaderRows   int                `json:"header_rows"`
	FreezeRows   int                `json:"freeze_rows"`
	AutoFilter   bool               `json:"auto_filter"`
}

type presentationSlideInput struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Body     string   `json:"body"`
	Bullets  []string `json:"bullets"`
}

type textReplacement struct {
	OldText              string `json:"old_text"`
	NewText              string `json:"new_text"`
	ReplaceAll           bool   `json:"replace_all"`
	ExpectedReplacements int    `json:"expected_replacements"`
}

type cellUpdate struct {
	Sheet   string `json:"sheet"`
	Cell    string `json:"cell"`
	Value   any    `json:"value"`
	Formula string `json:"formula"`
}

type pdfAnnotation struct {
	Page      int     `json:"page"`
	Type      string  `json:"type"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
	X2        float64 `json:"x2"`
	Y2        float64 `json:"y2"`
	Text      string  `json:"text"`
	Color     string  `json:"color"`
	LineWidth float64 `json:"line_width"`
	FontSize  float64 `json:"font_size"`
}

type authoredDocument struct {
	Data    []byte
	Details map[string]any
}

func NewCreateTool(maxInputBytes int, maxDocumentBytes int64) *CreateTool {
	return &CreateTool{MaxInputBytes: normalizedAuthoringInputLimit(maxInputBytes), MaxDocumentBytes: normalizedAuthoredDocumentLimit(maxDocumentBytes)}
}

func NewEditTool(maxInputBytes int, maxDocumentBytes int64) *EditTool {
	return &EditTool{MaxInputBytes: normalizedAuthoringInputLimit(maxInputBytes), MaxDocumentBytes: normalizedAuthoredDocumentLimit(maxDocumentBytes)}
}

func (t *CreateTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "document_create",
		Label:              "建立辦公文件",
		Version:            "1.0.0",
		Category:           "documents",
		Description:        "在 Project／Session Sandbox 內以結構化內容建立 DOCX、XLSX、PPTX 或 PDF。可用 template_path 保留既有文件樣式，再以 replacements、cell_updates 或 annotations 填入內容。預設不覆寫既有檔案；Unicode PDF 會優先使用指定字型，否則自動探索完整覆蓋的系統字型。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"document-create", "template-preservation", "automatic-font-discovery", "glyph-coverage", "docx", "xlsx", "pptx", "pdf", "workspace-sandbox", "atomic-write", "atomic-replace", "bounded-input"},
		RequiresPermission: true,
		InputSchema:        documentCreateSchema(),
	}
}

func (t *EditTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "document_edit",
		Label:              "編輯辦公文件",
		Version:            "1.0.0",
		Category:           "documents",
		Description:        "保留來源文件，將局部編輯結果另存到 output_path。DOCX/PPTX 使用 replacements 精確替換文字；XLSX 使用 cell_updates 更新儲存格，也可 replacements；PDF 使用 annotations 疊加文字、線段或方框，Unicode 標註可自動探索完整覆蓋的系統 TTF。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"document-edit", "exact-replace", "spreadsheet-cell-update", "pdf-annotation", "automatic-font-discovery", "glyph-coverage", "docx", "xlsx", "pptx", "pdf", "workspace-sandbox", "atomic-write", "atomic-replace"},
		RequiresPermission: true,
		InputSchema:        documentEditSchema(),
	}
}

func (t *CreateTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	var request documentCreateRequest
	if err := decodeAuthoringArguments(invocation.Call.Arguments, t.MaxInputBytes, &request); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	roots := invocation.SandboxRoots()
	outputPath, err := resolveAuthoringOutput(roots, request.Path, request.CreateParent)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	format, err := requestedDocumentFormat(request.Format, outputPath)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	fontPath, err := resolveOptionalFont(roots, request.FontPath)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	var authored authoredDocument
	if strings.TrimSpace(request.TemplatePath) != "" {
		templatePath, resolveErr := resolveTemplatePath(roots, request.TemplatePath, format, t.MaxDocumentBytes)
		if resolveErr != nil {
			return documentFailure(invocation.Call, resolveErr.Error()), nil
		}
		fontPath, err = resolveAutomaticPDFFont(ctx, format, fontPath, annotationText(request.Annotations))
		if err == nil {
			authored, err = createFromTemplate(ctx, templatePath, format, request, fontPath)
		}
	} else {
		fontPath, err = resolveAutomaticPDFFont(ctx, format, fontPath, pdfRequestText(request))
		if err == nil {
			switch format {
			case formatDOCX:
				authored, err = createDOCX(ctx, request)
			case formatXLSX:
				authored, err = createXLSX(ctx, request)
			case formatPPTX:
				authored, err = createPPTX(ctx, request)
			case formatPDF:
				authored, err = createPDF(ctx, request, fontPath)
			}
		}
	}
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if int64(len(authored.Data)) > t.MaxDocumentBytes {
		return documentFailure(invocation.Call, fmt.Sprintf("generated document exceeds the %d byte safety limit", t.MaxDocumentBytes)), nil
	}
	mode := os.FileMode(0o640)
	if info, statErr := os.Stat(outputPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := toolutil.AtomicWriteFile(outputPath, authored.Data, mode, request.Overwrite); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	displayPath := toolutil.DisplayPathInRoots(roots, outputPath)
	details := authored.Details
	if details == nil {
		details = map[string]any{}
	}
	details["path"] = displayPath
	details["format"] = string(format)
	details["bytes"] = len(authored.Data)
	details["sha256"] = fmt.Sprintf("%x", sha256.Sum256(authored.Data))
	details["overwrite"] = request.Overwrite
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    fmt.Sprintf("created %s document at %s (%d bytes)", strings.ToUpper(string(format)), displayPath, len(authored.Data)),
		Details:    details,
	}, nil
}

func (t *EditTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	var request documentEditRequest
	if err := decodeAuthoringArguments(invocation.Call.Arguments, t.MaxInputBytes, &request); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if strings.TrimSpace(request.Path) == "" || strings.TrimSpace(request.OutputPath) == "" {
		return documentFailure(invocation.Call, "path and output_path are required"), nil
	}
	roots := invocation.SandboxRoots()
	sourcePath, err := toolutil.ResolvePathInRoots(roots, request.Path, true)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	info, err := os.Stat(sourcePath)
	if err != nil || !info.Mode().IsRegular() {
		return documentFailure(invocation.Call, "path is not a regular file"), nil
	}
	if info.Size() > t.MaxDocumentBytes {
		return documentFailure(invocation.Call, fmt.Sprintf("document exceeds the %d byte safety limit", t.MaxDocumentBytes)), nil
	}
	outputPath, err := resolveAuthoringOutput(roots, request.OutputPath, request.CreateParent)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if filepath.Clean(sourcePath) == filepath.Clean(outputPath) {
		return documentFailure(invocation.Call, "output_path must differ from path so the source document is preserved"), nil
	}
	format, err := detectDocumentFormat(sourcePath)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if requested, detectErr := requestedDocumentFormat("", outputPath); detectErr != nil || requested != format {
		return documentFailure(invocation.Call, fmt.Sprintf("output_path extension must be .%s", format)), nil
	}
	fontPath, err := resolveOptionalFont(roots, request.FontPath)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	var authored authoredDocument
	fontPath, err = resolveAutomaticPDFFont(ctx, format, fontPath, annotationText(request.Annotations))
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	switch format {
	case formatDOCX, formatPPTX, formatXLSX:
		authored, err = editOpenXML(ctx, sourcePath, format, request)
	case formatPDF:
		authored, err = editPDF(ctx, sourcePath, request, fontPath)
	}
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if int64(len(authored.Data)) > t.MaxDocumentBytes {
		return documentFailure(invocation.Call, fmt.Sprintf("edited document exceeds the %d byte safety limit", t.MaxDocumentBytes)), nil
	}
	mode := info.Mode().Perm()
	if outputInfo, statErr := os.Stat(outputPath); statErr == nil {
		mode = outputInfo.Mode().Perm()
	}
	if err := toolutil.AtomicWriteFile(outputPath, authored.Data, mode, request.Overwrite); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	displayPath := toolutil.DisplayPathInRoots(roots, outputPath)
	details := authored.Details
	if details == nil {
		details = map[string]any{}
	}
	details["path"] = displayPath
	details["source_path"] = toolutil.DisplayPathInRoots(roots, sourcePath)
	details["format"] = string(format)
	details["bytes"] = len(authored.Data)
	details["sha256"] = fmt.Sprintf("%x", sha256.Sum256(authored.Data))
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    fmt.Sprintf("created edited %s copy at %s (%d bytes); source was preserved", strings.ToUpper(string(format)), displayPath, len(authored.Data)),
		Details:    details,
	}, nil
}

func decodeAuthoringArguments(arguments map[string]any, maxBytes int, target any) error {
	data, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("encode tool arguments: %w", err)
	}
	if len(data) > maxBytes {
		return fmt.Errorf("document input exceeds %d bytes", maxBytes)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid document input: %w", err)
	}
	return nil
}

func resolveAuthoringOutput(roots []string, requested string, createParent bool) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("path is required")
	}
	outputPath, err := toolutil.ResolvePathInRoots(roots, requested, false)
	if err != nil {
		return "", err
	}
	if createParent {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
			return "", err
		}
	}
	if info, statErr := os.Stat(outputPath); statErr == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	return outputPath, nil
}

func requestedDocumentFormat(requested, outputPath string) (documentFormat, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(outputPath)), ".")
	if requested == "" {
		requested = extension
	}
	format := documentFormat(requested)
	switch format {
	case formatDOCX, formatXLSX, formatPPTX, formatPDF:
	default:
		return "", fmt.Errorf("format must be docx, xlsx, pptx or pdf")
	}
	if extension != string(format) {
		return "", fmt.Errorf("path extension must be .%s", format)
	}
	return format, nil
}

func resolveOptionalFont(roots []string, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", nil
	}
	fontPath, err := toolutil.ResolvePathInRoots(roots, requested, true)
	if err != nil {
		return "", fmt.Errorf("resolve font_path: %w", err)
	}
	info, err := os.Stat(fontPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("font_path is not a regular file")
	}
	if info.Size() > 32*1024*1024 {
		return "", fmt.Errorf("font_path exceeds the 32 MB safety limit")
	}
	if !strings.EqualFold(filepath.Ext(fontPath), ".ttf") {
		return "", fmt.Errorf("font_path must point to a TrueType .ttf file")
	}
	return fontPath, nil
}

func normalizedAuthoringInputLimit(value int) int {
	if value <= 0 {
		return 8 * 1024 * 1024
	}
	return value
}

func normalizedAuthoredDocumentLimit(value int64) int64 {
	if value <= 0 {
		return defaultMaxAuthoredDocumentBytes
	}
	return value
}

func documentCreateSchema() map[string]any {
	block := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":  map[string]any{"type": "string", "enum": []string{"heading", "paragraph", "bullet", "numbered", "table", "page_break"}},
			"text":  map[string]any{"type": "string"},
			"level": map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
			"rows":  map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		},
		"required": []string{"type"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":          map[string]any{"type": "string", "description": "輸出檔路徑，副檔名須與 format 一致"},
			"format":        map[string]any{"type": "string", "enum": []string{"docx", "xlsx", "pptx", "pdf"}, "description": "省略時由 path 副檔名判斷"},
			"template_path": map[string]any{"type": "string", "description": "可選的同格式來源範本；DOCX/PPTX 用 replacements、XLSX 用 cell_updates、PDF 用 annotations 填入，未提供操作時原樣複製"},
			"title":         map[string]any{"type": "string"},
			"subject":       map[string]any{"type": "string"},
			"author":        map[string]any{"type": "string"},
			"overwrite":     map[string]any{"type": "boolean", "default": false},
			"create_parent": map[string]any{"type": "boolean", "default": false},
			"font_path":     map[string]any{"type": "string", "description": "PDF Unicode 文字使用的 Sandbox 內 TTF 字型"},
			"blocks":        map[string]any{"type": "array", "items": block, "description": "DOCX/PDF 內容區塊"},
			"sheets": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"name", "rows"}, "properties": map[string]any{
					"name":          map[string]any{"type": "string"},
					"rows":          map[string]any{"type": "array", "items": map[string]any{"type": "array"}},
					"formulas":      map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"column_widths": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "number"}},
					"header_rows":   map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
					"freeze_rows":   map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000},
					"auto_filter":   map[string]any{"type": "boolean"},
				}}, "description": "XLSX 工作表"},
			"slides": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"title": map[string]any{"type": "string"}, "subtitle": map[string]any{"type": "string"},
					"body": map[string]any{"type": "string"}, "bullets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				}}, "description": "PPTX 投影片"},
			"replacements": replacementSchema(),
			"cell_updates": cellUpdateSchema(),
			"annotations":  annotationSchema(),
		},
		"required": []string{"path"},
	}
}

func documentEditSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":          map[string]any{"type": "string", "description": "既有來源文件"},
			"output_path":   map[string]any{"type": "string", "description": "另存新檔路徑，格式須與來源相同"},
			"overwrite":     map[string]any{"type": "boolean", "default": false},
			"create_parent": map[string]any{"type": "boolean", "default": false},
			"font_path":     map[string]any{"type": "string", "description": "PDF 文字標註使用的 Sandbox 內 TTF 字型"},
			"replacements":  replacementSchema(),
			"cell_updates":  cellUpdateSchema(),
			"annotations":   annotationSchema(),
		},
		"required": []string{"path", "output_path"},
	}
}

func replacementSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object", "required": []string{"old_text", "new_text"}, "properties": map[string]any{
				"old_text": map[string]any{"type": "string", "minLength": 1}, "new_text": map[string]any{"type": "string"},
				"replace_all":           map[string]any{"type": "boolean", "default": false},
				"expected_replacements": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
			},
		},
	}
}

func cellUpdateSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object", "required": []string{"sheet", "cell"}, "properties": map[string]any{
				"sheet": map[string]any{"type": "string"}, "cell": map[string]any{"type": "string"},
				"value": map[string]any{}, "formula": map[string]any{"type": "string"},
			},
		},
	}
}

func annotationSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object", "required": []string{"page", "type", "x", "y"}, "properties": map[string]any{
				"page": map[string]any{"type": "integer", "minimum": 1}, "type": map[string]any{"type": "string", "enum": []string{"text", "line", "rectangle"}},
				"x": map[string]any{"type": "number", "minimum": 0}, "y": map[string]any{"type": "number", "minimum": 0},
				"width": map[string]any{"type": "number", "minimum": 0}, "height": map[string]any{"type": "number", "minimum": 0},
				"x2": map[string]any{"type": "number", "minimum": 0}, "y2": map[string]any{"type": "number", "minimum": 0},
				"text": map[string]any{"type": "string"}, "color": map[string]any{"type": "string", "description": "#RRGGBB"},
				"line_width": map[string]any{"type": "number", "minimum": 0.1, "maximum": 72}, "font_size": map[string]any{"type": "number", "minimum": 4, "maximum": 144},
			},
		},
	}
}
