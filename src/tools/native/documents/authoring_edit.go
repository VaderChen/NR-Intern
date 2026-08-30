package documents

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	wordParagraphXMLPattern   = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?p(?:\s[^>]*)?>.*?</(?:[A-Za-z0-9_]+:)?p\s*>`)
	spreadsheetItemXMLPattern = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?(?:si|is)(?:\s[^>]*)?>.*?</(?:[A-Za-z0-9_]+:)?(?:si|is)\s*>`)
	xmlTextNodePattern        = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?t\b[^>]*>(.*?)</(?:[A-Za-z0-9_]+:)?t\s*>`)
)

func editOpenXML(ctx context.Context, sourcePath string, format documentFormat, request documentEditRequest) (authoredDocument, error) {
	if len(request.Annotations) > 0 {
		return authoredDocument{}, fmt.Errorf("annotations are only supported for PDF")
	}
	if format != formatXLSX && len(request.CellUpdates) > 0 {
		return authoredDocument{}, fmt.Errorf("cell_updates are only supported for XLSX")
	}
	if len(request.Replacements) == 0 && len(request.CellUpdates) == 0 {
		return authoredDocument{}, fmt.Errorf("document edit requires replacements or cell_updates")
	}
	archive, err := openOfficeArchive(sourcePath)
	if err != nil {
		return authoredDocument{}, err
	}
	parts, groupPattern, err := editableOpenXMLParts(archive, format)
	if err != nil {
		_ = archive.Close()
		return authoredDocument{}, err
	}
	updates := map[string][]byte{}
	for _, part := range parts {
		data, readErr := archive.read(part)
		if readErr != nil {
			_ = archive.Close()
			return authoredDocument{}, readErr
		}
		updates[part] = data
	}
	sheets := []workbookSheet{}
	if format == formatXLSX {
		sheets, err = workbookSheets(archive)
		if err != nil {
			_ = archive.Close()
			return authoredDocument{}, err
		}
	}
	_ = archive.Close()
	totalReplacements := 0
	for index, replacement := range request.Replacements {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		if replacement.OldText == "" {
			return authoredDocument{}, fmt.Errorf("replacements[%d].old_text is required", index)
		}
		limit := 1
		if replacement.ReplaceAll {
			limit = -1
		}
		count := 0
		for _, part := range parts {
			if limit > 0 && count >= limit {
				break
			}
			remaining := limit
			if limit > 0 {
				remaining = limit - count
			}
			updated, partCount := replaceXMLGroupedText(updates[part], groupPattern, replacement.OldText, replacement.NewText, remaining)
			updates[part] = updated
			count += partCount
		}
		if count == 0 {
			return authoredDocument{}, fmt.Errorf("replacements[%d].old_text was not found; document was not changed", index)
		}
		if replacement.ExpectedReplacements > 0 && count != replacement.ExpectedReplacements {
			return authoredDocument{}, fmt.Errorf("replacements[%d] precondition failed: expected %d, would replace %d", index, replacement.ExpectedReplacements, count)
		}
		totalReplacements += count
	}
	cellChanges := 0
	if format == formatXLSX {
		for index, update := range request.CellUpdates {
			if err := ctx.Err(); err != nil {
				return authoredDocument{}, err
			}
			sheet, selectErr := selectWorkbookSheet(sheets, update.Sheet)
			if selectErr != nil {
				return authoredDocument{}, fmt.Errorf("cell_updates[%d]: %w", index, selectErr)
			}
			row, column, parseErr := parseSpreadsheetCellReference(update.Cell)
			if parseErr != nil {
				return authoredDocument{}, fmt.Errorf("cell_updates[%d].cell: %w", index, parseErr)
			}
			cell := worksheetCellValue{Value: update.Value, Formula: strings.TrimPrefix(strings.TrimSpace(update.Formula), "=")}
			updated, changed, updateErr := setWorksheetCell(updates[sheet.Part], row, column, cell)
			if updateErr != nil {
				return authoredDocument{}, fmt.Errorf("cell_updates[%d]: %w", index, updateErr)
			}
			updates[sheet.Part] = updated
			if changed {
				cellChanges++
			}
		}
	}
	data, err := rewriteOpenXMLPackage(ctx, sourcePath, updates)
	if err != nil {
		return authoredDocument{}, err
	}
	return authoredDocument{Data: data, Details: map[string]any{"replacement_count": totalReplacements, "cell_update_count": cellChanges}}, nil
}

func editableOpenXMLParts(archive *officeArchive, format documentFormat) ([]string, *regexp.Regexp, error) {
	parts := []string{}
	switch format {
	case formatDOCX:
		for _, section := range docxSections(archive) {
			parts = append(parts, section.part)
		}
		return parts, wordParagraphXMLPattern, nil
	case formatPPTX:
		for _, prefix := range []string{"ppt/slides/slide", "ppt/notesSlides/notesSlide"} {
			for _, name := range archive.names(prefix) {
				if strings.HasSuffix(name, ".xml") {
					parts = append(parts, name)
				}
			}
		}
		sort.Strings(parts)
		return parts, wordParagraphXMLPattern, nil
	case formatXLSX:
		if archive.has("xl/sharedStrings.xml") {
			parts = append(parts, "xl/sharedStrings.xml")
		}
		sheets, err := workbookSheets(archive)
		if err != nil {
			return nil, nil, err
		}
		for _, sheet := range sheets {
			parts = append(parts, sheet.Part)
		}
		return parts, spreadsheetItemXMLPattern, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Open XML edit format")
	}
}

type xmlTextNode struct {
	ContentStart int
	ContentEnd   int
	Text         string
	JoinedStart  int
	JoinedEnd    int
}

type textOccurrence struct {
	Start int
	End   int
}

func replaceXMLGroupedText(data []byte, groupPattern *regexp.Regexp, oldText, newText string, limit int) ([]byte, int) {
	groups := groupPattern.FindAllIndex(data, -1)
	if len(groups) == 0 {
		return data, 0
	}
	type groupUpdate struct {
		Start int
		End   int
		Data  []byte
	}
	updates := []groupUpdate{}
	replaced := 0
	for _, group := range groups {
		if limit > 0 && replaced >= limit {
			break
		}
		start, end := group[0], group[1]
		remaining := limit
		if limit > 0 {
			remaining = limit - replaced
		}
		updated, count := replaceXMLTextNodes(data[start:end], oldText, newText, remaining)
		if count == 0 {
			continue
		}
		updates = append(updates, groupUpdate{Start: start, End: end, Data: updated})
		replaced += count
	}
	result := append([]byte(nil), data...)
	for index := len(updates) - 1; index >= 0; index-- {
		update := updates[index]
		result = append(result[:update.Start], append(update.Data, result[update.End:]...)...)
	}
	return result, replaced
}

func replaceXMLTextNodes(group []byte, oldText, newText string, limit int) ([]byte, int) {
	matches := xmlTextNodePattern.FindAllSubmatchIndex(group, -1)
	if len(matches) == 0 {
		return group, 0
	}
	nodes := make([]xmlTextNode, 0, len(matches))
	var joined strings.Builder
	for _, match := range matches {
		decoded := html.UnescapeString(string(group[match[2]:match[3]]))
		node := xmlTextNode{ContentStart: match[2], ContentEnd: match[3], Text: decoded, JoinedStart: joined.Len()}
		joined.WriteString(decoded)
		node.JoinedEnd = joined.Len()
		nodes = append(nodes, node)
	}
	occurrences := findTextOccurrences(joined.String(), oldText, limit)
	if len(occurrences) == 0 {
		return group, 0
	}
	builders := make([]strings.Builder, len(nodes))
	cursor := 0
	for _, occurrence := range occurrences {
		appendOriginalNodeRange(builders, nodes, cursor, occurrence.Start)
		for index, node := range nodes {
			if occurrence.Start >= node.JoinedStart && occurrence.Start < node.JoinedEnd || occurrence.Start == node.JoinedEnd && occurrence.Start == len(joined.String()) {
				builders[index].WriteString(newText)
				break
			}
		}
		cursor = occurrence.End
	}
	appendOriginalNodeRange(builders, nodes, cursor, len(joined.String()))
	result := append([]byte(nil), group...)
	for index := len(nodes) - 1; index >= 0; index-- {
		node := nodes[index]
		encoded := []byte(xmlText(builders[index].String()))
		result = append(result[:node.ContentStart], append(encoded, result[node.ContentEnd:]...)...)
	}
	return result, len(occurrences)
}

func findTextOccurrences(value, oldText string, limit int) []textOccurrence {
	result := []textOccurrence{}
	offset := 0
	for offset <= len(value) {
		position := strings.Index(value[offset:], oldText)
		if position < 0 {
			break
		}
		start := offset + position
		result = append(result, textOccurrence{Start: start, End: start + len(oldText)})
		offset = start + len(oldText)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func appendOriginalNodeRange(builders []strings.Builder, nodes []xmlTextNode, start, end int) {
	if start >= end {
		return
	}
	for index, node := range nodes {
		intersectionStart := maxInt(start, node.JoinedStart)
		intersectionEnd := minInt(end, node.JoinedEnd)
		if intersectionStart >= intersectionEnd {
			continue
		}
		localStart := intersectionStart - node.JoinedStart
		localEnd := intersectionEnd - node.JoinedStart
		builders[index].WriteString(node.Text[localStart:localEnd])
	}
}

func setWorksheetCell(data []byte, row, column int, cell worksheetCellValue) ([]byte, bool, error) {
	reference := spreadsheetColumnName(column) + strconv.Itoa(row)
	cellPattern := regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?c\b[^>]*\br="` + regexp.QuoteMeta(reference) + `"[^>]*(?:/>|>.*?</(?:[A-Za-z0-9_]+:)?c\s*>)`)
	location := cellPattern.FindIndex(data)
	clearCell := cell.Value == nil && cell.Formula == ""
	if location != nil {
		if clearCell {
			return append(append([]byte(nil), data[:location[0]]...), data[location[1]:]...), true, nil
		}
		openingEnd := strings.Index(string(data[location[0]:location[1]]), ">")
		style := 0
		if openingEnd > 0 {
			opening := string(data[location[0] : location[0]+openingEnd+1])
			stylePattern := regexp.MustCompile(`\bs="([0-9]+)"`)
			if match := stylePattern.FindStringSubmatch(opening); match != nil {
				style, _ = strconv.Atoi(match[1])
			}
		}
		cell.Style = style
		replacement := []byte(worksheetCellXML(reference, cell))
		return append(append([]byte(nil), data[:location[0]]...), append(replacement, data[location[1]:]...)...), true, nil
	}
	if clearCell {
		return data, false, nil
	}
	cellXML := []byte(worksheetCellXML(reference, cell))
	rowPattern := regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_]+:)?row\b[^>]*\br="` + strconv.Itoa(row) + `"[^>]*(?:/>|>.*?</(?:[A-Za-z0-9_]+:)?row\s*>)`)
	rowLocation := rowPattern.FindIndex(data)
	result := append([]byte(nil), data...)
	if rowLocation != nil {
		rowXML := result[rowLocation[0]:rowLocation[1]]
		if bytesHasSelfClosingEnd(rowXML) {
			opening := strings.TrimSuffix(string(rowXML), "/>") + ">"
			rowXML = []byte(opening + string(cellXML) + `</row>`)
		} else {
			insertAt := worksheetCellInsertPosition(rowXML, column)
			if insertAt < 0 {
				return nil, false, fmt.Errorf("worksheet row %d is malformed", row)
			}
			rowXML = append(append([]byte(nil), rowXML[:insertAt]...), append(cellXML, rowXML[insertAt:]...)...)
		}
		result = append(result[:rowLocation[0]], append(rowXML, result[rowLocation[1]:]...)...)
	} else {
		insertAt := worksheetRowInsertPosition(result, row)
		if insertAt < 0 {
			return nil, false, fmt.Errorf("worksheet does not contain sheetData")
		}
		rowXML := []byte(`<row r="` + strconv.Itoa(row) + `">` + string(cellXML) + `</row>`)
		result = append(result[:insertAt], append(rowXML, result[insertAt:]...)...)
	}
	return expandWorksheetDimension(result, row, column), true, nil
}

func expandWorksheetDimension(data []byte, row, column int) []byte {
	existingRow, existingColumn := worksheetDimension(data)
	if row < existingRow {
		row = existingRow
	}
	if column < existingColumn {
		column = existingColumn
	}
	reference := "A1:" + spreadsheetColumnName(column) + strconv.Itoa(row)
	if row == 1 && column == 1 {
		reference = "A1"
	}
	pattern := regexp.MustCompile(`<(?:[A-Za-z0-9_]+:)?dimension\b[^>]*/>`)
	if location := pattern.FindIndex(data); location != nil {
		return append(append([]byte(nil), data[:location[0]]...), append([]byte(`<dimension ref="`+reference+`"/>`), data[location[1]:]...)...)
	}
	worksheetOpen := regexp.MustCompile(`<(?:[A-Za-z0-9_]+:)?worksheet\b[^>]*>`).FindIndex(data)
	if worksheetOpen == nil {
		return data
	}
	return append(data[:worksheetOpen[1]], append([]byte(`<dimension ref="`+reference+`"/>`), data[worksheetOpen[1]:]...)...)
}

func bytesHasSelfClosingEnd(value []byte) bool {
	return strings.HasSuffix(strings.TrimSpace(string(value)), "/>")
}

func worksheetCellInsertPosition(rowXML []byte, wantedColumn int) int {
	cellPattern := regexp.MustCompile(`<(?:[A-Za-z0-9_]+:)?c\b[^>]*\br="([A-Za-z]{1,3}[1-9][0-9]{0,6})"[^>]*>`)
	for _, match := range cellPattern.FindAllSubmatchIndex(rowXML, -1) {
		_, column, err := parseSpreadsheetCellReference(string(rowXML[match[2]:match[3]]))
		if err == nil && column > wantedColumn {
			return match[0]
		}
	}
	closing := regexp.MustCompile(`</(?:[A-Za-z0-9_]+:)?row\s*>`).FindIndex(rowXML)
	if closing == nil {
		return -1
	}
	return closing[0]
}

func worksheetRowInsertPosition(worksheetXML []byte, wantedRow int) int {
	rowPattern := regexp.MustCompile(`<(?:[A-Za-z0-9_]+:)?row\b[^>]*\br="([1-9][0-9]{0,6})"[^>]*>`)
	for _, match := range rowPattern.FindAllSubmatchIndex(worksheetXML, -1) {
		row, _ := strconv.Atoi(string(worksheetXML[match[2]:match[3]]))
		if row > wantedRow {
			return match[0]
		}
	}
	closing := regexp.MustCompile(`</(?:[A-Za-z0-9_]+:)?sheetData\s*>`).FindIndex(worksheetXML)
	if closing == nil {
		return -1
	}
	return closing[0]
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
