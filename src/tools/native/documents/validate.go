package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const maxValidationIssues = 1000

type ValidateTool struct {
	MaxDocumentBytes int64
	MaxOutputBytes   int
}

type validationIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Part     string `json:"part,omitempty"`
}

type validationReport struct {
	Valid       bool                 `json:"valid"`
	Format      string               `json:"format"`
	SizeBytes   int64                `json:"size_bytes"`
	PageCount   int                  `json:"page_count,omitempty"`
	SheetCount  int                  `json:"sheet_count,omitempty"`
	SlideCount  int                  `json:"slide_count,omitempty"`
	Paragraphs  int                  `json:"paragraph_count,omitempty"`
	Errors      int                  `json:"error_count"`
	Warnings    int                  `json:"warning_count"`
	Issues      []validationIssue    `json:"issues,omitempty"`
	Renderers   rendererAvailability `json:"renderers"`
	VisualCheck string               `json:"visual_check"`
}

type validationCollector struct {
	strict bool
	issues []validationIssue
}

func NewValidateTool(maxDocumentBytes int64, maxOutputBytes int) *ValidateTool {
	return &ValidateTool{MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes), MaxOutputBytes: normalizedOutputLimit(maxOutputBytes)}
}

func (t *ValidateTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "document_validate",
		Label:        "驗證辦公文件",
		Version:      "1.0.0",
		Category:     "documents",
		Description:  "驗證 PDF、DOCX、XLSX 或 PPTX 的容器、XML、必要部件、內部關聯與格式特有結構；另回報公式錯誤、外部關聯、巨集／嵌入物件及視覺渲染後端狀態。結構驗證不會冒充已完成視覺檢查。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"document-validation", "openxml", "relationship-audit", "formula-error-scan", "renderer-detection", "pdf", "docx", "xlsx", "pptx", "workspace-sandbox"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":                   map[string]any{"type": "string", "description": "Sandbox 內的來源文件"},
				"strict":                 map[string]any{"type": "boolean", "default": false, "description": "將外部關聯、巨集與嵌入物件視為錯誤"},
				"require_render_backend": map[string]any{"type": "boolean", "default": false, "description": "缺少此格式所需的視覺渲染後端時視為錯誤"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ValidateTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	filePath, displayPath, info, failure := resolveDocument(invocation, t.MaxDocumentBytes)
	if failure != "" {
		return documentFailure(invocation.Call, failure), nil
	}
	strict := toolutil.Bool(invocation.Call.Arguments, "strict", false)
	requireRenderer := toolutil.Bool(invocation.Call.Arguments, "require_render_backend", false)
	report, err := validateDocument(ctx, filePath, info, strict, requireRenderer)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("encode validation report: %v", err)), nil
	}
	content, truncated := limitUTF8(string(encoded), t.MaxOutputBytes)
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    content,
		Details: map[string]any{
			"path":          displayPath,
			"format":        report.Format,
			"valid":         report.Valid,
			"error_count":   report.Errors,
			"warning_count": report.Warnings,
			"truncated":     truncated,
		},
	}, nil
}

