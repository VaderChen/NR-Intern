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
	"path/filepath"
	"strings"

	"github.com/phpdave11/gofpdf"
	"github.com/phpdave11/gofpdf/contrib/gofpdi"
)

const (
	maxPDFCompositionPages = 500
	maxPDFSourceBytes      = int64(512 * 1024 * 1024)
	maxPDFSplitOutputBytes = int64(512 * 1024 * 1024)
)

type PDFPagesTool struct {
	MaxInputBytes    int
	MaxDocumentBytes int64
}

type pdfPagesRequest struct {
	Operation    string          `json:"operation"`
	Sources      []pdfPageSource `json:"sources"`
	OutputPath   string          `json:"output_path"`
	OutputDir    string          `json:"output_dir"`
	ChunkSize    int             `json:"chunk_size"`
	Overwrite    bool            `json:"overwrite"`
	CreateParent bool            `json:"create_parent"`
}

type pdfPageSource struct {
	Path  string `json:"path"`
	Pages []int  `json:"pages"`
}

type resolvedPDFPageSource struct {
	Path      string
	Display   string
	PageCount int
	Pages     []int
}

type pdfPageReference struct {
	Path string
	Page int
}

func NewPDFPagesTool(maxInputBytes int, maxDocumentBytes int64) *PDFPagesTool {
	return &PDFPagesTool{MaxInputBytes: normalizedAuthoringInputLimit(maxInputBytes), MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes)}
}

func (t *PDFPagesTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "pdf_pages",
		Label:              "整理 PDF 頁面",
		Version:            "1.0.0",
		Category:           "documents",
		Description:        "依 sources 與 pages 的順序合併、擷取或重排 PDF；也可把單一 PDF 依 chunk_size 拆成多份。來源與輸出都限制在 Sandbox，預設不覆寫。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"pdf-merge", "pdf-extract", "pdf-reorder", "pdf-split", "workspace-sandbox", "atomic-write"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{"type": "string", "enum": []string{"compose", "split"}},
				"sources": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{
					"type": "object", "required": []string{"path"}, "properties": map[string]any{
						"path":  map[string]any{"type": "string"},
						"pages": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "依指定順序取頁；省略時取全部頁面"},
					},
				}},
				"output_path":   map[string]any{"type": "string", "description": "compose 的輸出 PDF"},
				"output_dir":    map[string]any{"type": "string", "description": "split 的輸出目錄"},
				"chunk_size":    map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
				"overwrite":     map[string]any{"type": "boolean", "default": false},
				"create_parent": map[string]any{"type": "boolean", "default": false},
			},
			"required": []string{"operation", "sources"},
		},
	}
}

func (t *PDFPagesTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	var request pdfPagesRequest
	if err := decodeAuthoringArguments(invocation.Call.Arguments, t.MaxInputBytes, &request); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	operation := strings.ToLower(strings.TrimSpace(request.Operation))
	if operation != "compose" && operation != "split" {
		return documentFailure(invocation.Call, "operation must be compose or split"), nil
	}
	sources, references, err := t.resolveSources(ctx, invocation.SandboxRoots(), request.Sources)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if operation == "compose" {
		return t.executeCompose(ctx, invocation, request, sources, references)
	}
	if len(sources) != 1 {
		return documentFailure(invocation.Call, "split requires exactly one source"), nil
	}
	return t.executeSplit(ctx, invocation, request, sources[0], references)
}

