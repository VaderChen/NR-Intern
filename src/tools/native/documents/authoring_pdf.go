package documents

import (
	"bytes"
	"context"
	"fmt"
	"github.com/ledongthuc/pdf"
	"github.com/phpdave11/gofpdf"
	"github.com/phpdave11/gofpdf/contrib/gofpdi"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	pdfLetterWidth  = 612.0
	pdfLetterHeight = 792.0
	pdfMargin       = 72.0
)

func createPDF(ctx context.Context, request documentCreateRequest, fontPath string) (authoredDocument, error) {
	if strings.TrimSpace(request.Title) == "" && len(request.Blocks) == 0 {
		return authoredDocument{}, fmt.Errorf("PDF requires title or at least one content block")
	}
	if fontPath == "" && requestNeedsUnicodeFont(request) {
		return authoredDocument{}, fmt.Errorf("PDF content contains non-ASCII text; provide a Sandbox TTF font_path so glyphs can be embedded")
	}
	document := gofpdf.NewCustom(&gofpdf.InitType{OrientationStr: "P", UnitStr: "pt", Size: gofpdf.SizeType{Wd: pdfLetterWidth, Ht: pdfLetterHeight}})
	document.SetMargins(pdfMargin, pdfMargin, pdfMargin)
	document.SetAutoPageBreak(true, pdfMargin)
	document.SetTitle(request.Title, true)
	document.SetSubject(request.Subject, true)
	document.SetAuthor(request.Author, true)
	document.SetCreator("NR-Intern", true)
	fontFamily, err := configurePDFFont(document, fontPath)
	if err != nil {
		return authoredDocument{}, err
	}
	document.AddPage()
	if strings.TrimSpace(request.Title) != "" {
		document.SetTextColor(23, 43, 77)
		document.SetFont(fontFamily, "", 22)
		document.MultiCell(0, 28, request.Title, "", "L", false)
		document.Ln(10)
	}
	blocksWritten := 0
	numberedItem := 0
	for index, block := range request.Blocks {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "heading":
			level := block.Level
			if level < 1 || level > 3 {
				level = 1
			}
			size := map[int]float64{1: 18, 2: 15, 3: 13}[level]
			document.Ln(8)
			document.SetTextColor(29, 78, 216)
			document.SetFont(fontFamily, "", size)
			document.MultiCell(0, size*1.35, block.Text, "", "L", false)
			document.Ln(3)
		case "paragraph":
			document.SetTextColor(31, 41, 51)
			document.SetFont(fontFamily, "", 11)
			document.MultiCell(0, 16, block.Text, "", "L", false)
			document.Ln(5)
		case "bullet", "numbered":
			prefix := "- "
			if strings.EqualFold(block.Type, "numbered") {
				numberedItem++
				prefix = strconv.Itoa(numberedItem) + ". "
			} else if fontPath != "" {
				prefix = "• "
			}
			document.SetTextColor(31, 41, 51)
			document.SetFont(fontFamily, "", 11)
			document.SetX(pdfMargin + float64(maxInt(block.Level-1, 0))*18)
			document.MultiCell(0, 16, prefix+block.Text, "", "L", false)
			document.Ln(2)
		case "table":
			if len(block.Rows) == 0 {
				return authoredDocument{}, fmt.Errorf("blocks[%d] table requires at least one row", index)
			}
			if err := writePDFTable(document, fontFamily, block.Rows); err != nil {
				return authoredDocument{}, fmt.Errorf("blocks[%d]: %w", index, err)
			}
			document.Ln(8)
		case "page_break":
			document.AddPage()
		default:
			return authoredDocument{}, fmt.Errorf("blocks[%d].type must be heading, paragraph, bullet, numbered, table or page_break", index)
		}
		blocksWritten++
	}
	var output bytes.Buffer
	if err := document.Output(&output); err != nil {
		return authoredDocument{}, fmt.Errorf("create PDF: %w", err)
	}
	details := map[string]any{"block_count": blocksWritten, "page_count": document.PageCount(), "font_embedded": fontPath != ""}
	if fontPath != "" {
		details["font_file"] = filepath.Base(fontPath)
	}
	return authoredDocument{Data: output.Bytes(), Details: details}, nil
}

