package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var _ ports.MemoryRepository = (*MemoryRepository)(nil)

type MemoryRepository struct {
	mu       sync.RWMutex
	filePath string
	items    map[string]domain.Memory
}

type memoryFile struct {
	Version int                      `json:"version"`
	Items   map[string]domain.Memory `json:"items"`
}

type scoredMemory struct {
	value domain.Memory
	score float64
}

// 最差候選放在根節點；只保留使用者要求的前 limit 筆，不複製整個資料庫。
type memorySearchHeap []scoredMemory

func (h memorySearchHeap) Len() int           { return len(h) }
func (h memorySearchHeap) Less(i, j int) bool { return betterMemory(h[j], h[i]) }
func (h memorySearchHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *memorySearchHeap) Push(value any)    { *h = append(*h, value.(scoredMemory)) }
func (h *memorySearchHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = scoredMemory{}
	*h = (*h)[:last]
	return value
}

func betterMemory(left, right scoredMemory) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	if !left.value.UpdatedAt.Equal(right.value.UpdatedAt) {
		return left.value.UpdatedAt.After(right.value.UpdatedAt)
	}
	return left.value.ID < right.value.ID
}

func NewMemoryRepository(dataDir string) (*MemoryRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "memory")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create memory store: %w", err)
	}
	repository := &MemoryRepository{
		filePath: filepath.Join(root, "memories.json"),
		items:    map[string]domain.Memory{},
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (r *MemoryRepository) Remember(ctx context.Context, input domain.RememberMemoryInput) (domain.Memory, error) {
	if err := ctx.Err(); err != nil {
		return domain.Memory{}, err
	}
	input.Scope = strings.TrimSpace(input.Scope)
	input.Content = strings.TrimSpace(input.Content)
	input.Kind = normalizeMemoryKind(input.Kind)
	if input.Scope == "" || input.Content == "" {
		return domain.Memory{}, fmt.Errorf("%w: memory scope and content are required", domain.ErrInvalidInput)
	}
	if !validMemoryKind(input.Kind) {
		return domain.Memory{}, fmt.Errorf("%w: unsupported memory kind %q", domain.ErrInvalidInput, input.Kind)
	}
	if input.Confidence <= 0 {
		input.Confidence = 0.8
	}
	if input.Confidence > 1 {
		input.Confidence = 1
	}
	input.Tags = normalizeStrings(input.Tags)
	input.Supersedes = normalizeStrings(input.Supersedes)
	normalizedContent := normalizeText(input.Content)
	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()
	for id, existing := range r.items {
		if existing.Scope != input.Scope || existing.Kind != input.Kind || existing.Status != domain.MemoryStatusActive || normalizeText(existing.Content) != normalizedContent {
			continue
		}
		// 更新使用新容器，不改動原紀錄的切片／map；回復只需保留被改動的項目。
		previous := existing
		existing.Content = input.Content
		existing.Tags = mergeStrings(existing.Tags, input.Tags)
		if input.Confidence > existing.Confidence {
			existing.Confidence = input.Confidence
		}
		existing.SourceSessionID = firstValue(input.SourceSessionID, existing.SourceSessionID)
		existing.SourceMessageID = firstValue(input.SourceMessageID, existing.SourceMessageID)
		existing.Metadata = mergeMetadata(existing.Metadata, input.Metadata)
		existing.UpdatedAt = now
		r.items[id] = existing
		if err := r.persistLocked(); err != nil {
			r.items[id] = previous
			return domain.Memory{}, err
		}
		return cloneMemory(existing), nil
	}

	value := domain.Memory{
		ID:              domain.NewID("memory"),
		Scope:           input.Scope,
		Kind:            input.Kind,
		Content:         input.Content,
		Tags:            append([]string(nil), input.Tags...),
		Confidence:      input.Confidence,
		Status:          domain.MemoryStatusActive,
		SourceSessionID: strings.TrimSpace(input.SourceSessionID),
		SourceMessageID: strings.TrimSpace(input.SourceMessageID),
		Supersedes:      append([]string(nil), input.Supersedes...),
		Metadata:        valueutil.CloneMap(input.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	type originalItem struct {
		value  domain.Memory
		exists bool
	}
	var previous map[string]originalItem
	for _, supersededID := range input.Supersedes {
		existing, ok := r.items[supersededID]
		if !ok || existing.Scope != input.Scope || existing.Status != domain.MemoryStatusActive {
			continue
		}
		if previous == nil {
			previous = make(map[string]originalItem)
		}
		if _, captured := previous[existing.ID]; !captured {
			original, exists := r.items[existing.ID]
			previous[existing.ID] = originalItem{value: original, exists: exists}
		}
		existing.Status = domain.MemoryStatusSuperseded
		existing.SupersededBy = value.ID
		existing.UpdatedAt = now
		r.items[existing.ID] = existing
	}
	displaced, displacedExists := r.items[value.ID]
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		if displacedExists {
			r.items[value.ID] = displaced
		} else {
			delete(r.items, value.ID)
		}
		for id, original := range previous {
			if original.exists {
				r.items[id] = original.value
			} else {
				delete(r.items, id)
			}
		}
		return domain.Memory{}, err
	}
	return cloneMemory(value), nil
}

func (r *MemoryRepository) Get(ctx context.Context, scope, id string) (domain.Memory, error) {
	if err := ctx.Err(); err != nil {
		return domain.Memory{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.items[strings.TrimSpace(id)]
	if !ok || value.Scope != strings.TrimSpace(scope) {
		return domain.Memory{}, fmt.Errorf("%w: memory %q", domain.ErrNotFound, id)
	}
	return cloneMemory(value), nil
}

func (r *MemoryRepository) Search(ctx context.Context, query domain.MemoryQuery) ([]domain.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query.Scope = strings.TrimSpace(query.Scope)
	if query.Scope == "" {
		return nil, fmt.Errorf("%w: memory scope is required", domain.ErrInvalidInput)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 100 {
		limit = 100
	}
	wantedKinds := map[domain.MemoryKind]struct{}{}
	for _, kind := range query.Kinds {
		wantedKinds[normalizeMemoryKind(kind)] = struct{}{}
	}
	wantedTags := normalizeStrings(query.Tags)
	wantedTagSet := stringSet(wantedTags)
	queryText := normalizeText(query.Text)
	queryTerms := termSet(query.Text)
	matcher := newMemoryTermMatcher(queryTerms)

	r.mu.RLock()
	defer r.mu.RUnlock()
	scored := make(memorySearchHeap, 0, min(limit, len(r.items)))
	for _, value := range r.items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if value.Scope != query.Scope || value.Status != domain.MemoryStatusActive {
			continue
		}
		if len(wantedKinds) > 0 {
			if _, ok := wantedKinds[value.Kind]; !ok {
				continue
			}
		}
		tagMatches := 0
		if len(wantedTags) > 0 {
			tagMatches = overlapCount(stringSet(value.Tags), wantedTagSet)
		}
		if len(wantedTags) > 0 && tagMatches == 0 {
			continue
		}
		score := float64(tagMatches * 4)
		if queryText != "" {
			content := normalizeText(value.Content)
			termMatches := matcher.match(value)
			if strings.Contains(content, queryText) {
				score += 12
			}
			if strings.Contains(queryText, content) && utf8.RuneCountInString(content) > 4 {
				score += 5
			}
			score += float64(termMatches * 2)
			if score == 0 {
				continue
			}
		}
		score += value.Confidence
		candidate := scoredMemory{value: value, score: score}
		if len(scored) < limit {
			heap.Push(&scored, candidate)
		} else if betterMemory(candidate, scored[0]) {
			scored[0] = candidate
			heap.Fix(&scored, 0)
		}
	}
	sort.Slice(scored, func(i, j int) bool { return betterMemory(scored[i], scored[j]) })
	result := make([]domain.Memory, len(scored))
	for index, item := range scored {
		// 持有讀鎖直到結果完成複製，避免淘汰／遺忘與回傳內容交錯。
		result[index] = cloneMemory(item.value)
	}
	return result, nil
}

// Scopes 回傳目前有生效記憶的所有 scope，依名稱排序。
func (r *MemoryRepository) Scopes(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	r.mu.RLock()
	for _, value := range r.items {
		if value.Status != domain.MemoryStatusActive {
			continue
		}
		seen[value.Scope] = true
	}
	r.mu.RUnlock()
	result := make([]string, 0, len(seen))
	for scope := range seen {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result, nil
}

// ListScope 回傳 scope 內所有仍生效的記憶，依建立時間排序。
func (r *MemoryRepository) ListScope(ctx context.Context, scope string) ([]domain.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("%w: memory scope is required", domain.ErrInvalidInput)
	}
	r.mu.RLock()
	result := make([]domain.Memory, 0, len(r.items))
	for _, value := range r.items {
		if value.Scope != scope || value.Status != domain.MemoryStatusActive {
			continue
		}
		result = append(result, cloneMemory(value))
	}
	r.mu.RUnlock()
	sort.SliceStable(result, func(first, second int) bool {
		return result[first].CreatedAt.Before(result[second].CreatedAt)
	})
	return result, nil
}

func (r *MemoryRepository) Forget(ctx context.Context, scope, id, reason string) (domain.Memory, error) {
	if err := ctx.Err(); err != nil {
		return domain.Memory{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.items[strings.TrimSpace(id)]
	if !ok || value.Scope != strings.TrimSpace(scope) {
		return domain.Memory{}, fmt.Errorf("%w: memory %q", domain.ErrNotFound, id)
	}
	if value.Status == domain.MemoryStatusForgotten {
		return cloneMemory(value), nil
	}
	previous, previousExists := r.items[value.ID]
	now := time.Now().UTC()
	value.Status = domain.MemoryStatusForgotten
	value.ForgetReason = strings.TrimSpace(reason)
	value.ForgottenAt = &now
	value.UpdatedAt = now
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		if previousExists {
			r.items[value.ID] = previous
		} else {
			delete(r.items, value.ID)
		}
		return domain.Memory{}, err
	}
	return cloneMemory(value), nil
}

// PurgeInactive 永久移除在 before 之前就已失效的記憶，回傳移除筆數。
//
// forgotten 與 superseded 不會被召回，也不佔 scope 上限，但它們留在檔案裡，而這個
// 檔案每次寫入都整份重寫——留越久，每次寫記憶就越慢。保留一段時間是為了稽核
// 「當時到底記了什麼」，過了那段時間就沒有理由再扛著。
func (r *MemoryRepository) PurgeInactive(ctx context.Context, before time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed map[string]domain.Memory
	for id, value := range r.items {
		if value.Status == domain.MemoryStatusActive || !value.UpdatedAt.Before(before) {
			continue
		}
		if removed == nil {
			removed = make(map[string]domain.Memory)
		}
		removed[id] = value
		delete(r.items, id)
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if err := r.persistLocked(); err != nil {
		for id, value := range removed {
			r.items[id] = value
		}
		return 0, err
	}
	return len(removed), nil
}

func (r *MemoryRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read memory store: %w", err)
	}
	var snapshot memoryFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode memory store: %w", err)
	}
	if snapshot.Items != nil {
		r.items = snapshot.Items
	}
	return nil
}

// persistableMemoriesLocked 回傳可以寫進 memories.json 的記憶。
//
// 記憶體隔離對話寫下的記憶不落地：內容是該次對話的內容摘要，正是這個功能
// 不想留在硬碟上的東西。memories.json 是單一檔案，沒辦法像 transcript 那樣
// 按根目錄分流，所以在寫入時濾掉；記憶體中仍然完整，本次執行期間的召回、
// 搜尋與遺忘都不受影響，程序結束就消失。
//
// 只看 SourceSessionID 是安全的，因為隔離對話有自己的 scope（見
// memory.ScopeForSessionWithSpace），不會就地改寫別人寫的記憶。
//
// 沒有隔離記憶時直接回傳原 map，避免每次寫入都多複製一份。
func (r *MemoryRepository) persistableMemoriesLocked() map[string]domain.Memory {
	volatile := 0
	for _, item := range r.items {
		if domain.EphemeralProjectCodeFromID(item.SourceSessionID) != "" {
			volatile++
		}
	}
	if volatile == 0 {
		return r.items
	}
	values := make(map[string]domain.Memory, len(r.items)-volatile)
	for id, item := range r.items {
		if domain.EphemeralProjectCodeFromID(item.SourceSessionID) != "" {
			continue
		}
		values[id] = item
	}
	return values
}

func (r *MemoryRepository) persistLocked() error {
	data, err := json.MarshalIndent(memoryFile{Version: 1, Items: r.persistableMemoriesLocked()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory store: %w", err)
	}
	temporary := r.filePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write memory store: %w", err)
	}
	if err := replaceFile(temporary, r.filePath); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace memory store: %w", err)
	}
	return nil
}

func normalizeMemoryKind(kind domain.MemoryKind) domain.MemoryKind {
	return domain.MemoryKind(strings.ToLower(strings.TrimSpace(string(kind))))
}

func validMemoryKind(kind domain.MemoryKind) bool {
	switch kind {
	case domain.MemoryKindFact, domain.MemoryKindPreference, domain.MemoryKindDecision, domain.MemoryKindProcedure, domain.MemoryKindConstraint:
		return true
	default:
		return false
	}
}

func normalizeText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	previousSpace := false
	for _, character := range value {
		space := unicode.IsSpace(character)
		if space && (character != ' ' || previousSpace) {
			var output strings.Builder
			output.Grow(len(value))
			for field := range strings.FieldsSeq(value) {
				if output.Len() > 0 {
					output.WriteByte(' ')
				}
				output.WriteString(field)
			}
			return output.String()
		}
		previousSpace = space
	}
	return value
}

func termSet(value string) map[string]struct{} {
	result := map[string]struct{}{}
	visitMemoryTerms(value, func(term string) bool { result[term] = struct{}{}; return true })
	return result
}

// 逐詞拜訪，保留既有「完整詞＋不超過 128 字的雙字詞」語意。
// 以 UTF-8 邊界切片，不為每個字／詞配置 []rune 與暫存字串。
func visitMemoryTerms(value string, visit func(string) bool) {
	value = strings.ToLower(value)
	start, count := -1, 0
	flush := func(end int) bool {
		if start < 0 {
			return true
		}
		word := value[start:end]
		if !visit(word) {
			return false
		}
		if count <= 128 {
			previous := -1
			for index, character := range word {
				if previous >= 0 && !visit(word[previous:index+utf8.RuneLen(character)]) {
					return false
				}
				previous = index
			}
		}
		start, count = -1, 0
		return true
	}
	for index, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' || character == '-' {
			if start < 0 {
				start = index
			}
			count++
		} else if !flush(index) {
			return
		}
	}
	flush(len(value))
}

