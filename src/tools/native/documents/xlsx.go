package documents

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

type workbookSheet struct {
	Name  string
	Part  string
	Index int
}

type spreadsheetRow struct {
	Number int
	Cells  []spreadsheetCell
}

type spreadsheetCell struct {
	Reference string
	Value     string
	Formula   string
}

func inspectXLSX(ctx context.Context, filePath string, base documentInspection) (documentInspection, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentInspection{}, err
	}
	defer archive.Close()
	sheets, err := workbookSheets(archive)
	if err != nil {
		return documentInspection{}, err
	}
	base.Metadata = officeMetadata(archive)
	base.SheetCount = len(sheets)
	base.Sections = make([]documentSection, 0, len(sheets))
	for _, sheet := range sheets {
		if err := ctx.Err(); err != nil {
			return documentInspection{}, err
		}
		rows, columns := 0, 0
		if data, readErr := archive.read(sheet.Part); readErr == nil {
			rows, columns = worksheetDimension(data)
		}
		base.Sections = append(base.Sections, documentSection{
			Name: sheet.Name, Kind: "worksheet", Index: sheet.Index, Rows: rows, Columns: columns,
		})
	}
	return base, nil
}

func readXLSX(ctx context.Context, filePath string, options readOptions) (documentReadResult, error) {
	archive, err := openOfficeArchive(filePath)
	if err != nil {
		return documentReadResult{}, err
	}
	defer archive.Close()
	sheets, err := workbookSheets(archive)
	if err != nil {
		return documentReadResult{}, err
	}
	if len(sheets) == 0 {
		return documentReadResult{}, fmt.Errorf("XLSX does not contain any worksheets")
	}
	selected, err := selectWorkbookSheet(sheets, options.Section)
	if err != nil {
		return documentReadResult{}, err
	}
	sharedStrings := []string{}
	if archive.has("xl/sharedStrings.xml") {
		data, readErr := archive.read("xl/sharedStrings.xml")
		if readErr != nil {
			return documentReadResult{}, readErr
		}
		sharedStrings, err = parseSharedStrings(ctx, data)
		if err != nil {
			return documentReadResult{}, fmt.Errorf("parse XLSX shared strings: %w", err)
		}
	}
	data, err := archive.read(selected.Part)
	if err != nil {
		return documentReadResult{}, err
	}
	totalRows, totalColumns := worksheetDimension(data)
	startRow := options.StartRow
	endRow := options.EndRow
	if endRow == 0 {
		endRow = startRow + 199
	}
	if endRow < startRow {
		return documentReadResult{}, fmt.Errorf("end_row must be greater than or equal to start_row")
	}
	rows, err := extractWorksheetRows(ctx, data, sharedStrings, startRow, endRow)
	if err != nil {
		return documentReadResult{}, fmt.Errorf("read XLSX worksheet %s: %w", selected.Name, err)
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("[Excel sheet: %s; rows %d-%d", selected.Name, startRow, endRow))
	if totalRows > 0 {
		output.WriteString(fmt.Sprintf("/%d", totalRows))
	}
	output.WriteString("]\n")
	if len(rows) == 0 {
		output.WriteString("[no populated cells in selected row range]\n")
	}
	for _, row := range rows {
		output.WriteString(fmt.Sprintf("R%d:", row.Number))
		for _, cell := range row.Cells {
			output.WriteByte(' ')
			output.WriteString(cell.Reference)
			if options.IncludeFormula && cell.Formula != "" {
				output.WriteString(" formula=")
				output.WriteString(strconv.Quote(cell.Formula))
				output.WriteString(" value=")
			} else {
				output.WriteByte('=')
			}
			output.WriteString(strconv.Quote(cell.Value))
			output.WriteString(" |")
		}
		output.WriteByte('\n')
	}
	return documentReadResult{
		Format:  string(formatXLSX),
		Content: output.String(),
		Details: map[string]any{
			"sheet":       selected.Name,
			"sheet_index": selected.Index,
			"start_row":   startRow,
			"end_row":     endRow,
			"total_rows":  totalRows,
			"columns":     totalColumns,
			"rows_read":   len(rows),
		},
	}, nil
}