func writePDFTable(document *gofpdf.Fpdf, fontFamily string, rows [][]string) error {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return fmt.Errorf("table rows require at least one cell")
	}
	pageWidth, pageHeight := document.GetPageSize()
	cellWidth := (pageWidth - 2*pdfMargin) / float64(columns)
	lineHeight := 14.0
	for rowIndex, row := range rows {
		document.SetFont(fontFamily, "", 10)
		maxLines := 1
		for column := 0; column < columns; column++ {
			value := ""
			if column < len(row) {
				value = row[column]
			}
			lines := document.SplitText(value, cellWidth-10)
			if len(lines) > maxLines {
				maxLines = len(lines)
			}
		}
		rowHeight := float64(maxLines)*lineHeight + 8
		if document.GetY()+rowHeight > pageHeight-pdfMargin {
			document.AddPage()
		}
		startX, startY := pdfMargin, document.GetY()
		for column := 0; column < columns; column++ {
			value := ""
			if column < len(row) {
				value = row[column]
			}
			x := startX + float64(column)*cellWidth
			if rowIndex == 0 {
				document.SetFillColor(220, 230, 241)
				document.Rect(x, startY, cellWidth, rowHeight, "F")
			}
			document.SetDrawColor(184, 194, 204)
			document.Rect(x, startY, cellWidth, rowHeight, "D")
			document.SetXY(x+5, startY+4)
			document.MultiCell(cellWidth-10, lineHeight, value, "", "L", false)
		}
		document.SetXY(pdfMargin, startY+rowHeight)
	}
	return nil
}

func editPDF(ctx context.Context, sourcePath string, request documentEditRequest, fontPath string) (result authoredDocument, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("edit PDF: %v", recovered)
		}
	}()
	if len(request.Annotations) == 0 {
		return authoredDocument{}, fmt.Errorf("PDF editing requires at least one annotation")
	}
	pageCount, err := pdfDocumentPageCount(sourcePath)
	if err != nil {
		return authoredDocument{}, err
	}
	annotations := map[int][]pdfAnnotation{}
	needsFont := false
	for index, annotation := range request.Annotations {
		if annotation.Page < 1 || annotation.Page > pageCount {
			return authoredDocument{}, fmt.Errorf("annotations[%d].page exceeds PDF page count %d", index, pageCount)
		}
		typeName := strings.ToLower(strings.TrimSpace(annotation.Type))
		if typeName != "text" && typeName != "line" && typeName != "rectangle" {
			return authoredDocument{}, fmt.Errorf("annotations[%d].type must be text, line or rectangle", index)
		}
		if typeName == "text" {
			if strings.TrimSpace(annotation.Text) == "" {
				return authoredDocument{}, fmt.Errorf("annotations[%d].text is required", index)
			}
			needsFont = needsFont || containsNonASCII(annotation.Text)
		}
		annotations[annotation.Page] = append(annotations[annotation.Page], annotation)
	}
	if needsFont && fontPath == "" {
		return authoredDocument{}, fmt.Errorf("PDF annotation contains non-ASCII text; provide a Sandbox TTF font_path so glyphs can be embedded")
	}
	document := gofpdf.NewCustom(&gofpdf.InitType{OrientationStr: "P", UnitStr: "pt", Size: gofpdf.SizeType{Wd: pdfLetterWidth, Ht: pdfLetterHeight}})
	document.SetAutoPageBreak(false, 0)
	document.SetCreator("NR-Intern", true)
	fontFamily, err := configurePDFFont(document, fontPath)
	if err != nil {
		return authoredDocument{}, err
	}
	importer := gofpdi.NewImporter()
	for pageNumber := 1; pageNumber <= pageCount; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		templateID := importer.ImportPage(document, sourcePath, pageNumber, "/MediaBox")
		if document.Err() {
			return authoredDocument{}, fmt.Errorf("import PDF page %d: %w", pageNumber, document.Error())
		}
		width, height := pdfImportedPageSize(importer.GetPageSizes(), pageNumber)
		orientation := "P"
		if width > height {
			orientation = "L"
		}
		document.AddPageFormat(orientation, gofpdf.SizeType{Wd: width, Ht: height})
		importer.UseImportedTemplate(document, templateID, 0, 0, width, height)
		for _, annotation := range annotations[pageNumber] {
			if err := drawPDFAnnotation(document, fontFamily, annotation); err != nil {
				return authoredDocument{}, err
			}
		}
	}
	var output bytes.Buffer
	if err := document.Output(&output); err != nil {
		return authoredDocument{}, fmt.Errorf("write edited PDF: %w", err)
	}
	details := map[string]any{"page_count": pageCount, "annotation_count": len(request.Annotations), "font_embedded": fontPath != ""}
	if fontPath != "" {
		details["font_file"] = filepath.Base(fontPath)
	}
	return authoredDocument{Data: output.Bytes(), Details: details}, nil
}

