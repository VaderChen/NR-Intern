package filestore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"

	"AgenticService/src/domain"
)

func referencePerformanceTerms(value string) map[string]struct{} {
	terms := map[string]struct{}{}
	var word []rune
	flush := func() {
		if len(word) > 0 {
			terms[string(word)] = struct{}{}
			if len(word) <= 128 {
				for index := 0; index+1 < len(word); index++ {
					terms[string(word[index:index+2])] = struct{}{}
				}
			}
		}
		word = nil
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' || character == '-' {
			word = append(word, character)
		} else {
			flush()
		}
	}
	flush()
	return terms
}

func referencePerformanceNormalize(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func TestMemoryTermAndNormalizationCompatibilitySmoke(t *testing.T) {
	samples := []string{"", "ABC_DEF-123 中文詞組🙂", "\t A\u00a0 B \r\nC ", "a\xff\xfe中文\xe4\xb8", strings.Repeat("中", 128), strings.Repeat("中", 129)}
	random := rand.New(rand.NewPCG(3, 4))
	units := []string{"AbC", "中文", "ßİ", " ", "\n\t", "\u3000", "\xff", "12_-"}
	for range 150 {
		var value strings.Builder
		for range random.IntN(80) {
			value.WriteString(units[random.IntN(len(units))])
		}
		samples = append(samples, value.String())
	}
	for _, sample := range samples {
		if got, want := termSet(sample), referencePerformanceTerms(sample); !reflect.DeepEqual(got, want) {
			t.Fatalf("詞彙集合不同：%q got=%v want=%v", sample, got, want)
		}
		if got, want := normalizeText(sample), referencePerformanceNormalize(sample); got != want {
			t.Fatalf("正規化不同：%q got=%q want=%q", sample, got, want)
		}
		value := domain.Memory{Content: sample, Tags: []string{"中文", "ABC", "a a"}}
		for _, query := range []string{"abc 中文", "- 12", "a", "無關詞", sample} {
			wanted := referencePerformanceTerms(query)
			matcher := newMemoryTermMatcher(wanted)
			want := overlapCount(referencePerformanceTerms(sample+" "+strings.Join(value.Tags, " ")), wanted)
			if got := matcher.match(value); got != want {
				t.Fatalf("命中數不同：%q query=%q got=%d want=%d", sample, query, got, want)
			}
			if got := matcher.match(domain.Memory{}); got != 0 {
				t.Fatal("命中標記跨候選殘留")
			}
			matcher.generation = ^uint64(0)
			if got := matcher.match(value); got != want {
				t.Fatal("命中代次溢位未重設")
			}
		}
	}
}

func referencePerformanceSearch(r *MemoryRepository, query domain.MemoryQuery) []domain.Memory {
	limit := query.Limit
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, 100)
	kinds := map[domain.MemoryKind]bool{}
	for _, kind := range query.Kinds {
		kinds[normalizeMemoryKind(kind)] = true
	}
	tags := stringSet(query.Tags)
	text := referencePerformanceNormalize(query.Text)
	terms := referencePerformanceTerms(query.Text)
	var values []scoredMemory
	for _, value := range r.items {
		if value.Scope != strings.TrimSpace(query.Scope) || value.Status != domain.MemoryStatusActive || (len(kinds) > 0 && !kinds[value.Kind]) {
			continue
		}
		matched := overlapCount(stringSet(value.Tags), tags)
		if len(tags) > 0 && matched == 0 {
			continue
		}
		score := float64(matched * 4)
		content := referencePerformanceNormalize(value.Content)
		if text != "" {
			if strings.Contains(content, text) {
				score += 12
			}
			if strings.Contains(text, content) && len([]rune(content)) > 4 {
				score += 5
			}
			score += float64(2 * overlapCount(referencePerformanceTerms(value.Content+" "+strings.Join(value.Tags, " ")), terms))
			if score == 0 {
				continue
			}
		}
		values = append(values, scoredMemory{value: value, score: score + value.Confidence})
	}
	sort.Slice(values, func(i, j int) bool { return betterMemory(values[i], values[j]) })
	values = values[:min(limit, len(values))]
	result := make([]domain.Memory, len(values))
	for i, value := range values {
		result[i] = cloneMemory(value.value)
	}
	return result
}

