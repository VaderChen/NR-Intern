package documents

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	pdf "github.com/ledongthuc/pdf"
)

const (
	defaultRenderDPI       = 144
	maxRenderDPI           = 300
	maxRenderedPages       = 200
	maxRenderedOutputBytes = int64(512 * 1024 * 1024)
	maxRendererLogBytes    = 64 * 1024
)

type RenderTool struct {
	MaxDocumentBytes int64
	MaxOutputBytes   int
}

type renderRequest struct {
	Path       string
	OutputDir  string
	StartPage  int
	EndPage    int
	DPI        int
	Overwrite  bool
	EmitPDF    bool
	SourcePath string
	Format     documentFormat
}

type renderResult struct {
	Backend       string
	PageCount     int
	StartPage     int
	EndPage       int
	RenderedPaths []string
	PDFPath       string
	Warnings      []string
}

type rendererAvailability struct {
	OfficeConverter string `json:"office_converter,omitempty"`
	PDFRenderer     string `json:"pdf_renderer,omitempty"`
	OfficeReady     bool   `json:"office_ready"`
	PDFReady        bool   `json:"pdf_ready"`
}

func NewRenderTool(maxDocumentBytes int64, maxOutputBytes int) *RenderTool {
	return &RenderTool{MaxDocumentBytes: normalizedDocumentLimit(maxDocumentBytes), MaxOutputBytes: normalizedOutputLimit(maxOutputBytes)}
}

func (t *RenderTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "document_render",
		Label:              "渲染辦公文件",
		Version:            "1.0.0",
		Category:           "documents",
		Description:        "將 Sandbox 內的 PDF、DOCX、XLSX 或 PPTX 渲染為逐頁 PNG，以供視覺檢查。Office 文件使用可探測的 LibreOffice 後端轉成 PDF，再由 Poppler 渲染；不具備後端時會明確回報，不會以文字抽取冒充版面驗證。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"document-render", "visual-qa", "png", "pdf", "docx", "xlsx", "pptx", "workspace-sandbox", "atomic-write"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Sandbox 內的來源文件"},
				"output_dir": map[string]any{"type": "string", "description": "Sandbox 內的 PNG 輸出目錄；不存在時會建立"},
				"start_page": map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"end_page":   map[string]any{"type": "integer", "minimum": 1, "description": "包含此頁；省略時渲染到最後一頁，單次最多 200 頁"},
				"dpi":        map[string]any{"type": "integer", "minimum": 72, "maximum": maxRenderDPI, "default": defaultRenderDPI},
				"emit_pdf":   map[string]any{"type": "boolean", "default": false, "description": "Office 文件另外保留轉換後 PDF；PDF 來源則另存副本"},
				"overwrite":  map[string]any{"type": "boolean", "default": false},
			},
			"required": []string{"path", "output_dir"},
		},
	}
}

