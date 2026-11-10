package documents

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestWorksheetInsertionCompatibilitySmoke(t *testing.T) {
	for _, prefix := range []string{"", "x:"} {
		rowXML := []byte(`<` + prefix + `row r="1"><` + prefix + `c r="A1"/><` + prefix + `c r="C1"><v>1</v></` + prefix + `c><` + prefix + `c r="ZZZ1"/></` + prefix + `row>`)
		for _, column := range []int{0, 1, 2, 3, 4, 16384} {
			want := -1
			// 舊版會收集所有 matches；與提早停止的游標版逐項對照。
			for _, match := range worksheetCellOpeningPattern.FindAllSubmatchIndex(rowXML, -1) {
				_, current, err := parseSpreadsheetCellReference(string(rowXML[match[2]:match[3]]))
				if err == nil && current > column {
					want = match[0]
					break
				}
			}
			if want < 0 {
				if closing := worksheetRowClosingPattern.FindIndex(rowXML); closing != nil {
					want = closing[0]
				}
			}
			if got := worksheetCellInsertPosition(rowXML, column); got != want {
				t.Fatalf("欄位插入位置不同：%d != %d", got, want)
			}
		}
		worksheet := []byte(`<sheetData><` + prefix + `row r="1"/><` + prefix + `row r="3"/><` + prefix + `row r="8"/></sheetData>`)
		for _, row := range []int{0, 1, 2, 3, 9} {
			want := -1
			for _, match := range worksheetRowOpeningPattern.FindAllSubmatchIndex(worksheet, -1) {
				current, _ := strconv.Atoi(string(worksheet[match[2]:match[3]]))
				if current > row {
					want = match[0]
					break
				}
			}
			if want < 0 {
				want = worksheetDataClosingPattern.FindIndex(worksheet)[0]
			}
			if got := worksheetRowInsertPosition(worksheet, row); got != want {
				t.Fatalf("列插入位置不同：%d != %d", got, want)
			}
		}
	}
	if worksheetCellInsertPosition([]byte(`<row>`), 1) != -1 || worksheetRowInsertPosition([]byte(`<worksheet>`), 1) != -1 {
		t.Fatal("損壞 XML 不應回傳可插入位置")
	}
}

func TestWorksheetEditPreservesStyleAndOrderSmoke(t *testing.T) {
	data := []byte(`<worksheet><dimension ref="A1:C3"/><sheetData><row r="1"><c r="B1" s="4"><v>1</v></c></row><row r="3"/></sheetData></worksheet>`)
	for _, change := range []struct {
		row, column int
		value       any
	}{{1, 2, "更新"}, {1, 1, "第一欄"}, {2, 3, "新列"}, {4, 4, "擴充"}} {
		var changed bool
		var err error
		data, changed, err = setWorksheetCell(data, change.row, change.column, worksheetCellValue{Value: change.value})
		if err != nil || !changed {
			t.Fatal("更新未成功", err)
		}
	}
	text := string(data)
	if !strings.Contains(text, `<c r="B1" t="inlineStr" s="4"`) || !strings.Contains(text, `<dimension ref="A1:D4"/>`) {
		t.Fatalf("樣式或維度未保留：%s", text)
	}
	if strings.Index(text, `r="A1"`) > strings.Index(text, `r="B1"`) || strings.Index(text, `<row r="2"`) > strings.Index(text, `<row r="3"`) {
		t.Fatal("儲存格或列順序改變")
	}
	data, changed, err := setWorksheetCell(data, 1, 2, worksheetCellValue{})
	if err != nil || !changed || bytes.Contains(data, []byte(`r="B1"`)) {
		t.Fatal("清除儲存格未成功", err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("產出的 XML 無法解碼", err)
		}
	}
}
