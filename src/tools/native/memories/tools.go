package memories

import (
	"AgenticService/src/domain"
	"AgenticService/src/memory"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type SearchTool struct {
	Repository ports.MemoryRepository
}

type RememberTool struct {
	Repository ports.MemoryRepository
}

type ForgetTool struct {
	Repository ports.MemoryRepository
}

func NewSearchTool(repository ports.MemoryRepository) *SearchTool {
	return &SearchTool{Repository: repository}
}

func NewRememberTool(repository ports.MemoryRepository) *RememberTool {
	return &RememberTool{Repository: repository}
}

func NewForgetTool(repository ports.MemoryRepository) *ForgetTool {
	return &ForgetTool{Repository: repository}
}

func (t *SearchTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "memory_search",
		Label:        "查詢長期記憶",
		Version:      "1.0.0",
		Category:     "memory",
		Description:  "查詢目前記憶作用域內的長期記憶。適合確認偏好、決策、事實、程序與限制；召回內容仍應視需要驗證。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"recall", "scope-isolation", "lexical-retrieval"},
		ReadOnly:     true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "要查詢的語意或關鍵字；省略時列出最近的有效記憶"},
				"kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": memoryKinds()}, "description": "可選的記憶種類過濾"},
				"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "至少符合其中一個標籤"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 8},
			},
		},
	}
}

func (t *SearchTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "memory repository is unavailable"), nil
	}
	kinds, err := parseKinds(toolutil.StringSlice(invocation.Call.Arguments, "kinds"))
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	values, err := t.Repository.Search(ctx, domain.MemoryQuery{
		Scope: memory.ScopeForSession(invocation.Session),
		Text:  toolutil.String(invocation.Call.Arguments, "query"),
		Kinds: kinds,
		Tags:  toolutil.StringSlice(invocation.Call.Arguments, "tags"),
		Limit: toolutil.Int(invocation.Call.Arguments, "limit", 8, 1, 50),
	})
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	return jsonExecution(invocation.Call, map[string]any{"count": len(values), "memories": values})
}

func (t *RememberTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "memory_remember",
		Label:        "寫入長期記憶",
		Version:      "1.0.0",
		Category:     "memory",
		Description:  "保存跨 session 仍有價值的事實、偏好、決策、程序或限制。不要保存暫時資訊、憑證、金鑰、密碼或其他敏感資料。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"remember", "deduplicate", "supersede", "scope-isolation"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content":    map[string]any{"type": "string", "description": "單一、完整且可獨立理解的長期記憶內容"},
				"kind":       map[string]any{"type": "string", "enum": memoryKinds()},
				"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": 0.8},
				"supersedes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "被此記憶取代的舊記憶 ID"},
			},
			"required": []string{"content", "kind"},
		},
	}
}

func (t *RememberTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "memory repository is unavailable"), nil
	}
	content := toolutil.String(invocation.Call.Arguments, "content")
	if content == "" {
		return failure(invocation.Call, "content is required"), nil
	}
	kinds, err := parseKinds([]string{toolutil.String(invocation.Call.Arguments, "kind")})
	if err != nil || len(kinds) != 1 {
		if err == nil {
			err = fmt.Errorf("kind is required")
		}
		return failure(invocation.Call, err.Error()), nil
	}
	value, err := t.Repository.Remember(ctx, domain.RememberMemoryInput{
		Scope:           memory.ScopeForSession(invocation.Session),
		Kind:            kinds[0],
		Content:         content,
		Tags:            toolutil.StringSlice(invocation.Call.Arguments, "tags"),
		Confidence:      toolutil.Float(invocation.Call.Arguments, "confidence", 0.8, 0, 1),
		SourceSessionID: invocation.Session.ID,
		Supersedes:      toolutil.StringSlice(invocation.Call.Arguments, "supersedes"),
		Metadata:        map[string]any{"source": "agent_tool"},
	})
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	return jsonExecution(invocation.Call, map[string]any{"memory": value})
}

func (t *ForgetTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         "memory_forget",
		Label:        "遺忘長期記憶",
		Version:      "1.0.0",
		Category:     "memory",
		Description:  "將指定記憶標記為已遺忘，保留稽核紀錄但不再召回。只有使用者明確要求遺忘時才能呼叫。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"soft-forget", "audit-trail", "scope-isolation"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "description": "要遺忘的記憶 ID"},
				"reason": map[string]any{"type": "string", "description": "使用者要求遺忘的理由或原始語意"},
			},
			"required": []string{"id", "reason"},
		},
	}
}

func (t *ForgetTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "memory repository is unavailable"), nil
	}
	id := toolutil.String(invocation.Call.Arguments, "id")
	reason := toolutil.String(invocation.Call.Arguments, "reason")
	if id == "" || reason == "" {
		return failure(invocation.Call, "id and reason are required"), nil
	}
	value, err := t.Repository.Forget(ctx, memory.ScopeForSession(invocation.Session), id, reason)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	return jsonExecution(invocation.Call, map[string]any{"memory": value})
}

func parseKinds(values []string) ([]domain.MemoryKind, error) {
	result := make([]domain.MemoryKind, 0, len(values))
	for _, value := range values {
		kind := domain.MemoryKind(strings.ToLower(strings.TrimSpace(value)))
		switch kind {
		case domain.MemoryKindFact, domain.MemoryKindPreference, domain.MemoryKindDecision, domain.MemoryKindProcedure, domain.MemoryKindConstraint:
			result = append(result, kind)
		case "":
			continue
		default:
			return nil, fmt.Errorf("unsupported memory kind %q", value)
		}
	}
	return result, nil
}

func memoryKinds() []string {
	return []string{"fact", "preference", "decision", "procedure", "constraint"}
}

func jsonExecution(call domain.ToolCall, value any) (domain.ToolExecution, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return domain.ToolExecution{}, err
	}
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: string(data)}, nil
}

func failure(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), IsError: true}
}
