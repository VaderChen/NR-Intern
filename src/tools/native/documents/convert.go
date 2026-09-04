package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type ConvertTool struct {
	MaxDocumentBytes int64
}

func NewConvertTool(maxDocumentBytes int64) *ConvertTool {
	return &ConvertTool{MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes)}
}

func (t *ConvertTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "document_convert",
		Label:              "轉換辦公文件",
		Version:            "1.0.0",
		Category:           "documents",
		Description:        "透過固定探索的 LibreOffice 後端，將 Word（DOCX）、Excel（XLSX）、PowerPoint（PPTX）與相容的舊式 Office／OpenDocument 文件轉成 PDF，或將 DOC、XLS、PPT、ODT、ODS、ODP、RTF 遷移到對應 Open XML 格式。來源與輸出都必須位於 Sandbox。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"document-convert", "office-to-pdf", "legacy-office-migration", "docx", "xlsx", "pptx", "pdf", "workspace-sandbox", "atomic-write"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "description": "Sandbox 內的來源文件"},
				"output_path":   map[string]any{"type": "string", "description": "Sandbox 內的輸出文件；副檔名決定轉換格式"},
				"overwrite":     map[string]any{"type": "boolean", "default": false},
				"create_parent": map[string]any{"type": "boolean", "default": false},
			},
			"required": []string{"path", "output_path"},
		},
	}
}

func (t *ConvertTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if err := ctx.Err(); err != nil {
		return domain.ToolExecution{}, err
	}
	roots := invocation.SandboxRoots()
	sourcePath, err := toolutil.ResolvePathInRoots(roots, toolutil.String(invocation.Call.Arguments, "path"), true)
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
	outputPath, err := resolveAuthoringOutput(roots, toolutil.String(invocation.Call.Arguments, "output_path"), toolutil.Bool(invocation.Call.Arguments, "create_parent", false))
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if filepath.Clean(sourcePath) == filepath.Clean(outputPath) {
		return documentFailure(invocation.Call, "output_path must differ from path"), nil
	}
	sourceExtension := strings.ToLower(filepath.Ext(sourcePath))
	targetExtension := strings.ToLower(filepath.Ext(outputPath))
	if err := validateOfficeConversion(sourceExtension, targetExtension); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	availability := discoverRenderers()
	if availability.OfficeConverter == "" {
		return documentFailure(invocation.Call, "document conversion requires LibreOffice soffice; install it or bundle it under resources/bin"), nil
	}
	staging, err := os.MkdirTemp(filepath.Dir(outputPath), ".document-convert-*")
	if err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("create conversion staging directory: %v", err)), nil
	}
	defer os.RemoveAll(staging)
	convertedPath, err := convertOfficeToFormat(ctx, availability.OfficeConverter, sourcePath, staging, strings.TrimPrefix(targetExtension, "."))
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	converted, err := readRegularFile(convertedPath, t.MaxDocumentBytes)
	if err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("read converted document: %v", err)), nil
	}
	if _, err := detectDocumentFormat(convertedPath); err != nil {
		return documentFailure(invocation.Call, fmt.Sprintf("validate converted document: %v", err)), nil
	}
	overwrite := toolutil.Bool(invocation.Call.Arguments, "overwrite", false)
	if err := toolutil.AtomicWriteFile(outputPath, converted, info.Mode().Perm(), overwrite); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	displayPath := toolutil.DisplayPathInRoots(roots, outputPath)
	return domain.ToolExecution{
		ToolCallID: invocation.Call.ID,
		ToolName:   invocation.Call.Name,
		Content:    fmt.Sprintf("converted document to %s at %s", strings.ToUpper(strings.TrimPrefix(targetExtension, ".")), displayPath),
		Details: toolutil.ProducedFiles(map[string]any{
			"source_path": toolutil.DisplayPathInRoots(roots, sourcePath),
			"path":        displayPath,
			"source_type": strings.TrimPrefix(sourceExtension, "."),
			"format":      strings.TrimPrefix(targetExtension, "."),
			"backend":     filepath.Base(availability.OfficeConverter),
			"bytes":       len(converted),
			"sha256":      fmt.Sprintf("%x", sha256.Sum256(converted)),
		}, outputPath),
	}, nil
}

func validateOfficeConversion(sourceExtension, targetExtension string) error {
	groups := map[string]map[string]bool{
		"word":         {".doc": true, ".docx": true, ".odt": true, ".rtf": true},
		"spreadsheet":  {".xls": true, ".xlsx": true, ".ods": true},
		"presentation": {".ppt": true, ".pptx": true, ".odp": true},
	}
	targets := map[string]string{"word": ".docx", "spreadsheet": ".xlsx", "presentation": ".pptx"}
	for group, extensions := range groups {
		if !extensions[sourceExtension] {
			continue
		}
		if targetExtension == ".pdf" || targetExtension == targets[group] && sourceExtension != targetExtension {
			return nil
		}
		return fmt.Errorf("%s documents may be converted to PDF or %s", group, strings.ToUpper(strings.TrimPrefix(targets[group], ".")))
	}
	return fmt.Errorf("unsupported source extension %q; supported sources are DOC/DOCX/ODT/RTF, XLS/XLSX/ODS and PPT/PPTX/ODP", sourceExtension)
}

func convertOfficeToFormat(ctx context.Context, soffice, sourcePath, outputDir, targetFormat string) (string, error) {
	profile, err := os.MkdirTemp(outputDir, "libreoffice-profile-*")
	if err != nil {
		return "", fmt.Errorf("create LibreOffice profile: %w", err)
	}
	profileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(profile)}).String()
	arguments := []string{"--headless", "--nologo", "--nodefault", "--nofirststartwizard", "-env:UserInstallation=" + profileURL, "--convert-to", targetFormat, "--outdir", outputDir, sourcePath}
	if err := runRendererCommand(ctx, soffice, arguments...); err != nil {
		return "", fmt.Errorf("convert Office document to %s: %w", strings.ToUpper(targetFormat), err)
	}
	expected := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))+"."+targetFormat)
	if info, statErr := os.Stat(expected); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return expected, nil
	}
	files, _ := filepath.Glob(filepath.Join(outputDir, "*."+targetFormat))
	for _, candidate := range files {
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("LibreOffice did not produce a %s document", strings.ToUpper(targetFormat))
}