// 每次搜尋共用一份命中標記；不為資料庫裡的每一筆記憶配置一個 map。
// 代次只活在單一 Search 呼叫，並行搜尋不共用可變狀態。
type memoryTermMatcher struct {
	indexes    map[string]int
	seen       []uint64
	generation uint64
}

func newMemoryTermMatcher(wanted map[string]struct{}) memoryTermMatcher {
	matcher := memoryTermMatcher{indexes: make(map[string]int, len(wanted)), seen: make([]uint64, len(wanted))}
	for term := range wanted {
		matcher.indexes[term] = len(matcher.indexes)
	}
	return matcher
}

func (m *memoryTermMatcher) match(value domain.Memory) int {
	if len(m.indexes) == 0 {
		return 0
	}
	m.generation++
	if m.generation == 0 {
		clear(m.seen)
		m.generation = 1
	}
	matched := 0
	visit := func(term string) bool {
		if index, ok := m.indexes[term]; ok && m.seen[index] != m.generation {
			m.seen[index] = m.generation
			matched++
		}
		return matched < len(m.indexes)
	}
	visitMemoryTerms(value.Content, visit)
	for _, tag := range value.Tags {
		if matched == len(m.indexes) {
			break
		}
		visitMemoryTerms(tag, visit)
	}
	return matched
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = normalizeText(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func overlapCount(left, right map[string]struct{}) int {
	count := 0
	for value := range left {
		if _, ok := right[value]; ok {
			count++
		}
	}
	return count
}

func normalizeStrings(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func mergeStrings(left, right []string) []string {
	return normalizeStrings(append(append([]string(nil), left...), right...))
}

func mergeMetadata(left, right map[string]any) map[string]any {
	result := valueutil.CloneMap(left)
	if result == nil && len(right) > 0 {
		result = map[string]any{}
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneMemory(value domain.Memory) domain.Memory {
	result := value
	result.Tags = append([]string(nil), value.Tags...)
	result.Supersedes = append([]string(nil), value.Supersedes...)
	result.Metadata = valueutil.CloneMap(value.Metadata)
	if value.LastAccessedAt != nil {
		copyTime := *value.LastAccessedAt
		result.LastAccessedAt = &copyTime
	}
	if value.ForgottenAt != nil {
		copyTime := *value.ForgottenAt
		result.ForgottenAt = &copyTime
	}
	return result
}
