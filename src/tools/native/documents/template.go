package documents

import (
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"fmt"
	"os"
	"strings"
)

func resolveTemplatePath(roots []string, requested string, outputFormat documentFormat, maxBytes int64) (string, error) {
	templatePath, err := toolutil.ResolvePathInRoots(roots, requested, true)
	if err != nil {
		return "", fmt.Errorf("resolve template_path: %w", err)
	}
	info, err := os.Stat(templatePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("template_path is not a regular file")
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("template_path exceeds the %d byte safety limit", maxBytes)
	}
	templateFormat, err := detectDocumentFormat(templatePath)
	if err != nil {
		return "", fmt.Errorf("detect template format: %w", err)
	}
	if templateFormat != outputFormat {
		return "", fmt.Errorf("template_path format %s does not match output format %s", strings.ToUpper(string(templateFormat)), strings.ToUpper(string(outputFormat)))
	}
	return templatePath, nil
}

func createFromTemplate(ctx context.Context, templatePath string, format documentFormat, request documentCreateRequest, fontPath string) (authoredDocument, error) {
	if len(request.Blocks) > 0 || len(request.Sheets) > 0 || len(request.Slides) > 0 {
		return authoredDocument{}, fmt.Errorf("template_path preserves the source layout; use replacements, cell_updates or annotations instead of blocks, sheets or slides")
	}
	editRequest := documentEditRequest{
		Path: templatePath, FontPath: fontPath,
		Replacements: request.Replacements, CellUpdates: request.CellUpdates, Annotations: request.Annotations,
	}
	operationCount := len(editRequest.Replacements) + len(editRequest.CellUpdates) + len(editRequest.Annotations)
	if operationCount == 0 {
		data, err := os.ReadFile(templatePath)
		if err != nil {
			return authoredDocument{}, fmt.Errorf("read template: %w", err)
		}
		return authoredDocument{Data: data, Details: map[string]any{"template_preserved": true, "template_operation_count": 0}}, nil
	}
	var (
		result authoredDocument
		err    error
	)
	switch format {
	case formatDOCX, formatPPTX, formatXLSX:
		result, err = editOpenXML(ctx, templatePath, format, editRequest)
	case formatPDF:
		if len(editRequest.Replacements) > 0 || len(editRequest.CellUpdates) > 0 {
			return authoredDocument{}, fmt.Errorf("PDF templates only support annotations")
		}
		result, err = editPDF(ctx, templatePath, editRequest, fontPath)
	}
	if err != nil {
		return authoredDocument{}, err
	}
	if result.Details == nil {
		result.Details = map[string]any{}
	}
	result.Details["template_preserved"] = true
	result.Details["template_operation_count"] = operationCount
	return result, nil
}

func resolveAutomaticPDFFont(ctx context.Context, format documentFormat, explicitPath, text string) (string, error) {
	if format != formatPDF || !containsNonASCII(text) {
		return explicitPath, nil
	}
	required := requiredFontRunes(text)
	if explicitPath != "" {
		coverages, err := inspectFontCandidate(fontCandidate{Path: explicitPath, Scope: "sandbox"}, required)
		if err != nil {
			return "", fmt.Errorf("inspect font_path: %w", err)
		}
		for _, coverage := range coverages {
			if coverage.Complete {
				return explicitPath, nil
			}
		}
		return "", fmt.Errorf("font_path does not contain every glyph required by the PDF content")
	}
	match, ok := findSystemFontForText(ctx, text)
	if !ok {
		return "", fmt.Errorf("PDF content contains non-ASCII text and no installed TrueType font covers every required glyph; provide a Sandbox TTF font_path")
	}
	return match.path, nil
}

func annotationText(annotations []pdfAnnotation) string {
	var output strings.Builder
	for _, annotation := range annotations {
		if strings.EqualFold(strings.TrimSpace(annotation.Type), "text") {
			output.WriteString(annotation.Text)
			output.WriteRune('\n')
		}
	}
	return output.String()
}
