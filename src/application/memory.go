package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
)

// DefaultMemoryScope 是未指定 scope 時使用的預設值，與 Harness 對 Session 的
// 預設 scope（Agent ID）一致。
const DefaultMemoryScope = "general-agent"

// 長期記憶過去只能透過 Agent 工具存取：使用者無法檢視 Agent 記住了什麼、
// 也無法在 Agent 記錯時自行更正。這組 use case 把同一個 repository 開放給
// 管理介面，讀寫都走與工具相同的 scope 隔離。

func (s *Service) memoryRepository() (ports.MemoryRepository, error) {
	if s.memories == nil {
		return nil, fmt.Errorf("%w: long-term memory is disabled on this backend", domain.ErrConflict)
	}
	return s.memories, nil
}

func (s *Service) SearchMemories(ctx context.Context, query domain.MemoryQuery) ([]domain.Memory, error) {
	repository, err := s.memoryRepository()
	if err != nil {
		return nil, err
	}
	query.Scope = normalizeMemoryScope(query.Scope)
	if query.Limit <= 0 || query.Limit > 200 {
		query.Limit = 50
	}
	return repository.Search(ctx, query)
}

func (s *Service) GetMemory(ctx context.Context, scope, memoryID string) (domain.Memory, error) {
	repository, err := s.memoryRepository()
	if err != nil {
		return domain.Memory{}, err
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return domain.Memory{}, fmt.Errorf("%w: memory id is required", domain.ErrInvalidInput)
	}
	return repository.Get(ctx, normalizeMemoryScope(scope), memoryID)
}

// RememberMemory 讓使用者直接寫入長期記憶。來源標記為 operator，
// 以便與 Agent 自行寫入的記憶區分。
func (s *Service) RememberMemory(ctx context.Context, input domain.RememberMemoryInput) (domain.Memory, error) {
	repository, err := s.memoryRepository()
	if err != nil {
		return domain.Memory{}, err
	}
	input.Scope = normalizeMemoryScope(input.Scope)
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		return domain.Memory{}, fmt.Errorf("%w: memory content is required", domain.ErrInvalidInput)
	}
	if input.Kind == "" {
		input.Kind = domain.MemoryKindFact
	}
	if !validMemoryKind(input.Kind) {
		return domain.Memory{}, fmt.Errorf("%w: unknown memory kind %q", domain.ErrInvalidInput, input.Kind)
	}
	if input.Confidence <= 0 || input.Confidence > 1 {
		input.Confidence = 1
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	input.Metadata["source"] = "operator"
	memory, err := repository.Remember(ctx, input)
	if err != nil {
		return domain.Memory{}, err
	}
	s.logger.Info("memory written by operator", "memory_id", memory.ID, "scope", memory.Scope, "kind", memory.Kind)
	return memory, nil
}

// ForgetMemory 是軟性遺忘：資料保留稽核資訊，但不再被召回。
func (s *Service) ForgetMemory(ctx context.Context, scope, memoryID, reason string) (domain.Memory, error) {
	repository, err := s.memoryRepository()
	if err != nil {
		return domain.Memory{}, err
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return domain.Memory{}, fmt.Errorf("%w: memory id is required", domain.ErrInvalidInput)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "forgotten by operator"
	}
	memory, err := repository.Forget(ctx, normalizeMemoryScope(scope), memoryID, reason)
	if err != nil {
		return domain.Memory{}, err
	}
	s.logger.Info("memory forgotten by operator", "memory_id", memory.ID, "scope", memory.Scope)
	return memory, nil
}

func normalizeMemoryScope(scope string) string {
	if scope = strings.TrimSpace(scope); scope != "" {
		return scope
	}
	return DefaultMemoryScope
}

func validMemoryKind(kind domain.MemoryKind) bool {
	switch kind {
	case domain.MemoryKindFact, domain.MemoryKindPreference, domain.MemoryKindDecision,
		domain.MemoryKindProcedure, domain.MemoryKindConstraint:
		return true
	default:
		return false
	}
}
