package documents

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var spreadsheetCellReferencePattern = regexp.MustCompile(`(?i)^([A-Z]{1,3})([1-9][0-9]{0,6})$`)

func createXLSX(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if len(request.Sheets) == 0 {
		return authoredDocument{}, fmt.Errorf("XLSX requires at least one sheet")
	}
	entries := map[string][]byte{
		"_rels/.rels":       rootRelationships("xl/workbook.xml"),
		"docProps/core.xml": officeCoreProperties(request.Title, request.Subject, request.Author),
		"xl/styles.xml":     xlsxStyles(),
	}
	var workbook strings.Builder
	var relationships strings.Builder
	var contentTypes strings.Builder
	var appTitles strings.Builder
	seenNames := map[string]struct{}{}
	workbook.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView xWindow="0" yWindow="0" windowWidth="24000" windowHeight="12000"/></bookViews><sheets>`)
	relationships.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	contentTypes.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	for index, sheet := range request.Sheets {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		name := strings.TrimSpace(sheet.Name)
		if err := validateWorksheetName(name); err != nil {
			return authoredDocument{}, fmt.Errorf("sheets[%d]: %w", index, err)
		}
		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			return authoredDocument{}, fmt.Errorf("duplicate worksheet name %q", name)
		}
		seenNames[key] = struct{}{}
		sheetID := index + 1
		part := fmt.Sprintf("xl/worksheets/sheet%d.xml", sheetID)
		worksheet, err := buildWorksheet(sheet)
		if err != nil {
			return authoredDocument{}, fmt.Errorf("sheets[%d]: %w", index, err)
		}
		entries[part] = worksheet
		workbook.WriteString(`<sheet name="` + xmlAttributeText(name) + `" sheetId="` + strconv.Itoa(sheetID) + `" r:id="rId` + strconv.Itoa(sheetID) + `"/>`)
		relationships.WriteString(`<Relationship Id="rId` + strconv.Itoa(sheetID) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet` + strconv.Itoa(sheetID) + `.xml"/>`)
		contentTypes.WriteString(`<Override PartName="/xl/worksheets/sheet` + strconv.Itoa(sheetID) + `.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`)
		appTitles.WriteString(`<vt:lpstr>` + xmlText(name) + `</vt:lpstr>`)
	}
	styleRelID := len(request.Sheets) + 1
	workbook.WriteString(`</sheets><calcPr calcId="191029" fullCalcOnLoad="1" forceFullCalc="1"/></workbook>`)
	relationships.WriteString(`<Relationship Id="rId` + strconv.Itoa(styleRelID) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`)
	contentTypes.WriteString(`</Types>`)
	entries["xl/workbook.xml"] = []byte(workbook.String())
	entries["xl/_rels/workbook.xml.rels"] = []byte(relationships.String())
	entries["[Content_Types].xml"] = []byte(contentTypes.String())
	entries["docProps/app.xml"] = xlsxAppProperties(len(request.Sheets), appTitles.String())
	data, err := buildOpenXMLPackage(ctx, entries)
	if err != nil {
		return authoredDocument{}, err
	}
	return authoredDocument{Data: data, Details: map[string]any{"sheet_count": len(request.Sheets)}}, nil
}

func buildWorksheet(sheet spreadsheetSheet) ([]byte, error) {
	cells := map[int]map[int]worksheetCellValue{}
	maxRow, maxColumn := 1, 1
	if len(sheet.Rows) > 1_048_576 {
		return nil, fmt.Errorf("worksheet exceeds 1,048,576 rows")
	}
	for rowIndex, row := range sheet.Rows {
		if len(row) > 16_384 {
			return nil, fmt.Errorf("row %d exceeds 16,384 columns", rowIndex+1)
		}
		for columnIndex, value := range row {
			if value == nil {
				continue
			}
			rowNumber, columnNumber := rowIndex+1, columnIndex+1
			if cells[rowNumber] == nil {
				cells[rowNumber] = map[int]worksheetCellValue{}
			}
			cells[rowNumber][columnNumber] = worksheetCellValue{Value: value, Style: boolInt(rowNumber <= sheet.HeaderRows)}
			if rowNumber > maxRow {
				maxRow = rowNumber
			}
			if columnNumber > maxColumn {
				maxColumn = columnNumber
			}
		}
	}
	for reference, formula := range sheet.Formulas {
		row, column, err := parseSpreadsheetCellReference(reference)
		if err != nil {
			return nil, fmt.Errorf("formula cell %q: %w", reference, err)
		}
		if cells[row] == nil {
			cells[row] = map[int]worksheetCellValue{}
		}
		value := cells[row][column]
		value.Formula = strings.TrimPrefix(strings.TrimSpace(formula), "=")
		value.Style = boolInt(row <= sheet.HeaderRows)
		cells[row][column] = value
		if row > maxRow {
			maxRow = row
		}
		if column > maxColumn {
			maxColumn = column
		}
	}
	dimension := "A1:" + spreadsheetColumnName(maxColumn) + strconv.Itoa(maxRow)
	if maxRow == 1 && maxColumn == 1 {
		dimension = "A1"
	}
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><dimension ref="` + dimension + `"/>`)
	if len(sheet.ColumnWidths) > 0 {
		widths := make(map[string]float64, len(sheet.ColumnWidths))
		keys := make([]string, 0, len(sheet.ColumnWidths))
		for key, width := range sheet.ColumnWidths {
			normalized := strings.ToUpper(strings.TrimSpace(key))
			if _, exists := widths[normalized]; !exists {
				keys = append(keys, normalized)
			}
			widths[normalized] = width
		}
		sort.Strings(keys)
		output.WriteString(`<cols>`)
		for _, columnName := range keys {
			_, column, err := parseSpreadsheetCellReference(columnName + "1")
			if err != nil {
				return nil, fmt.Errorf("invalid column_widths key %q", columnName)
			}
			width := widths[columnName]
			if width <= 0 || width > 255 {
				return nil, fmt.Errorf("column %s width must be between 0 and 255", columnName)
			}
			output.WriteString(`<col min="` + strconv.Itoa(column) + `" max="` + strconv.Itoa(column) + `" width="` + strconv.FormatFloat(width, 'f', -1, 64) + `" customWidth="1"/>`)
		}
		output.WriteString(`</cols>`)
	}
	if sheet.FreezeRows > 0 {
		if sheet.FreezeRows >= 1_048_576 {
			return nil, fmt.Errorf("freeze_rows exceeds worksheet limit")
		}
		output.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="` + strconv.Itoa(sheet.FreezeRows) + `" topLeftCell="A` + strconv.Itoa(sheet.FreezeRows+1) + `" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	} else {
		output.WriteString(`<sheetViews><sheetView workbookViewId="0"/></sheetViews>`)
	}
	output.WriteString(`<sheetFormatPr defaultRowHeight="15"/><sheetData>`)
	rowNumbers := make([]int, 0, len(cells))
	for row := range cells {
		rowNumbers = append(rowNumbers, row)
	}
	sort.Ints(rowNumbers)
	for _, row := range rowNumbers {
		output.WriteString(`<row r="` + strconv.Itoa(row) + `">`)
		columns := make([]int, 0, len(cells[row]))
		for column := range cells[row] {
			columns = append(columns, column)
		}
		sort.Ints(columns)
		for _, column := range columns {
			reference := spreadsheetColumnName(column) + strconv.Itoa(row)
			output.WriteString(worksheetCellXML(reference, cells[row][column]))
		}
		output.WriteString(`</row>`)
	}
	output.WriteString(`</sheetData>`)
	if sheet.AutoFilter && len(cells) > 0 {
		output.WriteString(`<autoFilter ref="` + dimension + `"/>`)
	}
	output.WriteString(`<pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	return []byte(output.String()), nil
}

type worksheetCellValue struct {
	Value   any
	Formula string
	Style   int
}

func worksheetCellXML(reference string, cell worksheetCellValue) string {
	style := ""
	if cell.Style > 0 {
		style = ` s="` + strconv.Itoa(cell.Style) + `"`
	}
	if cell.Formula != "" {
		formula := `<f>` + xmlText(cell.Formula) + `</f>`
		switch value := cell.Value.(type) {
		case nil:
			return `<c r="` + reference + `"` + style + `>` + formula + `</c>`
		case bool:
			encoded := "0"
			if value {
				encoded = "1"
			}
			return `<c r="` + reference + `" t="b"` + style + `>` + formula + `<v>` + encoded + `</v></c>`
		case float64:
			return `<c r="` + reference + `"` + style + `>` + formula + `<v>` + strconv.FormatFloat(value, 'g', -1, 64) + `</v></c>`
		case float32:
			return `<c r="` + reference + `"` + style + `>` + formula + `<v>` + strconv.FormatFloat(float64(value), 'g', -1, 32) + `</v></c>`
		case int:
			return `<c r="` + reference + `"` + style + `>` + formula + `<v>` + strconv.Itoa(value) + `</v></c>`
		case int64:
			return `<c r="` + reference + `"` + style + `>` + formula + `<v>` + strconv.FormatInt(value, 10) + `</v></c>`
		default:
			return `<c r="` + reference + `" t="str"` + style + `>` + formula + `<v>` + xmlText(fmt.Sprint(value)) + `</v></c>`
		}
	}
	switch value := cell.Value.(type) {
	case nil:
		return `<c r="` + reference + `"` + style + `></c>`
	case bool:
		encoded := "0"
		if value {
			encoded = "1"
		}
		return `<c r="` + reference + `" t="b"` + style + `><v>` + encoded + `</v></c>`
	case float64:
		return `<c r="` + reference + `"` + style + `><v>` + strconv.FormatFloat(value, 'g', -1, 64) + `</v></c>`
	case float32:
		return `<c r="` + reference + `"` + style + `><v>` + strconv.FormatFloat(float64(value), 'g', -1, 32) + `</v></c>`
	case int:
		return `<c r="` + reference + `"` + style + `><v>` + strconv.Itoa(value) + `</v></c>`
	case int64:
		return `<c r="` + reference + `"` + style + `><v>` + strconv.FormatInt(value, 10) + `</v></c>`
	default:
		return `<c r="` + reference + `" t="inlineStr"` + style + `><is><t xml:space="preserve">` + xmlText(fmt.Sprint(value)) + `</t></is></c>`
	}
}

func validateWorksheetName(name string) error {
	if name == "" {
		return fmt.Errorf("worksheet name is required")
	}
	if len([]rune(name)) > 31 {
		return fmt.Errorf("worksheet name exceeds 31 characters")
	}
	if strings.ContainsAny(name, `[]:*?/\`) {
		return fmt.Errorf("worksheet name contains an invalid character")
	}
	if strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'") {
		return fmt.Errorf("worksheet name cannot begin or end with an apostrophe")
	}
	return nil
}

func parseSpreadsheetCellReference(reference string) (row, column int, err error) {
	match := spreadsheetCellReferencePattern.FindStringSubmatch(strings.TrimSpace(reference))
	if match == nil {
		return 0, 0, fmt.Errorf("cell reference must be A1 notation")
	}
	row, _ = strconv.Atoi(match[2])
	for _, character := range strings.ToUpper(match[1]) {
		column = column*26 + int(character-'A'+1)
	}
	if row < 1 || row > 1_048_576 || column < 1 || column > 16_384 {
		return 0, 0, fmt.Errorf("cell reference is outside XLSX limits")
	}
	return row, column, nil
}

func spreadsheetColumnName(column int) string {
	var result []byte
	for column > 0 {
		column--
		result = append([]byte{byte('A' + column%26)}, result...)
		column /= 26
	}
	return string(result)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func xlsxStyles() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><color theme="1"/><name val="Arial"/><family val="2"/></font><font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Arial"/><family val="2"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF1D4ED8"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left style="thin"><color rgb="FFD6DCE2"/></left><right style="thin"><color rgb="FFD6DCE2"/></right><top style="thin"><color rgb="FFD6DCE2"/></top><bottom style="thin"><color rgb="FFD6DCE2"/></bottom><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"><alignment vertical="center"/></xf><xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1"><alignment vertical="center"/></xf></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles><dxfs count="0"/><tableStyles count="0" defaultTableStyle="TableStyleMedium2" defaultPivotStyle="PivotStyleLight16"/></styleSheet>`)
}

func xlsxAppProperties(sheetCount int, titles string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>NR-Intern</Application><DocSecurity>0</DocSecurity><ScaleCrop>false</ScaleCrop><HeadingPairs><vt:vector size="2" baseType="variant"><vt:variant><vt:lpstr>Worksheets</vt:lpstr></vt:variant><vt:variant><vt:i4>` + strconv.Itoa(sheetCount) + `</vt:i4></vt:variant></vt:vector></HeadingPairs><TitlesOfParts><vt:vector size="` + strconv.Itoa(sheetCount) + `" baseType="lpstr">` + titles + `</vt:vector></TitlesOfParts><Company></Company><LinksUpToDate>false</LinksUpToDate><SharedDoc>false</SharedDoc><HyperlinksChanged>false</HyperlinksChanged><AppVersion>1.0</AppVersion></Properties>`)
}
