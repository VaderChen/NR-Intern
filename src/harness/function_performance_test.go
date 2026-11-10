package harness

import (
	"fmt"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

func functionRetrievalCatalog(count int) []domain.ToolDefinition {
	values := make([]domain.ToolDefinition, count)
	for i := range values {
		description := strings.Repeat("合成工具說明 synthetic fixture description ", 16)
		if i%4 == 0 {
			description += " inventory 庫存"
		}
		values[i] = domain.ToolDefinition{Name: fmt.Sprintf("synthetic_%05d", i), Description: description, ReadOnly: true}
	}
	return values
}

func BenchmarkFunctionToolRetrieval(b *testing.B) {
	for _, config := range []struct {
		name    string
		count   int
		enabled bool
	}{
		{"Disabled256", 256, false}, {"Small12", 12, true}, {"Enabled256", 256, true},
	} {
		b.Run(config.name, func(b *testing.B) {
			catalog := functionRetrievalCatalog(config.count)
			b.ReportAllocs()
			for b.Loop() {
				_ = newToolRetriever(catalog, "inventory", config.enabled)
			}
		})
	}
	b.Run("Search4096", func(b *testing.B) {
		retriever := newToolRetriever(functionRetrievalCatalog(4096), "inventory", true)
		b.ReportAllocs()
		for b.Loop() {
			if got := retriever.search("inventory", 8); len(got) != 8 {
				b.Fatalf("結果數量=%d", len(got))
			}
		}
	})
	b.Run("Stage256", func(b *testing.B) {
		catalog := functionRetrievalCatalog(256)
		retriever := newToolRetriever(catalog, "inventory", true)
		b.ReportAllocs()
		for b.Loop() {
			_ = retriever.stage(catalog)
		}
	})
}