func workbookSheets(archive *officeArchive) ([]workbookSheet, error) {
	workbook, err := archive.read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	relationships, err := archive.read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	targets := relationshipTargets(relationships, "xl")
	decoder := xml.NewDecoder(bytes.NewReader(workbook))
	result := []workbookSheet{}
	for {
		token, decodeErr := decoder.Token()
		if decodeErr == io.EOF {
			break
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("parse XLSX workbook: %w", decodeErr)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		name := xmlAttribute(start, "name")
		relationshipID := xmlRelationshipID(start)
		part := targets[relationshipID]
		if name == "" || part == "" || !archive.has(part) {
			continue
		}
		result = append(result, workbookSheet{Name: name, Part: part, Index: len(result) + 1})
	}
	return result, nil
}

func selectWorkbookSheet(sheets []workbookSheet, requested string) (workbookSheet, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return sheets[0], nil
	}
	if index, err := strconv.Atoi(requested); err == nil && index >= 1 && index <= len(sheets) {
		return sheets[index-1], nil
	}
	for _, sheet := range sheets {
		if strings.EqualFold(sheet.Name, requested) {
			return sheet, nil
		}
	}
	names := make([]string, 0, len(sheets))
	for _, sheet := range sheets {
		names = append(names, sheet.Name)
	}
	return workbookSheet{}, fmt.Errorf("XLSX worksheet %q not found; available worksheets: %s", requested, strings.Join(names, ", "))
}

func worksheetDimension(data []byte) (rows, columns int) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return 0, 0
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "dimension" {
			continue
		}
		reference := xmlAttribute(start, "ref")
		if separator := strings.LastIndex(reference, ":"); separator >= 0 {
			reference = reference[separator+1:]
		}
		return cellCoordinate(reference)
	}
}

func cellCoordinate(reference string) (row, column int) {
	for _, character := range strings.ToUpper(reference) {
		switch {
		case unicode.IsLetter(character):
			column = column*26 + int(character-'A'+1)
		case unicode.IsDigit(character):
			row = row*10 + int(character-'0')
		}
	}
	return row, column
}

func parseSharedStrings(ctx context.Context, data []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := []string{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "si" {
			continue
		}
		value, err := collectElementText(decoder, start)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
}

func extractWorksheetRows(ctx context.Context, data []byte, sharedStrings []string, startRow, endRow int) ([]spreadsheetRow, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := []spreadsheetRow{}
	currentRow := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		rowNumber, _ := strconv.Atoi(xmlAttribute(start, "r"))
		if rowNumber == 0 {
			rowNumber = currentRow + 1
		}
		currentRow = rowNumber
		if rowNumber > endRow {
			return result, nil
		}
		row, err := decodeSpreadsheetRow(decoder, start, rowNumber, sharedStrings)
		if err != nil {
			return nil, err
		}
		if rowNumber >= startRow && len(row.Cells) > 0 {
			result = append(result, row)
		}
	}
}

func decodeSpreadsheetRow(decoder *xml.Decoder, start xml.StartElement, rowNumber int, sharedStrings []string) (spreadsheetRow, error) {
	row := spreadsheetRow{Number: rowNumber}
	for {
		token, err := decoder.Token()
		if err != nil {
			return row, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == "c" {
				cell, err := decodeSpreadsheetCell(decoder, typed, sharedStrings)
				if err != nil {
					return row, err
				}
				if cell.Reference != "" || cell.Value != "" || cell.Formula != "" {
					row.Cells = append(row.Cells, cell)
				}
			}
		case xml.EndElement:
			if typed.Name.Local == start.Name.Local {
				return row, nil
			}
		}
	}
}

func decodeSpreadsheetCell(decoder *xml.Decoder, start xml.StartElement, sharedStrings []string) (spreadsheetCell, error) {
	cell := spreadsheetCell{Reference: xmlAttribute(start, "r")}
	cellType := xmlAttribute(start, "t")
	for {
		token, err := decoder.Token()
		if err != nil {
			return cell, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "v":
				if err := decoder.DecodeElement(&cell.Value, &typed); err != nil {
					return cell, err
				}
			case "f":
				if err := decoder.DecodeElement(&cell.Formula, &typed); err != nil {
					return cell, err
				}
			case "is":
				value, err := collectElementText(decoder, typed)
				if err != nil {
					return cell, err
				}
				cell.Value = value
			}
		case xml.EndElement:
			if typed.Name.Local == start.Name.Local {
				cell.Value = spreadsheetValue(cell.Value, cellType, sharedStrings)
				return cell, nil
			}
		}
	}
}

func spreadsheetValue(value, cellType string, sharedStrings []string) string {
	value = strings.TrimSpace(value)
	switch cellType {
	case "s":
		index, err := strconv.Atoi(value)
		if err == nil && index >= 0 && index < len(sharedStrings) {
			return sharedStrings[index]
		}
	case "b":
		if value == "1" {
			return "TRUE"
		}
		if value == "0" {
			return "FALSE"
		}
	}
	return value
}

func collectElementText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var output strings.Builder
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth > 1 {
				output.Write([]byte(typed))
			}
		}
	}
	return output.String(), nil
}