func validateDocument(ctx context.Context, filePath string, info os.FileInfo, strict, requireRenderer bool) (validationReport, error) {
	format, err := detectDocumentFormat(filePath)
	if err != nil {
		return validationReport{}, err
	}
	collector := &validationCollector{strict: strict}
	report := validationReport{Format: string(format), SizeBytes: info.Size(), Renderers: publicRendererAvailability(discoverRenderers()), VisualCheck: "not_run"}
	if format == formatPDF {
		inspection, inspectErr := inspectDocument(ctx, filePath, info)
		if inspectErr != nil {
			collector.error("pdf_parse_failed", inspectErr.Error(), "")
		} else {
			report.PageCount = inspection.PageCount
			if inspection.PageCount <= 0 {
				collector.error("pdf_has_no_pages", "PDF does not contain any pages", "")
			}
		}
	} else {
		validateOpenXML(ctx, filePath, format, collector)
		inspection, inspectErr := inspectDocument(ctx, filePath, info)
		if inspectErr != nil {
			collector.error("document_inspection_failed", inspectErr.Error(), "")
		} else {
			report.PageCount = inspection.PageCount
			report.SheetCount = inspection.SheetCount
			report.SlideCount = inspection.SlideCount
			report.Paragraphs = inspection.ParagraphCount
		}
	}
	requireOffice := format != formatPDF
	if requireRenderer {
		if report.Renderers.PDFRenderer == "" {
			collector.error("pdf_renderer_unavailable", "Poppler pdftoppm is unavailable", "")
		}
		if requireOffice && report.Renderers.OfficeConverter == "" {
			collector.error("office_converter_unavailable", "LibreOffice soffice is unavailable", "")
		}
	} else {
		if report.Renderers.PDFRenderer == "" {
			collector.warning("pdf_renderer_unavailable", "visual validation requires Poppler pdftoppm", "")
		}
		if requireOffice && report.Renderers.OfficeConverter == "" {
			collector.warning("office_converter_unavailable", "visual validation for Office files requires LibreOffice soffice", "")
		}
	}
	collector.sort()
	report.Issues = collector.issues
	for _, issue := range collector.issues {
		if issue.Severity == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	report.Valid = report.Errors == 0
	return report, nil
}

func validateOpenXML(ctx context.Context, filePath string, format documentFormat, collector *validationCollector) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		collector.error("openxml_package_invalid", err.Error(), "")
		return
	}
	defer archive.Close()
	required := map[documentFormat][]string{
		formatDOCX: {"[Content_Types].xml", "_rels/.rels", "word/document.xml"},
		formatXLSX: {"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels"},
		formatPPTX: {"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml", "ppt/_rels/presentation.xml.rels"},
	}
	for _, name := range required[format] {
		if !archive.has(name) {
			collector.error("required_part_missing", "required Open XML part is missing", name)
		}
	}
	seen := map[string]bool{}
	for _, entry := range archive.reader.File {
		if ctx.Err() != nil {
			collector.error("validation_cancelled", ctx.Err().Error(), "")
			return
		}
		normalized := strings.TrimPrefix(path.Clean(strings.ReplaceAll(entry.Name, "\\", "/")), "./")
		if normalized == "." || strings.HasPrefix(normalized, "../") || strings.HasPrefix(normalized, "/") {
			collector.error("unsafe_part_path", "Open XML package contains an unsafe part path", filepath.ToSlash(entry.Name))
			continue
		}
		if seen[normalized] {
			collector.error("duplicate_part", "Open XML package contains a duplicate part name", normalized)
			continue
		}
		seen[normalized] = true
		if entry.UncompressedSize64 > uint64(maxExpandedEntryBytes) {
			collector.error("expanded_part_too_large", fmt.Sprintf("expanded part exceeds %d bytes", maxExpandedEntryBytes), normalized)
			continue
		}
		lowerName := strings.ToLower(normalized)
		if strings.HasSuffix(lowerName, "vbaproject.bin") || strings.Contains(lowerName, "/macros/") {
			collector.unsupported("macro_present", "document contains a macro project that NR-Intern will not execute", normalized)
		}
		if strings.Contains(lowerName, "/embeddings/") || strings.Contains(lowerName, "oleobject") {
			collector.unsupported("embedded_object_present", "document contains an embedded or OLE object that NR-Intern will not execute", normalized)
		}
		if strings.HasSuffix(lowerName, ".xml") || strings.HasSuffix(lowerName, ".rels") {
			data, readErr := archive.read(normalized)
			if readErr != nil {
				collector.error("part_read_failed", readErr.Error(), normalized)
				continue
			}
			if parseErr := validateXML(data); parseErr != nil {
				collector.error("xml_invalid", parseErr.Error(), normalized)
				continue
			}
			if strings.HasSuffix(lowerName, ".rels") {
				validateRelationships(archive, normalized, data, collector)
			}
		}
	}
	if data, readErr := archive.read("[Content_Types].xml"); readErr == nil {
		validateContentTypes(archive, data, collector)
	}
	switch format {
	case formatDOCX:
		validateDOCXPackage(archive, collector)
	case formatXLSX:
		validateXLSXPackage(ctx, archive, collector)
	case formatPPTX:
		validatePPTXPackage(archive, collector)
	}
}

func validateXML(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func validateRelationships(archive *officeArchive, relationshipPart string, data []byte, collector *validationCollector) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	seenIDs := map[string]bool{}
	baseDirectory := relationshipBaseDirectory(relationshipPart)
	for {
		token, err := decoder.Token()
		if err != nil {
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		id, target, mode := "", "", ""
		for _, attribute := range start.Attr {
			switch attribute.Name.Local {
			case "Id":
				id = attribute.Value
			case "Target":
				target = attribute.Value
			case "TargetMode":
				mode = attribute.Value
			}
		}
		if id == "" || target == "" {
			collector.error("relationship_incomplete", "relationship requires Id and Target", relationshipPart)
			continue
		}
		if seenIDs[id] {
			collector.error("relationship_id_duplicate", "relationship IDs must be unique within a part", relationshipPart)
			continue
		}
		seenIDs[id] = true
		if strings.EqualFold(mode, "External") || strings.Contains(target, "://") {
			collector.unsupported("external_relationship", "document contains an external relationship; target is intentionally omitted from this report", relationshipPart)
			continue
		}
		resolved := target
		if strings.HasPrefix(resolved, "/") {
			resolved = strings.TrimPrefix(path.Clean(resolved), "/")
		} else {
			resolved = path.Clean(path.Join(baseDirectory, resolved))
		}
		if strings.HasPrefix(resolved, "../") || !archive.has(resolved) {
			collector.error("relationship_target_missing", "internal relationship target is missing or escapes the package", relationshipPart)
		}
	}
}

func relationshipBaseDirectory(relationshipPart string) string {
	if relationshipPart == "_rels/.rels" {
		return ""
	}
	directory := path.Dir(relationshipPart)
	if path.Base(directory) == "_rels" {
		directory = path.Dir(directory)
	}
	return directory
}

func validateContentTypes(archive *officeArchive, data []byte, collector *validationCollector) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Override" {
			continue
		}
		partName := strings.TrimPrefix(xmlAttribute(start, "PartName"), "/")
		if partName != "" && !archive.has(partName) {
			collector.error("content_type_target_missing", "content type override references a missing part", partName)
		}
	}
}

