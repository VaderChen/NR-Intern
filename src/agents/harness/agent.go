// Package harnessagent 把具體 Harness Runner 包裝成 application 可註冊的 AgentEngine。
// 它位於 use-case 層之外，避免 application 反向依賴具體推理引擎。
package harnessagent

import (
	"AgenticService/src/domain"
	harnesscore "AgenticService/src/harness"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

var _ ports.AgentEngine = (*Agent)(nil)

type Agent struct {
	descriptorMu sync.RWMutex
	descriptor   domain.AgentDescriptor
	sessions     ports.SessionRepository
	runner       *harnesscore.Runner
}

func New(descriptor domain.AgentDescriptor, sessions ports.SessionRepository, runner *harnesscore.Runner) (*Agent, error) {
	if strings.TrimSpace(descriptor.ID) == "" || sessions == nil || runner == nil {
		return nil, fmt.Errorf("%w: descriptor, session repository and runner are required", domain.ErrInvalidInput)
	}
	return &Agent{descriptor: descriptor, sessions: sessions, runner: runner}, nil
}

func (a *Agent) Descriptor() domain.AgentDescriptor {
	a.descriptorMu.RLock()
	defer a.descriptorMu.RUnlock()
	return a.descriptor
}

func (a *Agent) SetName(name string) {
	a.descriptorMu.Lock()
	a.descriptor.Name = strings.TrimSpace(name)
	a.descriptorMu.Unlock()
}

// SetRunBudget 讓管理設定只影響之後開始的工作；Runner 會在每次 Run 啟動時取得快照。
func (a *Agent) SetRunBudget(budget domain.RunBudget) {
	if a == nil || a.runner == nil {
		return
	}
	a.runner.SetBudget(budget)
}

// SetToolCallMode 供管理介面即時切換工具呼叫協定。
func (a *Agent) SetToolCallMode(mode harnesscore.ToolCallMode) {
	if a == nil || a.runner == nil {
		return
	}
	a.runner.SetToolCallMode(mode)
}

// SetToolRetrieval 供管理介面即時切換 MCP 工具目錄的檢索過濾。
func (a *Agent) SetToolRetrieval(enabled bool) {
	if a == nil || a.runner == nil {
		return
	}
	a.runner.SetToolRetrieval(enabled)
}

func (a *Agent) CreateSession(ctx context.Context, input domain.CreateSessionInput) (domain.Session, error) {
	thinkingMode, err := domain.NormalizeThinkingMode(input.ThinkingMode)
	if err != nil {
		return domain.Session{}, err
	}
	input.ThinkingMode = thinkingMode
	return a.sessions.Create(ctx, a.descriptor.ID, input)
}

func (a *Agent) ListSessions(ctx context.Context) ([]domain.Session, error) {
	return a.sessions.List(ctx, a.descriptor.ID)
}

func (a *Agent) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	session, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if session.AgentID != a.descriptor.ID {
		return domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
	}
	return session, nil
}

func (a *Agent) DeleteSession(ctx context.Context, sessionID string) error {
	if _, err := a.GetSession(ctx, sessionID); err != nil {
		return err
	}
	return a.sessions.Delete(ctx, sessionID)
}