func (t *RenderTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	requestedPath := toolutil.String(invocation.Call.Arguments, "path")
	requestedOutput := toolutil.String(invocation.Call.Arguments, "output_dir")
	if requestedPath == "" || requestedOutput == "" {
		return documentFailure(invocation.Call, "path and output_dir are required"), nil
	}
	roots := invocation.SandboxRoots()
	sourcePath, err := toolutil.ResolvePathInRoots(roots, requestedPath, true)
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
	outputDir, err := resolveRenderOutputDirectory(roots, requestedOutput)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	format, err := detectDocumentFormat(sourcePath)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	request := renderRequest{
		Path: requestedPath, OutputDir: outputDir, SourcePath: sourcePath, Format: format,
		StartPage: toolutil.Int(invocation.Call.Arguments, "start_page", 1, 1, 1_000_000),
		EndPage:   toolutil.Int(invocation.Call.Arguments, "end_page", 0, 0, 1_000_000),
		DPI:       toolutil.Int(invocation.Call.Arguments, "dpi", defaultRenderDPI, 72, maxRenderDPI),
		Overwrite: toolutil.Bool(invocation.Call.Arguments, "overwrite", false),
		EmitPDF:   toolutil.Bool(invocation.Call.Arguments, "emit_pdf", false),
	}
	result, err := renderDocument(ctx, request)
	if err != nil {
		return documentFailure(invocation.Call, err.Error()), nil
	}
	displayPaths := make([]string, 0, len(result.RenderedPaths))
	for _, renderedPath := range result.RenderedPaths {
		displayPaths = append(displayPaths, toolutil.DisplayPathInRoots(roots, renderedPath))
	}
	details := map[string]any{
		"source_path":    toolutil.DisplayPathInRoots(roots, sourcePath),
		"format":         string(format),
		"backend":        result.Backend,
		"page_count":     result.PageCount,
		"start_page":     result.StartPage,
		"end_page":       result.EndPage,
		"dpi":            request.DPI,
		"rendered_files": displayPaths,
	}
	if result.PDFPath != "" {
		details["pdf_path"] = toolutil.DisplayPathInRoots(roots, result.PDFPath)
	}
	if len(result.Warnings) > 0 {
		details["warnings"] = result.Warnings
	}
	details = toolutil.ProducedFiles(details, result.RenderedPaths...)
	content := fmt.Sprintf("rendered %d page(s) from %s to %s", len(displayPaths), strings.ToUpper(string(format)), toolutil.DisplayPathInRoots(roots, outputDir))
	return domain.ToolExecution{ToolCallID: invocation.Call.ID, ToolName: invocation.Call.Name, Content: content, Details: details}, nil
}

func resolveRenderOutputDirectory(roots []string, requested string) (string, error) {
	outputDir, err := toolutil.ResolvePathInRoots(roots, requested, false)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(outputDir); statErr == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("output_dir is not a directory")
		}
		return outputDir, nil
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return "", fmt.Errorf("create output_dir: %w", err)
	}
	return outputDir, nil
}