func pdfDocumentPageCount(sourcePath string) (count int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("parse PDF: %v", recovered)
		}
	}()
	file, reader, err := pdf.Open(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	if reader.NumPage() < 1 {
		return 0, fmt.Errorf("PDF does not contain any pages")
	}
	return reader.NumPage(), nil
}

func pdfImportedPageSize(sizes map[int]map[string]map[string]float64, page int) (float64, float64) {
	if boxes := sizes[page]; boxes != nil {
		if mediaBox := boxes["/MediaBox"]; mediaBox != nil && mediaBox["w"] > 0 && mediaBox["h"] > 0 {
			return mediaBox["w"], mediaBox["h"]
		}
	}
	return pdfLetterWidth, pdfLetterHeight
}

func drawPDFAnnotation(document *gofpdf.Fpdf, fontFamily string, annotation pdfAnnotation) error {
	r, g, b, err := parseHexColor(annotation.Color)
	if err != nil {
		return err
	}
	lineWidth := annotation.LineWidth
	if lineWidth <= 0 {
		lineWidth = 2
	}
	document.SetLineWidth(lineWidth)
	document.SetDrawColor(r, g, b)
	switch strings.ToLower(strings.TrimSpace(annotation.Type)) {
	case "text":
		fontSize := annotation.FontSize
		if fontSize <= 0 {
			fontSize = 14
		}
		width := annotation.Width
		if width <= 0 {
			width = 240
		}
		document.SetTextColor(r, g, b)
		document.SetFont(fontFamily, "", fontSize)
		document.SetXY(annotation.X, annotation.Y)
		document.MultiCell(width, fontSize*1.35, annotation.Text, "", "L", false)
	case "line":
		if annotation.X2 == annotation.X && annotation.Y2 == annotation.Y {
			return fmt.Errorf("line annotation requires x2/y2 different from x/y")
		}
		document.Line(annotation.X, annotation.Y, annotation.X2, annotation.Y2)
	case "rectangle":
		if annotation.Width <= 0 || annotation.Height <= 0 {
			return fmt.Errorf("rectangle annotation requires positive width and height")
		}
		document.Rect(annotation.X, annotation.Y, annotation.Width, annotation.Height, "D")
	}
	return nil
}

func configurePDFFont(document *gofpdf.Fpdf, fontPath string) (string, error) {
	if fontPath == "" {
		document.SetFont("Helvetica", "", 11)
		return "Helvetica", nil
	}
	document.AddUTF8Font("DocumentFont", "", fontPath)
	if document.Err() {
		return "", fmt.Errorf("load PDF font: %w", document.Error())
	}
	document.SetFont("DocumentFont", "", 11)
	if document.Err() {
		return "", fmt.Errorf("select PDF font: %w", document.Error())
	}
	return "DocumentFont", nil
}

func requestNeedsUnicodeFont(request documentCreateRequest) bool {
	if containsNonASCII(request.Title) || containsNonASCII(request.Subject) || containsNonASCII(request.Author) {
		return true
	}
	for _, block := range request.Blocks {
		if containsNonASCII(block.Text) {
			return true
		}
		for _, row := range block.Rows {
			for _, cell := range row {
				if containsNonASCII(cell) {
					return true
				}
			}
		}
	}
	return false
}

func containsNonASCII(value string) bool {
	for _, character := range value {
		if character > 0x7F {
			return true
		}
	}
	return false
}

func parseHexColor(value string) (int, int, int, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if value == "" {
		return 239, 68, 68, nil
	}
	if len(value) != 6 {
		return 0, 0, 0, fmt.Errorf("color must use #RRGGBB")
	}
	parsed, err := strconv.ParseUint(value, 16, 24)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("color must use #RRGGBB")
	}
	return int(parsed >> 16), int((parsed >> 8) & 0xFF), int(parsed & 0xFF), nil
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
