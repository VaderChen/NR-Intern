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

// sheetsFromCellUpdates 把儲存格清單攤成工作表的列陣列。
//
// 工作表順序依第一次出現的先後，欄列大小由最大的儲存格參照決定；沒有被指定的
// 位置留空。公式（formula）沿用既有的 formulas 對照表。
func sheetsFromCellUpdates(updates []cellUpdate) ([]spreadsheetSheet, error) {
	order := []string{}
	grids := map[string]map[int]map[int]any{}
	formulas := map[string]map[string]string{}
	for index, update := range updates {
		name := strings.TrimSpace(update.Sheet)
		if name == "" {
			name = "Sheet1"
		}
		// 允許 Excel 的完整寫法 Sheet1!A1 與 '工作表 1'!A1：工作表名稱就寫在那裡，
		// 沒有理由因為格式不同就拒絕。sheet 欄位缺席時就用參照裡的名稱。
		// rows 直接給整張表：這是 sheets 的形狀被放進 cell_updates，內容不用改。
		if len(update.Rows) > 0 {
			if _, exists := grids[name]; !exists {
				grids[name] = map[int]map[int]any{}
				formulas[name] = map[string]string{}
				order = append(order, name)
			}
			base := 0
			for row := range grids[name] {
				if row > base {
					base = row
				}
			}
			for rowIndex, values := range update.Rows {
				row := base + rowIndex + 1
				if _, exists := grids[name][row]; !exists {
					grids[name][row] = map[int]any{}
				}
				for columnIndex, value := range values {
					grids[name][row][columnIndex+1] = value
				}
			}
			continue
		}
		// value 是「儲存格對照表」時，一筆就描述整張表：{"A1": "部門", "B1": "數量"}。
		//
		// 實測 20 次呼叫裡，模型反覆用這種寫法——對一份 23 列的表，要它吐出上百筆
		// 單格項目本來就不合理，它的表達方式比較接近人的想法。形狀沒有歧義，收下。
		if cells, ok := cellReferenceMap(update.Value); ok {
			if _, exists := grids[name]; !exists {
				grids[name] = map[int]map[int]any{}
				formulas[name] = map[string]string{}
				order = append(order, name)
			}
			for reference, value := range cells {
				match := spreadsheetCellReferencePattern.FindStringSubmatch(strings.ToUpper(reference))
				row, _ := strconv.Atoi(match[2])
				column := columnIndexFromLetters(match[1])
				if _, exists := grids[name][row]; !exists {
					grids[name][row] = map[int]any{}
				}
				grids[name][row][column] = value
			}
			continue
		}
		// 數字定址優先：row/column 明確，不需要解析任何字串格式。
		if update.Row > 0 && update.Column > 0 {
			if _, exists := grids[name]; !exists {
				grids[name] = map[int]map[int]any{}
				formulas[name] = map[string]string{}
				order = append(order, name)
			}
			if strings.TrimSpace(update.Formula) != "" {
				formulas[name][cellReferenceFromIndexes(update.Row, update.Column)] = update.Formula
				continue
			}
			if _, exists := grids[name][update.Row]; !exists {
				grids[name][update.Row] = map[int]any{}
			}
			grids[name][update.Row][update.Column] = update.Value
			continue
		}
		reference := strings.TrimSpace(update.Cell)
		// 有些模型會自創「工作表/列/欄」這種寫法（實測看過 "sheet0/1/1"）。
		// 這個形狀沒有歧義，直接支援比讓它反覆重試划算。
		if parts := strings.Split(reference, "/"); len(parts) == 3 {
			row, rowErr := strconv.Atoi(strings.TrimSpace(parts[1]))
			column, columnErr := strconv.Atoi(strings.TrimSpace(parts[2]))
			if rowErr == nil && columnErr == nil && row > 0 && column > 0 {
				if qualifier := strings.TrimSpace(parts[0]); qualifier != "" && strings.TrimSpace(update.Sheet) == "" {
					name = qualifier
				}
				reference = cellReferenceFromIndexes(row, column)
			}
		}
		if separator := strings.LastIndex(reference, "!"); separator >= 0 {
			qualifier := strings.Trim(strings.TrimSpace(reference[:separator]), "'")
			reference = strings.TrimSpace(reference[separator+1:])
			if strings.TrimSpace(update.Sheet) == "" && qualifier != "" {
				name = qualifier
			}
		}
		reference = strings.ToUpper(reference)
		match := spreadsheetCellReferencePattern.FindStringSubmatch(reference)
		if match == nil {
			// 錯誤訊息要把可用的寫法講完，模型才修得回來。
			return nil, fmt.Errorf(`cell_updates[%d]: 位置無法解析（cell=%q）。可用寫法：cell 為 "A1" 或 "Sheet1!A1"；或用 row 與 column（皆為 1 起算的整數）；或 value 直接給儲存格對照表 {"A1": "部門", "B1": "數量"}`, index, update.Cell)
		}
		column := columnIndexFromLetters(match[1])
		row, err := strconv.Atoi(match[2])
		if err != nil || row < 1 {
			return nil, fmt.Errorf("cell_updates[%d]: cell %q is not a valid reference such as A1", index, update.Cell)
		}
		if _, exists := grids[name]; !exists {
			grids[name] = map[int]map[int]any{}
			formulas[name] = map[string]string{}
			order = append(order, name)
		}
		if strings.TrimSpace(update.Formula) != "" {
			formulas[name][reference] = update.Formula
			continue
		}
		if _, exists := grids[name][row]; !exists {
			grids[name][row] = map[int]any{}
		}
		grids[name][row][column] = update.Value
	}
	sheets := make([]spreadsheetSheet, 0, len(order))
	for _, name := range order {
		grid := grids[name]
		maxRow, maxColumn := 0, 0
		for row, columns := range grid {
			if row > maxRow {
				maxRow = row
			}
			for column := range columns {
				if column > maxColumn {
					maxColumn = column
				}
			}
		}
		rows := make([][]any, 0, maxRow)
		for row := 1; row <= maxRow; row++ {
			values := make([]any, maxColumn)
			for column := 1; column <= maxColumn; column++ {
				if cell, exists := grid[row][column]; exists {
					values[column-1] = cell
				}
			}
			rows = append(rows, values)
		}
		sheet := spreadsheetSheet{Name: name, Rows: rows}
		if len(formulas[name]) > 0 {
			sheet.Formulas = formulas[name]
		}
		sheets = append(sheets, sheet)
	}
	return sheets, nil
}