func renderDocument(ctx context.Context, request renderRequest) (renderResult, error) {
	availability := discoverRenderers()
	if availability.PDFRenderer == "" {
		return renderResult{}, fmt.Errorf("document rendering requires Poppler pdftoppm; install it or bundle it under resources/bin")
	}
	staging, err := os.MkdirTemp(filepath.Dir(request.OutputDir), ".document-render-*")
	if err != nil {
		return renderResult{}, fmt.Errorf("create render staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	pdfPath := request.SourcePath
	backend := "poppler"
	if request.Format != formatPDF {
		if availability.OfficeConverter == "" {
			return renderResult{}, fmt.Errorf("rendering %s requires LibreOffice soffice; install it or bundle it under resources/bin", strings.ToUpper(string(request.Format)))
		}
		pdfPath, err = convertOfficeToPDF(ctx, availability.OfficeConverter, request.SourcePath, staging)
		if err != nil {
			return renderResult{}, err
		}
		backend = "libreoffice+poppler"
	}
	pageCount, err := pdfPageCount(ctx, pdfPath)
	if err != nil {
		return renderResult{}, err
	}
	startPage, endPage := request.StartPage, request.EndPage
	if startPage > pageCount {
		return renderResult{}, fmt.Errorf("start_page %d exceeds rendered page count %d", startPage, pageCount)
	}
	if endPage == 0 {
		endPage = pageCount
	}
	if endPage < startPage {
		return renderResult{}, fmt.Errorf("end_page must be greater than or equal to start_page")
	}
	if endPage > pageCount {
		endPage = pageCount
	}
	if endPage-startPage+1 > maxRenderedPages {
		return renderResult{}, fmt.Errorf("render range exceeds %d pages; narrow start_page and end_page", maxRenderedPages)
	}
	prefix := filepath.Join(staging, "page")
	arguments := []string{"-f", strconv.Itoa(startPage), "-l", strconv.Itoa(endPage), "-r", strconv.Itoa(request.DPI), "-png", pdfPath, prefix}
	if err := runRendererCommand(ctx, availability.PDFRenderer, arguments...); err != nil {
		return renderResult{}, fmt.Errorf("render PDF pages: %w", err)
	}
	rendered, err := filepath.Glob(prefix + "-*.png")
	if err != nil || len(rendered) == 0 {
		return renderResult{}, fmt.Errorf("renderer did not produce PNG pages")
	}
	sort.Slice(rendered, func(i, j int) bool { return renderedPageNumber(rendered[i]) < renderedPageNumber(rendered[j]) })
	if len(rendered) != endPage-startPage+1 {
		return renderResult{}, fmt.Errorf("renderer produced %d PNG files for a %d-page range", len(rendered), endPage-startPage+1)
	}

	destinations := make([]string, 0, len(rendered))
	totalBytes := int64(0)
	for _, temporaryPath := range rendered {
		pageNumber := renderedPageNumber(temporaryPath)
		label := "page"
		if request.Format == formatPPTX {
			label = "slide"
		}
		destination := filepath.Join(request.OutputDir, fmt.Sprintf("%s-%d.png", label, pageNumber))
		if !request.Overwrite {
			if _, statErr := os.Stat(destination); statErr == nil {
				return renderResult{}, fmt.Errorf("render output already exists: %s", filepath.Base(destination))
			} else if !os.IsNotExist(statErr) {
				return renderResult{}, statErr
			}
		}
		info, statErr := os.Stat(temporaryPath)
		if statErr != nil || !info.Mode().IsRegular() {
			return renderResult{}, fmt.Errorf("inspect rendered page: %w", statErr)
		}
		totalBytes += info.Size()
		if totalBytes > maxRenderedOutputBytes {
			return renderResult{}, fmt.Errorf("rendered output exceeds the %d byte safety limit", maxRenderedOutputBytes)
		}
		destinations = append(destinations, destination)
	}
	emittedPDFDestination := ""
	if request.EmitPDF {
		stem := strings.TrimSuffix(filepath.Base(request.SourcePath), filepath.Ext(request.SourcePath))
		baseName := stem + ".pdf"
		if request.Format == formatPDF {
			baseName = stem + "-rendered.pdf"
		}
		emittedPDFDestination = filepath.Join(request.OutputDir, baseName)
		if !request.Overwrite {
			if _, statErr := os.Stat(emittedPDFDestination); statErr == nil {
				return renderResult{}, fmt.Errorf("rendered PDF output already exists: %s", filepath.Base(emittedPDFDestination))
			} else if !os.IsNotExist(statErr) {
				return renderResult{}, statErr
			}
		}
	}
	for index, temporaryPath := range rendered {
		data, readErr := os.ReadFile(temporaryPath)
		if readErr != nil {
			return renderResult{}, fmt.Errorf("read rendered page: %w", readErr)
		}
		if writeErr := toolutil.AtomicWriteFile(destinations[index], data, 0o640, request.Overwrite); writeErr != nil {
			return renderResult{}, fmt.Errorf("write rendered page: %w", writeErr)
		}
	}

	emittedPDF := ""
	if request.EmitPDF {
		data, readErr := os.ReadFile(pdfPath)
		if readErr != nil {
			return renderResult{}, fmt.Errorf("read rendered PDF: %w", readErr)
		}
		if writeErr := toolutil.AtomicWriteFile(emittedPDFDestination, data, 0o640, request.Overwrite); writeErr != nil {
			return renderResult{}, fmt.Errorf("write rendered PDF: %w", writeErr)
		}
		emittedPDF = emittedPDFDestination
	}
	return renderResult{Backend: backend, PageCount: pageCount, StartPage: startPage, EndPage: endPage, RenderedPaths: destinations, PDFPath: emittedPDF}, nil
}

func discoverRenderers() rendererAvailability {
	office := findRendererExecutable("NR_INTERN_SOFFICE", []string{"soffice", "libreoffice"}, officeRendererPaths())
	pdfRenderer := findRendererExecutable("NR_INTERN_PDFTOPPM", []string{"pdftoppm"}, pdfRendererPaths())
	return rendererAvailability{
		OfficeConverter: office,
		PDFRenderer:     pdfRenderer,
		OfficeReady:     office != "",
		PDFReady:        pdfRenderer != "",
	}
}

func findRendererExecutable(environmentName string, names, candidates []string) string {
	if configured := strings.TrimSpace(os.Getenv(environmentName)); configured != "" {
		if filepath.IsAbs(configured) && executableFile(configured) {
			return configured
		}
	}
	for _, name := range names {
		if found, err := exec.LookPath(name); err == nil && executableFile(found) {
			return found
		}
	}
	for _, candidate := range candidates {
		if executableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func officeRendererPaths() []string {
	paths := bundledRendererPaths("soffice")
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths, "/Applications/LibreOffice.app/Contents/MacOS/soffice")
	case "windows":
		paths = append(paths,
			filepath.Join(os.Getenv("ProgramFiles"), "LibreOffice", "program", "soffice.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "LibreOffice", "program", "soffice.exe"),
		)
	default:
		paths = append(paths, "/usr/bin/soffice", "/usr/local/bin/soffice", "/snap/bin/libreoffice")
	}
	return paths
}

func pdfRendererPaths() []string {
	name := "pdftoppm"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return bundledRendererPaths(name)
}

func bundledRendererPaths(name string) []string {
	executable, _ := os.Executable()
	if executable == "" {
		return nil
	}
	directory := filepath.Dir(executable)
	return []string{
		filepath.Join(directory, "resources", "bin", name),
		filepath.Join(directory, "..", "Resources", "bin", name),
		filepath.Join(directory, "bin", name),
	}
}

func executableFile(candidate string) bool {
	if strings.TrimSpace(candidate) == "" {
		return false
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}

func convertOfficeToPDF(ctx context.Context, soffice, sourcePath, outputDir string) (string, error) {
	return convertOfficeToFormat(ctx, soffice, sourcePath, outputDir, "pdf")
}

func pdfPageCount(ctx context.Context, pdfPath string) (count int, err error) {
	defer recoverPDF(&err)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	file, reader, err := pdf.Open(pdfPath)
	if err != nil {
		return 0, fmt.Errorf("open rendered PDF: %w", err)
	}
	defer file.Close()
	count = reader.NumPage()
	if count <= 0 {
		return 0, fmt.Errorf("rendered PDF does not contain any pages")
	}
	return count, nil
}

func runRendererCommand(ctx context.Context, executable string, arguments ...string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = rendererEnvironment(os.Environ())
	var output cappedWriter
	output.Limit = maxRendererLogBytes
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

func rendererEnvironment(base []string) []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true,
		"SYSTEMROOT": true, "COMSPEC": true, "PATHEXT": true, "USERPROFILE": true,
	}
	result := make([]string, 0, len(base))
	for _, item := range base {
		separator := strings.IndexByte(item, '=')
		if separator <= 0 {
			continue
		}
		key := strings.ToUpper(item[:separator])
		if allowed[key] || strings.HasPrefix(key, "LC_") {
			result = append(result, item)
		}
	}
	return result
}

type cappedWriter struct {
	Buffer bytes.Buffer
	Limit  int
}

func (w *cappedWriter) Write(data []byte) (int, error) {
	written := len(data)
	if w.Limit <= 0 || w.Buffer.Len() >= w.Limit {
		return written, nil
	}
	remaining := w.Limit - w.Buffer.Len()
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = w.Buffer.Write(data)
	return written, nil
}

func (w *cappedWriter) String() string { return w.Buffer.String() }

func renderedPageNumber(filePath string) int {
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	separator := strings.LastIndex(base, "-")
	if separator < 0 {
		return 0
	}
	value, _ := strconv.Atoi(base[separator+1:])
	return value
}

var _ io.Writer = (*cappedWriter)(nil)
