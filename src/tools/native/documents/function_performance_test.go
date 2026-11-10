package documents

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkFunctionWorksheetInsertion(b *testing.B) {
	var row strings.Builder
	row.WriteString(`<row r="1">`)
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&row, `<c r="%s1"><v>1</v></c>`, spreadsheetColumnName(i))
	}
	row.WriteString(`</row>`)
	data := []byte(row.String())
	b.ReportAllocs()
	for b.Loop() {
		if worksheetCellInsertPosition(data, 1) < 0 {
			b.Fatal("無法找到儲存格插入位置")
		}
	}
}
