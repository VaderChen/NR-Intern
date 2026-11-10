package harness

import (
	"reflect"
	"sort"
	"sync"
	"testing"

	"AgenticService/src/domain"
)

func referenceFunctionToolSearch(retriever *toolRetriever, query string, limit int) []domain.ToolDefinition {
	if limit <= 0 {
		return nil
	}
	type match struct {
		definition domain.ToolDefinition
		score      float64
	}
	terms := map[string]bool{}
	for _, term := range tokenizeForRetrieval(query) {
		terms[term] = true
	}
	if len(terms) == 0 {
		return nil
	}
	var matches []match
	for _, entry := range retriever.entries {
		score := 0.0
		for term := range terms {
			score += entry.terms[term] * retriever.idf[term]
		}
		if score > 0 {
			matches = append(matches, match{entry.definition, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].definition.Name < matches[j].definition.Name
	})
	values := make([]domain.ToolDefinition, 0, min(limit, len(matches)))
	for _, value := range matches[:min(limit, len(matches))] {
		values = append(values, value.definition)
	}
	return values
}

func TestToolSearchTopKCompatibilitySmoke(t *testing.T) {
	catalog := functionRetrievalCatalog(128)
	// 同分同名時仍依原始順序；Label 用來辨識沒有被錯換的候選。
	catalog[4].Name = catalog[0].Name
	catalog[4].Label, catalog[0].Label = "second", "first"
	r := newToolRetriever(catalog, "inventory", true)
	for _, query := range []string{"", "inventory", "庫存", "synthetic", "absent"} {
		for _, limit := range []int{-1, 0, 1, 6, 8, 128, 256} {
			got, want := r.search(query, limit), referenceFunctionToolSearch(r, query, limit)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("檢索排序不同：query=%s limit=%d", query, limit)
			}
		}
	}
}

func TestToolRetrievalLazyIndexConcurrentSmoke(t *testing.T) {
	catalog := functionRetrievalCatalog(64)
	r := newToolRetriever(catalog, "inventory", false)
	if r.idf != nil {
		t.Fatal("關閉檢索時不應建立索引")
	}
	for _, entry := range r.entries {
		if entry.terms != nil {
			t.Fatal("關閉時仍分詞")
		}
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 20 {
				if got := r.search("inventory", 8); len(got) != 8 {
					t.Errorf("搜尋結果數量=%d", len(got))
				}
				if len(r.stage(catalog)) != len(catalog) {
					t.Error("關閉時仍過濾目錄")
				}
			}
		})
	}
	group.Wait()
	active := newToolRetriever(catalog, "inventory", true)
	for range 8 {
		group.Go(func() {
			for range 20 {
				active.reveal(catalog[1].Name)
				_ = active.stage(catalog)
				_ = active.recognizable(nil)
				_ = active.search("庫存", 6)
			}
		})
	}
	group.Wait()
}