func (a *Agent) UpdateSession(ctx context.Context, sessionID string, input domain.UpdateSessionInput) (domain.Session, error) {
	if input.Title == nil && input.WorkspaceID == nil && input.ProjectID == nil && input.ProviderID == nil && input.Model == nil && input.ThinkingMode == nil && input.LockPlans == nil && input.PermissionProfile == nil && input.MemoryScope == nil && input.Pinned == nil && input.Position == nil {
		return domain.Session{}, fmt.Errorf("%w: at least one session field is required", domain.ErrInvalidInput)
	}
	session, err := a.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if input.Title != nil {
		session.Title = strings.TrimSpace(*input.Title)
		if session.Title == "" {
			return domain.Session{}, fmt.Errorf("%w: title cannot be empty", domain.ErrInvalidInput)
		}
	}
	if input.ProjectID != nil {
		session.ProjectID = strings.TrimSpace(*input.ProjectID)
	}
	if input.WorkspaceID != nil {
		session.WorkspaceID = strings.TrimSpace(*input.WorkspaceID)
	}
	if input.ProviderID != nil {
		session.ProviderID = strings.TrimSpace(*input.ProviderID)
	}
	if input.Model != nil {
		session.Model = strings.TrimSpace(*input.Model)
	}
	if input.ThinkingMode != nil {
		thinkingMode, normalizeErr := domain.NormalizeThinkingMode(*input.ThinkingMode)
		if normalizeErr != nil {
			return domain.Session{}, normalizeErr
		}
		session.ThinkingMode = thinkingMode
	}
	if input.LockPlans != nil {
		session.LockPlans = *input.LockPlans
	}
	if input.PermissionProfile != nil {
		session.PermissionProfile = strings.ToLower(strings.TrimSpace(*input.PermissionProfile))
		if session.PermissionProfile == "" {
			session.PermissionProfile = domain.DefaultPermissionProfile
		}
	}
	if input.MemoryScope != nil {
		if session.Metadata == nil {
			session.Metadata = map[string]any{}
		}
		if scope := strings.TrimSpace(*input.MemoryScope); scope != "" {
			session.Metadata["memory_scope"] = scope
		} else {
			delete(session.Metadata, "memory_scope")
		}
	}
	if input.Pinned != nil {
		session.Pinned = *input.Pinned
		if session.Pinned {
			if session.PinnedAt == nil {
				now := time.Now().UTC()
				session.PinnedAt = &now
			}
		} else {
			session.PinnedAt = nil
		}
	}
	if input.Position != nil {
		if *input.Position < 0 {
			return domain.Session{}, fmt.Errorf("%w: session position cannot be negative", domain.ErrInvalidInput)
		}
		session.Position = *input.Position
	}
	if err := a.sessions.Update(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return a.GetSession(ctx, sessionID)
}

// SetPermanentToolApproval 是 Application 審核流程專用入口，不暴露在一般
// Session PATCH，避免 HTTP Client 未經一次人工核准就自行關閉後續審核。
func (a *Agent) SetPermanentToolApproval(ctx context.Context, sessionID string, enabled bool) (domain.Session, error) {
	session, err := a.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	session.PermanentToolApproval = enabled
	if err := a.sessions.Update(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return a.GetSession(ctx, sessionID)
}

func (a *Agent) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	if _, err := a.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	return a.sessions.ListMessages(ctx, sessionID)
}

// RetractMessages 把 messageID 起的訊息移出對話，並回傳剩下的訊息。
//
// 只允許最後一則使用者訊息：重問中途的問題會讓後面所有回答失去依據，那不是
// 「重來一次」而是改寫歷史。transcript 不會被刪除，只是追加一筆撤回記錄。
func (a *Agent) RetractMessages(ctx context.Context, sessionID, messageID string) ([]domain.Message, error) {
	if _, err := a.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, fmt.Errorf("%w: message id is required", domain.ErrInvalidInput)
	}
	messages, err := a.sessions.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	lastUserIndex := -1
	target := -1
	for index, message := range messages {
		if strings.EqualFold(message.Role, "user") {
			lastUserIndex = index
		}
		if message.ID == messageID {
			target = index
		}
	}
	if target < 0 {
		return nil, fmt.Errorf("%w: message %q is not part of this session", domain.ErrNotFound, messageID)
	}
	if target != lastUserIndex {
		return nil, fmt.Errorf("%w: only the last user message can be retracted", domain.ErrInvalidInput)
	}
	if _, err := a.sessions.AppendEntry(ctx, sessionID, domain.SessionEntry{
		SessionID: sessionID,
		Type:      domain.SessionEntryMessagesRetracted,
		Data:      map[string]any{"from_message_id": messageID, "reason": "reask"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return a.sessions.ListMessages(ctx, sessionID)
}

func (a *Agent) ListEntries(ctx context.Context, sessionID string) ([]domain.SessionEntry, error) {
	if _, err := a.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	return a.sessions.ListEntries(ctx, sessionID)
}

func (a *Agent) Run(ctx context.Context, input domain.RunInput, emit ports.AgentEventSink) (domain.RunResult, error) {
	session, err := a.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.RunResult{}, err
	}
	if providerID := strings.TrimSpace(input.ProviderID); providerID != "" {
		session.ProviderID = providerID
	}
	if model := strings.TrimSpace(input.Model); model != "" {
		session.Model = model
	}
	if sandboxRoots := stringSliceFromMap(input.Metadata, "sandbox_roots"); len(sandboxRoots) > 0 {
		if session.Metadata == nil {
			session.Metadata = map[string]any{}
		} else {
			cloned := make(map[string]any, len(session.Metadata)+1)
			for key, value := range session.Metadata {
				cloned[key] = value
			}
			session.Metadata = cloned
		}
		session.Metadata["sandbox_roots"] = sandboxRoots
	}
	thinkingMode := strings.TrimSpace(input.ThinkingMode)
	if thinkingMode == "" {
		if value, exists := input.Metadata["thinking_mode"]; exists {
			legacy, ok := value.(string)
			if !ok && value != nil {
				return domain.RunResult{}, fmt.Errorf("%w: thinking_mode must be a string", domain.ErrInvalidInput)
			}
			thinkingMode = legacy
		} else {
			thinkingMode = session.ThinkingMode
		}
	}
	thinkingMode, err = domain.NormalizeThinkingMode(thinkingMode)
	if err != nil {
		return domain.RunResult{}, err
	}
	return a.runner.Run(ctx, harnesscore.Input{
		RunID:        input.RunID,
		Session:      session,
		UserInput:    input.UserInput,
		ProviderID:   input.ProviderID,
		Model:        input.Model,
		ThinkingMode: thinkingMode,
		Metadata:     input.Metadata,
	}, harnesscore.EventSink(emit))
}

func stringSliceFromMap(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	result := []string{}
	switch items := values[key].(type) {
	case []string:
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	case []any:
		for _, value := range items {
			if item, ok := value.(string); ok {
				if item = strings.TrimSpace(item); item != "" {
					result = append(result, item)
				}
			}
		}
	}
	return result
}