func TestMemoryTopKCompatibilitySmoke(t *testing.T) {
	r := syntheticPerformanceMemories(500)
	i := 0
	for id, value := range r.items {
		value.Kind = []domain.MemoryKind{domain.MemoryKindFact, domain.MemoryKindDecision}[i%2]
		value.Content = []string{"Synthetic memory", "記憶體 效率最佳化", "", "不同內容", strings.Repeat("a", 129)}[i%5]
		value.Tags = []string{[]string{"共同標籤", "Other"}[i%2]}
		value.Confidence = float64(i%10) / 10
		if i%9 == 0 {
			value.Scope = "other"
		}
		if i%7 == 0 {
			value.Status = domain.MemoryStatusForgotten
		}
		r.items[id] = value
		i++
	}
	for _, text := range []string{"", "SYNTHETIC", "效率最佳化", "未命中", "!!!"} {
		for _, limit := range []int{-1, 1, 8, 100, 150} {
			for _, tags := range [][]string{nil, {" 共同標籤 "}, {"absent"}} {
				for _, kinds := range [][]domain.MemoryKind{nil, {domain.MemoryKindFact}} {
					query := domain.MemoryQuery{Scope: " benchmark ", Text: text, Limit: limit, Tags: tags, Kinds: kinds}
					got, err := r.Search(context.Background(), query)
					if err != nil || !reflect.DeepEqual(got, referencePerformanceSearch(r, query)) {
						t.Fatalf("搜尋結果不相容 query=%+v err=%v", query, err)
					}
				}
			}
		}
	}
	values, err := r.Search(context.Background(), domain.MemoryQuery{Scope: "benchmark"})
	if err != nil || len(values) == 0 {
		t.Fatalf("搜尋失敗：%v", err)
	}
	values[0].Tags[0] = "changed"
	values[0].Metadata["changed"] = true
	if r.items[values[0].ID].Tags[0] == "changed" || r.items[values[0].ID].Metadata["changed"] != nil {
		t.Fatal("回傳的搜尋結果與 repository 共用可變容器")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Search(ctx, domain.MemoryQuery{Scope: "benchmark"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未傳遞：%v", err)
	}
}

func TestIndexCacheBoundAndRecencySmoke(t *testing.T) {
	var cache indexCache[int]
	for i := range maxCachedFileIndexes {
		cache.set(fmt.Sprint(i), i)
	}
	if value, ok := cache.get("0"); !ok || value != 0 {
		t.Fatal("快取遺失")
	}
	cache.set("new", 123)
	if len(cache.items) != maxCachedFileIndexes || cache.order.Len() != maxCachedFileIndexes {
		t.Fatal("索引快取超過上限")
	}
	if _, ok := cache.get("1"); ok {
		t.Fatal("未淘汰最舊索引")
	}
	if _, ok := cache.get("0"); !ok {
		t.Fatal("剛讀取的索引不應淘汰")
	}
	cache.set("0", 456)
	if value, _ := cache.get("0"); value != 456 {
		t.Fatal("快取更新失敗")
	}
	cache.delete("0")
	cache.delete("missing")
	if len(cache.items) != cache.order.Len() || len(cache.items) != maxCachedFileIndexes-1 {
		t.Fatal("刪除後 LRU 與索引不一致")
	}
}

func TestSessionIndexEvictionResumeSmoke(t *testing.T) {
	r, id := seededSession(t, 5)
	r.mu.Lock()
	for i := range maxCachedFileIndexes {
		r.indexes.set(fmt.Sprintf("synthetic-%d", i), &entryIndex{})
	}
	_, cached := r.indexes.get(id)
	r.mu.Unlock()
	if cached {
		t.Fatal("測試索引應已淘汰")
	}
	entry, err := r.AppendEntry(context.Background(), id, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
	if err != nil || entry.Sequence != 6 {
		t.Fatalf("淘汰後續寫序號=%d err=%v", entry.Sequence, err)
	}
	values, err := r.ListEntriesAfter(context.Background(), id, 3)
	if err != nil || len(values) != 3 || values[0].Sequence != 4 || values[2].Sequence != 6 {
		t.Fatalf("淘汰後歷史讀取=%+v err=%v", values, err)
	}
}

func TestEventIndexEvictionResumeSmoke(t *testing.T) {
	r, err := NewRunEventRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 5; i++ {
		if err := r.Append(context.Background(), domain.Event{ID: fmt.Sprint(i), Type: "synthetic", RunID: "run_synthetic", Sequence: i}); err != nil {
			t.Fatal(err)
		}
	}
	r.mu.Lock()
	for i := range maxCachedFileIndexes {
		r.indexes.set(fmt.Sprintf("synthetic-%d", i), &eventOffsetIndex{})
	}
	_, cached := r.indexes.get("run_synthetic")
	r.mu.Unlock()
	if cached {
		t.Fatal("測試索引應已淘汰")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path, err := r.eventPath("run_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	lock := r.runLock("run_synthetic")
	lock.Lock()
	_, err = r.lastSequenceLocked(ctx, "run_synthetic", path)
	lock.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("淘汰後索引重建未接受取消：%v", err)
	}
	if err := r.Append(context.Background(), domain.Event{ID: "6", Type: "synthetic", RunID: "run_synthetic", Sequence: 6}); err != nil {
		t.Fatal(err)
	}
	values, err := r.List(context.Background(), "run_synthetic", 3)
	if err != nil || len(values) != 3 || values[0].Sequence != 4 || values[2].Sequence != 6 {
		t.Fatalf("淘汰後事件重播=%+v err=%v", values, err)
	}
}

func TestEventIndexEvictionConcurrencySmoke(t *testing.T) {
	r, err := NewRunEventRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	group.Add(3)
	go func() {
		defer group.Done()
		for i := int64(1); i <= 40; i++ {
			if err := r.Append(context.Background(), domain.Event{ID: fmt.Sprint(i), Type: "synthetic", RunID: "run_concurrent", Sequence: i}); err != nil {
				t.Errorf("並行追加：%v", err)
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		for range 40 {
			values, err := r.List(context.Background(), "run_concurrent", 0)
			if err != nil {
				t.Errorf("並行讀取：%v", err)
				return
			}
			for i, value := range values {
				if value.Sequence != int64(i+1) {
					t.Errorf("並行讀取序號不連續：%d", value.Sequence)
					return
				}
			}
		}
	}()
	go func() {
		defer group.Done()
		for i := range maxCachedFileIndexes * 8 {
			r.mu.Lock()
			r.indexes.set(fmt.Sprintf("synthetic-%d", i), &eventOffsetIndex{})
			r.mu.Unlock()
		}
	}()
	group.Wait()
	values, err := r.List(context.Background(), "run_concurrent", 0)
	if err != nil || len(values) != 40 {
		t.Fatalf("淘汰與追加交錯後資料遺失：count=%d err=%v", len(values), err)
	}
}
