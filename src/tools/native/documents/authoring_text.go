package documents

import (
	"context"
	"encoding/csv"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// 純文字類文件與 Office 文件共用同一個工具，不另開一組。
//
// 這幾種格式原本沒有第一級的建立路徑：模型被 Shell 優先政策擋在 file_write 之前，
// 於是「做一份 Markdown 報告」會變成 shell_exec 的 heredoc，中文引號、反引號與
// 換行在那裡最容易出錯。document_create 本來就不受 Shell 優先限制，也已經有
// 覆寫保護與原子寫入，接上去比再造一個工具合理。
//
// 輸入結構完全沿用既有的：blocks 對應 Markdown／HTML／TXT，sheets[].rows 對應 CSV。
// 不新增任何概念，模型知道怎麼產 DOCX 就知道怎麼產 Markdown。

func createMarkdown(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if strings.TrimSpace(request.Title) == "" && len(request.Blocks) == 0 {
		return authoredDocument{}, fmt.Errorf("Markdown requires title or at least one content block")
	}
	var body strings.Builder
	if title := strings.TrimSpace(request.Title); title != "" {
		body.WriteString("# " + title + "\n\n")
	}
	headings, tables, numbered := 0, 0, 0
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
			// 標題已經佔掉 h1，區塊標題往下推一級，文件才只有一個最上層標題。
			body.WriteString(strings.Repeat("#", level+1) + " " + block.Text + "\n\n")
			headings++
		case "paragraph", "":
			body.WriteString(escapeMarkdownBlock(block.Text) + "\n\n")
		case "bullet":
			body.WriteString("- " + block.Text + "\n")
		case "numbered":
			numbered++
			body.WriteString(strconv.Itoa(numbered) + ". " + block.Text + "\n")
		case "table":
			if len(block.Rows) == 0 {
				return authoredDocument{}, fmt.Errorf("block %d: table requires rows", index+1)
			}
			body.WriteString(markdownTable(block.Rows))
			tables++
		case "page_break":
			// Markdown 沒有分頁，用水平線表示段落分界。
			body.WriteString("---\n\n")
		default:
			return authoredDocument{}, fmt.Errorf("block %d: unsupported type %q", index+1, block.Type)
		}
		if isMarkdownListBlock(block.Type) && !nextIsSameList(request.Blocks, index) {
			body.WriteString("\n")
		}
		if !isMarkdownListBlock(block.Type) {
			numbered = resetNumbering(block.Type, numbered)
		}
	}
	return authoredDocument{
		Data: []byte(strings.TrimRight(body.String(), "\n") + "\n"),
		Details: map[string]any{
			"blocks": len(request.Blocks), "headings": headings, "tables": tables,
		},
	}, nil
}

func createPlainText(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if strings.TrimSpace(request.Title) == "" && len(request.Blocks) == 0 {
		return authoredDocument{}, fmt.Errorf("text document requires title or at least one content block")
	}
	var body strings.Builder
	if title := strings.TrimSpace(request.Title); title != "" {
		body.WriteString(title + "\n" + strings.Repeat("=", displayWidth(title)) + "\n\n")
	}
	numbered := 0
	for index, block := range request.Blocks {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "heading":
			body.WriteString(block.Text + "\n" + strings.Repeat("-", displayWidth(block.Text)) + "\n\n")
		case "paragraph", "":
			body.WriteString(block.Text + "\n\n")
		case "bullet":
			body.WriteString("  - " + block.Text + "\n")
		case "numbered":
			numbered++
			body.WriteString("  " + strconv.Itoa(numbered) + ". " + block.Text + "\n")
		case "table":
			if len(block.Rows) == 0 {
				return authoredDocument{}, fmt.Errorf("block %d: table requires rows", index+1)
			}
			body.WriteString(plainTextTable(block.Rows))
		case "page_break":
			body.WriteString("\n")
		default:
			return authoredDocument{}, fmt.Errorf("block %d: unsupported type %q", index+1, block.Type)
		}
		if isMarkdownListBlock(block.Type) && !nextIsSameList(request.Blocks, index) {
			body.WriteString("\n")
		}
		if !isMarkdownListBlock(block.Type) {
			numbered = resetNumbering(block.Type, numbered)
		}
	}
	return authoredDocument{
		Data:    []byte(strings.TrimRight(body.String(), "\n") + "\n"),
		Details: map[string]any{"blocks": len(request.Blocks)},
	}, nil
}