func validateDOCXPackage(archive *officeArchive, collector *validationCollector) {
	if !archive.has("word/styles.xml") {
		collector.warning("docx_styles_missing", "DOCX does not contain an explicit styles part", "word/styles.xml")
	}
	if data, err := archive.read("word/document.xml"); err == nil && bytes.Contains(data, []byte("<w:numId")) && !archive.has("word/numbering.xml") {
		collector.error("docx_numbering_missing", "document references numbering but word/numbering.xml is missing", "word/document.xml")
	}
}

func validateXLSXPackage(ctx context.Context, archive *officeArchive, collector *validationCollector) {
	sheets, err := workbookSheets(archive)
	if err != nil {
		collector.error("xlsx_workbook_invalid", err.Error(), "xl/workbook.xml")
		return
	}
	if len(sheets) == 0 {
		collector.error("xlsx_has_no_sheets", "XLSX does not contain any reachable worksheets", "xl/workbook.xml")
		return
	}
	seenNames := map[string]bool{}
	for _, sheet := range sheets {
		key := strings.ToLower(strings.TrimSpace(sheet.Name))
		if seenNames[key] {
			collector.error("xlsx_sheet_name_duplicate", "worksheet names must be unique", "xl/workbook.xml")
		}
		seenNames[key] = true
		data, readErr := archive.read(sheet.Part)
		if readErr != nil {
			collector.error("xlsx_sheet_unreadable", readErr.Error(), sheet.Part)
			continue
		}
		scanWorksheetErrors(ctx, data, sheet.Part, collector)
	}
}

func scanWorksheetErrors(ctx context.Context, data []byte, part string, collector *validationCollector) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	cellReference, cellType := "", ""
	for {
		if ctx.Err() != nil {
			return
		}
		token, err := decoder.Token()
		if err != nil {
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "c":
			cellReference = xmlAttribute(start, "r")
			cellType = xmlAttribute(start, "t")
		case "f":
			var formula string
			if decodeErr := decoder.DecodeElement(&formula, &start); decodeErr == nil && strings.Contains(strings.ToUpper(formula), "#REF!") {
				collector.error("xlsx_formula_ref_error", fmt.Sprintf("formula in %s contains #REF!", cellReference), part)
			}
		case "v":
			if cellType != "e" {
				continue
			}
			var value string
			if decodeErr := decoder.DecodeElement(&value, &start); decodeErr == nil {
				collector.error("xlsx_cached_formula_error", fmt.Sprintf("cell %s contains formula error %s", cellReference, strings.TrimSpace(value)), part)
			}
		}
	}
}

func validatePPTXPackage(archive *officeArchive, collector *validationCollector) {
	slides, err := presentationSlides(archive)
	if err != nil {
		collector.error("pptx_presentation_invalid", err.Error(), "ppt/presentation.xml")
		return
	}
	if len(slides) == 0 {
		collector.error("pptx_has_no_slides", "PPTX does not contain any reachable slides", "ppt/presentation.xml")
	}
}

func publicRendererAvailability(value rendererAvailability) rendererAvailability {
	if value.OfficeConverter != "" {
		value.OfficeConverter = filepath.Base(value.OfficeConverter)
	}
	if value.PDFRenderer != "" {
		value.PDFRenderer = filepath.Base(value.PDFRenderer)
	}
	return value
}

func (c *validationCollector) add(severity, code, message, part string) {
	if len(c.issues) >= maxValidationIssues {
		return
	}
	c.issues = append(c.issues, validationIssue{Severity: severity, Code: code, Message: strings.TrimSpace(message), Part: filepath.ToSlash(part)})
}

func (c *validationCollector) error(code, message, part string) {
	c.add("error", code, message, part)
}

func (c *validationCollector) warning(code, message, part string) {
	c.add("warning", code, message, part)
}

func (c *validationCollector) unsupported(code, message, part string) {
	if c.strict {
		c.error(code, message, part)
	} else {
		c.warning(code, message, part)
	}
}

func (c *validationCollector) sort() {
	sort.SliceStable(c.issues, func(i, j int) bool {
		left, right := c.issues[i], c.issues[j]
		if left.Severity != right.Severity {
			return left.Severity == "error"
		}
		if left.Part != right.Part {
			return left.Part < right.Part
		}
		return left.Code < right.Code
	})
}