func (t *PDFPagesTool) resolveSources(ctx context.Context, roots []string, inputs []pdfPageSource) ([]resolvedPDFPageSource, []pdfPageReference, error) {
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("sources must contain at least one PDF")
	}
	resolved := make([]resolvedPDFPageSource, 0, len(inputs))
	references := []pdfPageReference{}
	totalBytes := int64(0)
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		path, err := toolutil.ResolvePathInRoots(roots, input.Path, true)
		if err != nil {
			return nil, nil, fmt.Errorf("sources[%d].path: %w", index, err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("sources[%d].path is not a regular file", index)
		}
		if info.Size() > t.MaxDocumentBytes {
			return nil, nil, fmt.Errorf("sources[%d] exceeds the %d byte safety limit", index, t.MaxDocumentBytes)
		}
		totalBytes += info.Size()
		if totalBytes > maxPDFSourceBytes {
			return nil, nil, fmt.Errorf("combined PDF sources exceed the %d byte safety limit", maxPDFSourceBytes)
		}
		format, err := detectDocumentFormat(path)
		if err != nil || format != formatPDF {
			return nil, nil, fmt.Errorf("sources[%d] is not a supported PDF", index)
		}
		pageCount, err := pdfDocumentPageCount(path)
		if err != nil {
			return nil, nil, fmt.Errorf("sources[%d]: %w", index, err)
		}
		pages := append([]int(nil), input.Pages...)
		if len(pages) == 0 {
			pages = make([]int, pageCount)
			for page := 1; page <= pageCount; page++ {
				pages[page-1] = page
			}
		}
		for pageIndex, page := range pages {
			if page < 1 || page > pageCount {
				return nil, nil, fmt.Errorf("sources[%d].pages[%d] exceeds page count %d", index, pageIndex, pageCount)
			}
			references = append(references, pdfPageReference{Path: path, Page: page})
			if len(references) > maxPDFCompositionPages {
				return nil, nil, fmt.Errorf("PDF operation exceeds the %d page safety limit", maxPDFCompositionPages)
			}
		}
		resolved = append(resolved, resolvedPDFPageSource{Path: path, Display: toolutil.DisplayPathInRoots(roots, path), PageCount: pageCount, Pages: pages})
	}
	return resolved, references, nil
}

func (t *PDFPagesTool) executeCompose(ctx context.Context, invocation tools.Invocation, request pdfPagesRequest, sources []resolvedPDFPageSource, references []pdfPageReference) (domain.ToolExecution, error) {
	outputPath, err := resolveAuthoringOutput(invocation.SandboxRoots(), request.OutputPath, request.CreateParent)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if !strings.EqualFold(filepath.Ext(outputPath), ".pdf") {
		return documentFailure(invocation.Call, "output_path must use the .pdf extension"), nil
	}
	for _, source := range sources {
		if filepath.Clean(source.Path) == filepath.Clean(outputPath) {
			return documentFailure(invocation.Call, "output_path must differ from every source path"), nil
		}
	}
	data, err := composePDFPages(ctx, references)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if int64(len(data)) > t.MaxDocumentBytes {
		return documentFailure(invocation.Call, fmt.Sprintf("composed PDF exceeds the %d byte safety limit", t.MaxDocumentBytes)), nil
	}
	if err := toolutil.AtomicWriteFile(outputPath, data, 0o640, request.Overwrite); err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	inputPaths := make([]string, len(sources))
	for index, source := range sources {
		inputPaths[index] = source.Display
	}
	displayPath := toolutil.DisplayPathInRoots(invocation.SandboxRoots(), outputPath)
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: fmt.Sprintf("composed %d PDF page(s) at %s", len(references), displayPath), Details: map[string]any{
		"path": displayPath, "source_paths": inputPaths, "page_count": len(references), "bytes": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data)),
	}}, nil
}