func createHTML(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	if strings.TrimSpace(request.Title) == "" && len(request.Blocks) == 0 {
		return authoredDocument{}, fmt.Errorf("HTML requires title or at least one content block")
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Document"
	}
	var body strings.Builder
	body.WriteString("<!doctype html>\n<html lang=\"zh-Hant\">\n<head>\n<meta charset=\"utf-8\">\n")
	body.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	body.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	if subject := strings.TrimSpace(request.Subject); subject != "" {
		body.WriteString("<meta name=\"description\" content=\"" + html.EscapeString(subject) + "\">\n")
	}
	if author := strings.TrimSpace(request.Author); author != "" {
		body.WriteString("<meta name=\"author\" content=\"" + html.EscapeString(author) + "\">\n")
	}
	// 樣式內嵌且極簡：產出的是可以直接雙擊開啟的單一檔案，不依賴任何外部資源。
	body.WriteString("<style>\nbody { max-width: 46rem; margin: 2rem auto; padding: 0 1rem; font-family: system-ui, sans-serif; line-height: 1.7; }\n")
	body.WriteString("table { border-collapse: collapse; width: 100%; }\nth, td { border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; }\n</style>\n")
	body.WriteString("</head>\n<body>\n")
	if strings.TrimSpace(request.Title) != "" {
		body.WriteString("<h1>" + html.EscapeString(request.Title) + "</h1>\n")
	}
	openList := ""
	closeList := func() {
		if openList != "" {
			body.WriteString("</" + openList + ">\n")
			openList = ""
		}
	}
	tables := 0
	for index, block := range request.Blocks {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		kind := strings.ToLower(strings.TrimSpace(block.Type))
		if kind != "bullet" && kind != "numbered" {
			closeList()
		}
		switch kind {
		case "heading":
			level := block.Level
			if level < 1 || level > 3 {
				level = 1
			}
			tag := "h" + strconv.Itoa(level+1)
			body.WriteString("<" + tag + ">" + html.EscapeString(block.Text) + "</" + tag + ">\n")
		case "paragraph", "":
			body.WriteString("<p>" + html.EscapeString(block.Text) + "</p>\n")
		case "bullet", "numbered":
			wanted := "ul"
			if kind == "numbered" {
				wanted = "ol"
			}
			if openList != wanted {
				closeList()
				body.WriteString("<" + wanted + ">\n")
				openList = wanted
			}
			body.WriteString("<li>" + html.EscapeString(block.Text) + "</li>\n")
		case "table":
			if len(block.Rows) == 0 {
				return authoredDocument{}, fmt.Errorf("block %d: table requires rows", index+1)
			}
			body.WriteString(htmlTable(block.Rows))
			tables++
		case "page_break":
			body.WriteString("<hr>\n")
		default:
			return authoredDocument{}, fmt.Errorf("block %d: unsupported type %q", index+1, block.Type)
		}
	}
	closeList()
	body.WriteString("</body>\n</html>\n")
	return authoredDocument{
		Data:    []byte(body.String()),
		Details: map[string]any{"blocks": len(request.Blocks), "tables": tables},
	}, nil
}

// createCSV 用與 XLSX 相同的 sheets 結構，只是輸出成一張表。
//
// CSV 只有一張表，給多張時明確報錯而不是默默丟掉其餘的——「檔案產出來了但少了
// 兩張表」比直接失敗難發現得多。
func createCSV(ctx context.Context, request documentCreateRequest) (authoredDocument, error) {
	sheets := request.Sheets
	if len(sheets) == 0 && len(request.CellUpdates) > 0 {
		derived, err := sheetsFromCellUpdates(request.CellUpdates)
		if err != nil {
			return authoredDocument{}, err
		}
		sheets = derived
	}
	if len(sheets) == 0 {
		return authoredDocument{}, fmt.Errorf("CSV requires sheets with rows, or cell_updates")
	}
	if len(sheets) > 1 {
		return authoredDocument{}, fmt.Errorf("CSV holds a single table but %d sheets were given; write one file per sheet or use format xlsx", len(sheets))
	}
	rows := sheets[0].Rows
	if len(rows) == 0 {
		return authoredDocument{}, fmt.Errorf("CSV requires at least one row")
	}
	var body strings.Builder
	writer := csv.NewWriter(&body)
	columns := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return authoredDocument{}, err
		}
		record := make([]string, 0, len(row))
		for _, value := range row {
			record = append(record, csvCellText(value))
		}
		if len(record) > columns {
			columns = len(record)
		}
		if err := writer.Write(record); err != nil {
			return authoredDocument{}, fmt.Errorf("encode CSV: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return authoredDocument{}, fmt.Errorf("encode CSV: %w", err)
	}
	return authoredDocument{
		Data:    []byte(body.String()),
		Details: map[string]any{"rows": len(rows), "columns": columns},
	}, nil
}