// cellReferenceMap 判斷 value 是不是「儲存格參照 → 內容」的對照表。
// 必須每一個鍵都是合法的儲存格參照，才不會把一般的巢狀資料誤判成表格。
func cellReferenceMap(value any) (map[string]any, bool) {
	cells, ok := value.(map[string]any)
	if !ok || len(cells) == 0 {
		return nil, false
	}
	for reference := range cells {
		if !spreadsheetCellReferencePattern.MatchString(strings.ToUpper(strings.TrimSpace(reference))) {
			return nil, false
		}
	}
	return cells, true
}

// cellReferenceFromIndexes 把 1 起算的列欄號轉回 A1 標記。
func cellReferenceFromIndexes(row, column int) string {
	letters := ""
	for value := column; value > 0; value = (value - 1) / 26 {
		letters = string(rune('A'+(value-1)%26)) + letters
	}
	return fmt.Sprintf("%s%d", letters, row)
}

// columnIndexFromLetters 把 A、B…Z、AA 轉成 1 起算的欄號。
func columnIndexFromLetters(letters string) int {
	index := 0
	for _, value := range strings.ToUpper(letters) {
		index = index*26 + int(value-'A') + 1
	}
	return index
}

func createXLSX(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	// 沒有 sheets 但有 cell_updates 時，用儲存格內容組出工作表。
	//
	// cell_updates 原本是給「編輯既有活頁簿」用的，但模型很自然會用儲存格的語言
	// 描述一份新表（{"sheet":"設備清單","cell":"A1","value":"總覽"}）。意圖完全明確：
	// 工作表名稱、位置、內容都給齊了。因為欄位名稱不同就拒絕，只會讓它一直重試——
	// 實測連續失敗到被 loop guard 擋下，最後回使用者「XLSX 工具呼叫失敗」。
	if len(request.Sheets) == 0 && len(request.CellUpdates) > 0 {
		sheets, err := sheetsFromCellUpdates(request.CellUpdates)
		if err != nil {
			return authoredDocument{}, err
		}
		request.Sheets = sheets
		request.CellUpdates = nil
	}
	if len(request.Sheets) == 0 {
		return authoredDocument{}, fmt.Errorf("XLSX requires at least one sheet: provide sheets (name plus rows), or cell_updates with sheet, cell and value")
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
