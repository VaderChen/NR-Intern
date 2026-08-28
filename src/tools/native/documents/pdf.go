package documents

import (
	"context"
	"fmt"
	"strings"

	pdf "github.com/ledongthuc/pdf"
)

func inspectPDF(ctx context.Context, filePath string, base documentInspection) (result documentInspection, err error) {
	defer recoverPDF(&err)
	if err := ctx.Err(); err != nil {
		return documentInspection{}, err
	}
	file, reader, err := pdf.Open(filePath)
	if err != nil {
		return documentInspection{}, fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	base.PageCount = reader.NumPage()
	base.Metadata = map[string]string{}
	info := reader.Trailer().Key("Info")
	for _, key := range []string{"Title", "Author", "Subject", "Keywords", "Creator", "Producer", "CreationDate", "ModDate"} {
		if value := strings.TrimSpace(info.Key(key).Text()); value != "" {
			base.Metadata[key] = value
		}
	}
	if len(base.Metadata) == 0 {
		base.Metadata = nil
	}
	return base, nil
}

func readPDF(ctx context.Context, filePath string, options readOptions) (result documentReadResult, err error) {
	defer recoverPDF(&err)
	file, reader, err := pdf.Open(filePath)
	if err != nil {
		return documentReadResult{}, fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	pageCount := reader.NumPage()
	if pageCount <= 0 {
		return documentReadResult{}, fmt.Errorf("PDF does not contain any pages")
	}
	startPage := options.StartPage
	endPage := options.EndPage
	if endPage == 0 {
		endPage = startPage
	}
	if startPage > pageCount {
		return documentReadResult{}, fmt.Errorf("start_page %d exceeds PDF page count %d", startPage, pageCount)
	}
	if endPage < startPage {
		return documentReadResult{}, fmt.Errorf("end_page must be greater than or equal to start_page")
	}
	if endPage > pageCount {
		endPage = pageCount
	}
	fonts := map[string]*pdf.Font{}
	var output strings.Builder
	extractedCharacters := 0
	for pageNumber := startPage; pageNumber <= endPage; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return documentReadResult{}, err
		}
		page := reader.Page(pageNumber)
		if page.V.IsNull() {
			continue
		}
		for _, name := range page.Fonts() {
			if _, exists := fonts[name]; !exists {
				font := page.Font(name)
				fonts[name] = &font
			}
		}
		text, textErr := page.GetPlainText(fonts)
		if textErr != nil {
			return documentReadResult{}, fmt.Errorf("extract PDF page %d: %w", pageNumber, textErr)
		}
		text = strings.TrimSpace(text)
		output.WriteString(fmt.Sprintf("[Page %d/%d]\n", pageNumber, pageCount))
		if text == "" {
			output.WriteString("[no extractable text]\n")
		} else {
			output.WriteString(text)
			output.WriteByte('\n')
			extractedCharacters += len([]rune(text))
		}
		if pageNumber < endPage {
			output.WriteByte('\n')
		}
	}
	warnings := []string{}
	if extractedCharacters == 0 {
		warnings = append(warnings, "selected pages contain no extractable text; the PDF may be scanned and require OCR")
	}
	return documentReadResult{
		Format:   string(formatPDF),
		Content:  output.String(),
		Warnings: warnings,
		Details: map[string]any{
			"page_count":           pageCount,
			"start_page":           startPage,
			"end_page":             endPage,
			"extracted_characters": extractedCharacters,
		},
	}, nil
}

func recoverPDF(err *error) {
	if recovered := recover(); recovered != nil {
		*err = fmt.Errorf("parse PDF: %v", recovered)
	}
}