// csvCellText 把儲存格值轉成文字，數字不要被 %v 變成科學記號。
func csvCellText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return fmt.Sprint(typed)
	}
}

func markdownTable(rows [][]string) string {
	var body strings.Builder
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	for index, row := range rows {
		body.WriteString("|")
		for column := 0; column < columns; column++ {
			cell := ""
			if column < len(row) {
				cell = escapeMarkdownCell(row[column])
			}
			body.WriteString(" " + cell + " |")
		}
		body.WriteString("\n")
		if index == 0 {
			body.WriteString("|")
			for column := 0; column < columns; column++ {
				body.WriteString(" --- |")
			}
			body.WriteString("\n")
		}
	}
	body.WriteString("\n")
	return body.String()
}

func htmlTable(rows [][]string) string {
	var body strings.Builder
	body.WriteString("<table>\n")
	for index, row := range rows {
		body.WriteString("<tr>")
		cellTag := "td"
		if index == 0 {
			cellTag = "th"
		}
		for _, cell := range row {
			body.WriteString("<" + cellTag + ">" + html.EscapeString(cell) + "</" + cellTag + ">")
		}
		body.WriteString("</tr>\n")
	}
	body.WriteString("</table>\n")
	return body.String()
}

// plainTextTable 用固定寬度排版，讓純文字表格在等寬字型下仍然對齊。
func plainTextTable(rows [][]string) string {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	widths := make([]int, columns)
	for _, row := range rows {
		for index, cell := range row {
			if width := displayWidth(cell); width > widths[index] {
				widths[index] = width
			}
		}
	}
	var body strings.Builder
	for _, row := range rows {
		for column := 0; column < columns; column++ {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			body.WriteString(cell)
			if column < columns-1 {
				body.WriteString(strings.Repeat(" ", widths[column]-displayWidth(cell)+2))
			}
		}
		body.WriteString("\n")
	}
	body.WriteString("\n")
	return body.String()
}

// displayWidth 估算等寬字型下的顯示寬度：CJK 與全形符號佔兩格。
//
// 不用 len()：中文一個字三個位元組，用位元組數排版會歪得比不排版還難看。
func displayWidth(text string) int {
	width := 0
	for _, value := range text {
		if value >= 0x1100 && (value <= 0x115F ||
			value == 0x2329 || value == 0x232A ||
			(value >= 0x2E80 && value <= 0xA4CF && value != 0x303F) ||
			(value >= 0xAC00 && value <= 0xD7A3) ||
			(value >= 0xF900 && value <= 0xFAFF) ||
			(value >= 0xFE30 && value <= 0xFE6F) ||
			(value >= 0xFF00 && value <= 0xFF60) ||
			(value >= 0xFFE0 && value <= 0xFFE6) ||
			(value >= 0x20000 && value <= 0x3FFFD)) {
			width += 2
			continue
		}
		width++
	}
	return width
}

// escapeMarkdownBlock 讓段落開頭的符號不會被讀成語法。
//
// 只處理行首：段落中間的 * 或 _ 多半是使用者真的要的字面內容，全部跳脫會讓
// 產出的 Markdown 充滿反斜線。
func escapeMarkdownBlock(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		for _, prefix := range []string{"#", ">", "-", "+", "|"} {
			if strings.HasPrefix(trimmed, prefix) {
				lines[index] = strings.Replace(line, prefix, `\`+prefix, 1)
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// escapeMarkdownCell 讓表格內容裡的 | 與換行不會破壞表格結構。
func escapeMarkdownCell(text string) string {
	text = strings.ReplaceAll(text, "|", `\|`)
	return strings.ReplaceAll(text, "\n", "<br>")
}

func isMarkdownListBlock(blockType string) bool {
	switch strings.ToLower(strings.TrimSpace(blockType)) {
	case "bullet", "numbered":
		return true
	default:
		return false
	}
}

// nextIsSameList 判斷下一個區塊是否延續同一份清單，用來決定要不要空行收尾。
func nextIsSameList(blocks []documentBlock, index int) bool {
	if index+1 >= len(blocks) {
		return false
	}
	current := strings.ToLower(strings.TrimSpace(blocks[index].Type))
	next := strings.ToLower(strings.TrimSpace(blocks[index+1].Type))
	return current == next
}

// resetNumbering 讓編號清單被其他區塊隔開後重新從 1 開始。
func resetNumbering(blockType string, numbered int) int {
	if isMarkdownListBlock(blockType) {
		return numbered
	}
	return 0
}
