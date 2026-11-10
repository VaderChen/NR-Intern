package filestore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"AgenticService/src/domain"
)

func syntheticPerformanceMemories(count int) *MemoryRepository {
	r := &MemoryRepository{items: make(map[string]domain.Memory, count)}
	for i := range count {
		id := fmt.Sprintf("synthetic-%06d", i)
		r.items[id] = domain.Memory{ID: id, Scope: "benchmark", Status: domain.MemoryStatusActive, Content: strings.Repeat("synthetic memory content ", 16), Tags: []string{"synthetic", "memory"}, Confidence: 0.8, UpdatedAt: time.Unix(int64(i), 0), Metadata: map[string]any{"nested": map[string]any{"source": "synthetic"}}}
	}
	return r
}

func BenchmarkPerformanceMemorySearch(b *testing.B) {
	r := syntheticPerformanceMemories(10_000)
	for _, query := range []struct{ name, text string }{{"Recent", ""}, {"Text", "synthetic memory"}} {
		b.Run(query.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				values, err := r.Search(ctx, domain.MemoryQuery{Scope: "benchmark", Text: query.text, Limit: 8})
				if err != nil || len(values) != 8 {
					b.Fatalf("搜尋結果數量=%d err=%v", len(values), err)
				}
			}
		})
	}
}
