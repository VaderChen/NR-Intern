package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
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
	previous := cloneMemories(r.items)
	for id, existing := range r.items {
		if existing.Scope != input.Scope || existing.Kind != input.Kind || existing.Status != domain.MemoryStatusActive || normalizeText(existing.Content) != normalizedContent {
			continue
		}
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
			r.items = previous
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
	for _, supersededID := range input.Supersedes {
		existing, ok := r.items[supersededID]
		if !ok || existing.Scope != input.Scope || existing.Status != domain.MemoryStatusActive {
			continue
		}
		existing.Status = domain.MemoryStatusSuperseded
		existing.SupersededBy = value.ID
		existing.UpdatedAt = now
		r.items[existing.ID] = existing
	}
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
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
	queryText := normalizeText(query.Text)
	queryTerms := termSet(query.Text)

	r.mu.RLock()
	scored := make([]scoredMemory, 0, len(r.items))
	for _, value := range r.items {
		if err := ctx.Err(); err != nil {
			r.mu.RUnlock()
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
		tagMatches := overlapCount(stringSet(value.Tags), stringSet(wantedTags))
		if len(wantedTags) > 0 && tagMatches == 0 {
			continue
		}
		score := float64(tagMatches * 4)
		content := normalizeText(value.Content)
		if queryText != "" {
			termMatches := overlapCount(termSet(value.Content+" "+strings.Join(value.Tags, " ")), queryTerms)
			if strings.Contains(content, queryText) {
				score += 12
			}
			if strings.Contains(queryText, content) && len([]rune(content)) > 4 {
				score += 5
			}
			score += float64(termMatches * 2)
			if score == 0 {
				continue
			}
		}
		score += value.Confidence
		scored = append(scored, scoredMemory{value: cloneMemory(value), score: score})
	}
	r.mu.RUnlock()
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].value.UpdatedAt.After(scored[j].value.UpdatedAt)
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]domain.Memory, len(scored))
	for index, item := range scored {
		result[index] = item.value
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
	previous := cloneMemories(r.items)
	value, ok := r.items[strings.TrimSpace(id)]
	if !ok || value.Scope != strings.TrimSpace(scope) {
		return domain.Memory{}, fmt.Errorf("%w: memory %q", domain.ErrNotFound, id)
	}
	if value.Status == domain.MemoryStatusForgotten {
		return cloneMemory(value), nil
	}
	now := time.Now().UTC()
	value.Status = domain.MemoryStatusForgotten
	value.ForgetReason = strings.TrimSpace(reason)
	value.ForgottenAt = &now
	value.UpdatedAt = now
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
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
	previous := cloneMemories(r.items)
	removed := 0
	for id, value := range r.items {
		if value.Status == domain.MemoryStatusActive || !value.UpdatedAt.Before(before) {
			continue
		}
		delete(r.items, id)
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return 0, err
	}
	return removed, nil
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
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func termSet(value string) map[string]struct{} {
	result := map[string]struct{}{}
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		text := string(word)
		result[text] = struct{}{}
		if len(word) <= 128 {
			for index := 0; index+1 < len(word); index++ {
				result[string(word[index:index+2])] = struct{}{}
			}
		}
		word = nil
	}
	for _, value := range []rune(strings.ToLower(value)) {
		if unicode.IsLetter(value) || unicode.IsNumber(value) || value == '_' || value == '-' {
			word = append(word, value)
			continue
		}
		flush()
	}
	flush()
	return result
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

func cloneMemories(values map[string]domain.Memory) map[string]domain.Memory {
	result := make(map[string]domain.Memory, len(values))
	for key, value := range values {
		result[key] = cloneMemory(value)
	}
	return result
}
