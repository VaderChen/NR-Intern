package memory

import (
	"strings"
	"testing"
)

func BenchmarkFunctionMemoryQueryTerms(b *testing.B) {
	text := strings.Repeat("合成文字查詢庫存 synthetic inventory record ", 256)
	b.ReportAllocs()
	for b.Loop() {
		if len(queryTerms(text)) == 0 {
			b.Fatal("未產生詞彙")
		}
	}
}
