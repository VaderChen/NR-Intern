package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// memorySessions 是 ports.SessionRepository 的最小記憶體實作。
// AppendEntry 刻意複製 filestore 的行為：ctx 已取消時拒絕寫入，
// 否則測試無法證明工具結果的落盤路徑真的不受取消影響。
type memorySessions struct {
	mu       sync.Mutex
	sequence int64
	session  domain.Session
	entries  []domain.SessionEntry
}

func newMemorySessions(session domain.Session) *memorySessions {
	return &memorySessions{session: session}
}

func (r *memorySessions) Create(context.Context, string, domain.CreateSessionInput) (domain.Session, error) {
	return r.session, nil
}

func (r *memorySessions) List(context.Context, string) ([]domain.Session, error) {
	return []domain.Session{r.session}, nil
}

func (r *memorySessions) Get(_ context.Context, sessionID string) (domain.Session, error) {
	if sessionID != r.session.ID {
		return domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
	}
	return r.session, nil
}

func (r *memorySessions) Update(_ context.Context, session domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session = session
	return nil
}

func (r *memorySessions) Delete(context.Context, string) error { return nil }

func (r *memorySessions) AppendEntry(ctx context.Context, sessionID string, entry domain.SessionEntry) (domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionEntry{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	entry.Sequence = r.sequence
	entry.SessionID = sessionID
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	r.entries = append(r.entries, entry)
	return entry, nil
}

func (r *memorySessions) ListEntries(context.Context, string) ([]domain.SessionEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.SessionEntry(nil), r.entries...), nil
}

func (r *memorySessions) ListEntriesAfter(ctx context.Context, sessionID string, afterSequence int64) ([]domain.SessionEntry, error) {
	entries, err := r.ListEntries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := []domain.SessionEntry{}
	for _, entry := range entries {
		if entry.Sequence > afterSequence {
			result = append(result, entry)
		}
	}
	return result, nil
}

func (r *memorySessions) LatestEntryOfType(ctx context.Context, sessionID, entryType string) (domain.SessionEntry, error) {
	entries, err := r.ListEntries(ctx, sessionID)
	if err != nil {
		return domain.SessionEntry{}, err
	}
	found := false
	var latest domain.SessionEntry
	for _, entry := range entries {
		if entry.Type == entryType && (!found || entry.Sequence >= latest.Sequence) {
			latest = entry
			found = true
		}
	}
	if !found {
		return domain.SessionEntry{}, fmt.Errorf("%w: %s entry", domain.ErrNotFound, entryType)
	}
	return latest, nil
}

func (r *memorySessions) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	entries, err := r.ListEntries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := []domain.Message{}
	for _, entry := range entries {
		if entry.Type == domain.SessionEntryMessage && entry.Message != nil {
			messages = append(messages, *entry.Message)
		}
	}
	return messages, nil
}

func (r *memorySessions) entryTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	types := make([]string, 0, len(r.entries))
	for _, entry := range r.entries {
		types = append(types, entry.Type)
	}
	return types
}

// scriptedModel 依序回傳預先安排的回應，讓 harness 測試不需要真的 Provider。
type scriptedModel struct {
	mu        sync.Mutex
	responses []domain.ModelResponse
	errors    []error
	requests  []domain.ModelRequest
	index     int
	onStream  func(turn int)
}

func (m *scriptedModel) Stream(_ context.Context, request domain.ModelRequest, _ ports.ModelEventSink) (domain.ModelResponse, error) {
	m.mu.Lock()
	turn := m.index
	m.index++
	m.requests = append(m.requests, request)
	m.mu.Unlock()
	if m.onStream != nil {
		m.onStream(turn)
	}
	if turn < len(m.errors) && m.errors[turn] != nil {
		return domain.ModelResponse{}, m.errors[turn]
	}
	if len(m.responses) == 0 {
		return domain.ModelResponse{}, fmt.Errorf("scripted model has no responses")
	}
	if turn >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}
	return m.responses[turn], nil
}

func (m *scriptedModel) lastRequest() domain.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return domain.ModelRequest{}
	}
	return m.requests[len(m.requests)-1]
}

type fakeTools struct {
	definitions []domain.ToolDefinition
	execute     func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error)
}

func (t *fakeTools) Definitions(context.Context, domain.Session) ([]domain.ToolDefinition, error) {
	return t.definitions, nil
}

func (t *fakeTools) Execute(ctx context.Context, session domain.Session, call domain.ToolCall, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t.execute == nil {
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
	}
	return t.execute(ctx, session, call)
}

func testSession() domain.Session {
	return domain.Session{
		ID:                "session_test",
		AgentID:           "agent_test",
		PermissionProfile: domain.DefaultPermissionProfile,
	}
}

func assistantWithCalls(id string, calls ...domain.ToolCall) domain.Message {
	return domain.Message{ID: id, SessionID: "session_test", Role: "assistant", ToolCalls: calls}
}

func toolResult(id, callID, content string) domain.Message {
	return domain.Message{ID: id, SessionID: "session_test", Role: "tool", ToolCallID: callID, Content: content}
}

func sequenced(messages ...domain.Message) []sequencedMessage {
	result := make([]sequencedMessage, len(messages))
	for index, message := range messages {
		result[index] = sequencedMessage{Sequence: int64(index + 1), Message: message}
	}
	return result
}

func roleSummary(messages []sequencedMessage) string {
	parts := make([]string, 0, len(messages))
	for _, item := range messages {
		if strings.EqualFold(item.Message.Role, "tool") {
			parts = append(parts, "tool:"+item.Message.ToolCallID)
			continue
		}
		parts = append(parts, item.Message.Role)
	}
	return strings.Join(parts, ",")
}

// fakeCapabilities 讓測試決定當次使用的模型宣告了什麼限制。
type fakeCapabilities struct {
	capabilities domain.ModelCapabilities
	asked        []string
}

func (c *fakeCapabilities) Capabilities(providerID, model string) domain.ModelCapabilities {
	c.asked = append(c.asked, providerID+"/"+model)
	return c.capabilities
}

// fakeApprovals 讓測試控制哪些工具需要人工核准。
type fakeApprovals struct {
	required map[string]bool
	mu       sync.Mutex
	begun    []string
	decision domain.ToolApprovalDecision
	beginErr error
}

func (a *fakeApprovals) Required(toolName string) bool { return a.required[toolName] }

func (a *fakeApprovals) Begin(request domain.ToolApprovalRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.beginErr != nil {
		return a.beginErr
	}
	a.begun = append(a.begun, request.ToolCallID)
	return nil
}

func (a *fakeApprovals) Wait(context.Context, string) (domain.ToolApprovalDecision, error) {
	if a.decision.Decision == "" {
		return domain.ToolApprovalDecision{Decision: domain.ToolApprovalApprove}, nil
	}
	return a.decision, nil
}

func (a *fakeApprovals) Decide(string, domain.ToolApprovalDecisionInput) error { return nil }

func (a *fakeApprovals) Cancel(string) {}