func (t *PDFPagesTool) executeSplit(ctx context.Context, invocation tools.Invocation, request pdfPagesRequest, source resolvedPDFPageSource, references []pdfPageReference) (domain.ToolExecution, error) {
	if strings.TrimSpace(request.OutputDir) == "" {
		return documentFailure(invocation.Call, "output_dir is required for split"), nil
	}
	outputDir, err := toolutil.ResolvePathInRoots(invocation.SandboxRoots(), request.OutputDir, false)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	if info, statErr := os.Stat(outputDir); statErr == nil && !info.IsDir() {
		return documentFailure(invocation.Call, "output_dir is not a directory"), nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return documentFailure(invocation.Call, statErr.Error()), nil
	} else if os.IsNotExist(statErr) {
		if !request.CreateParent {
			return documentFailure(invocation.Call, "output_dir does not exist; set create_parent=true to create it"), nil
		}
		if mkdirErr := os.MkdirAll(outputDir, 0o750); mkdirErr != nil {
			return documentFailure(invocation.Call, mkdirErr.Error()), nil
		}
	}
	chunkSize := request.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 1
	}
	if chunkSize > 100 {
		return documentFailure(invocation.Call, "chunk_size must not exceed 100"), nil
	}
	type splitArtifact struct {
		Path string
		Data []byte
	}
	artifacts := []splitArtifact{}
	totalBytes := int64(0)
	for start := 0; start < len(references); start += chunkSize {
		end := start + chunkSize
		if end > len(references) {
			end = len(references)
		}
		name := fmt.Sprintf("part-%d-pages-%d-%d.pdf", len(artifacts)+1, source.Pages[start], source.Pages[end-1])
		outputPath := filepath.Join(outputDir, name)
		if !request.Overwrite {
			if _, statErr := os.Stat(outputPath); statErr == nil {
				return documentFailure(invocation.Call, fmt.Sprintf("split output already exists: %s", name)), nil
			} else if !os.IsNotExist(statErr) {
				return documentFailure(invocation.Call, statErr.Error()), nil
			}
		}
		data, composeErr := composePDFPages(ctx, references[start:end])
		if composeErr != nil {
			return documentFailure(invocation.Call, composeErr.Error()), nil
		}
		totalBytes += int64(len(data))
		if totalBytes > maxPDFSplitOutputBytes {
			return documentFailure(invocation.Call, fmt.Sprintf("split PDF output exceeds the %d byte safety limit", maxPDFSplitOutputBytes)), nil
		}
		artifacts = append(artifacts, splitArtifact{Path: outputPath, Data: data})
	}
	outputPaths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if writeErr := toolutil.AtomicWriteFile(artifact.Path, artifact.Data, 0o640, request.Overwrite); writeErr != nil {
			return documentFailure(invocation.Call, writeErr.Error()), nil
		}
		outputPaths = append(outputPaths, toolutil.DisplayPathInRoots(invocation.SandboxRoots(), artifact.Path))
	}
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: fmt.Sprintf("split %d PDF page(s) into %d file(s)", len(references), len(outputPaths)), Details: map[string]any{
		"source_path": source.Display, "output_files": outputPaths, "page_count": len(references), "chunk_size": chunkSize, "bytes": totalBytes,
	}}, nil
}

func composePDFPages(ctx context.Context, references []pdfPageReference) (data []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("compose PDF pages: %v", recovered)
		}
	}()
	if len(references) == 0 {
		return nil, fmt.Errorf("PDF operation selected no pages")
	}
	document := gofpdf.NewCustom(&gofpdf.InitType{OrientationStr: "P", UnitStr: "pt", Size: gofpdf.SizeType{Wd: pdfLetterWidth, Ht: pdfLetterHeight}})
	document.SetAutoPageBreak(false, 0)
	document.SetCreator("NR-Intern", true)
	importer := gofpdi.NewImporter()
	for index, reference := range references {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		templateID := importer.ImportPage(document, reference.Path, reference.Page, "/MediaBox")
		if document.Err() {
			return nil, fmt.Errorf("import PDF page %d: %w", index+1, document.Error())
		}
		width, height := pdfImportedPageSize(importer.GetPageSizes(), reference.Page)
		orientation := "P"
		if width > height {
			orientation = "L"
		}
		document.AddPageFormat(orientation, gofpdf.SizeType{Wd: width, Ht: height})
		importer.UseImportedTemplate(document, templateID, 0, 0, width, height)
	}
	var output bytes.Buffer
	if err := document.Output(&output); err != nil {
		return nil, fmt.Errorf("write composed PDF: %w", err)
	}
	return output.Bytes(), nil
}
