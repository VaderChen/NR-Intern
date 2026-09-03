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
	Memory *memory.Manager
}

type RememberTool struct {
	Memory *memory.Manager
}

type ForgetTool struct {
	Memory *memory.Manager
}

// 工具一律經過 Manager：准入、去重、淘汰與 scope 解析都集中在那裡，
// 直接打 Repository 會繞過全部策略。
func NewSearchTool(manager *memory.Manager) *SearchTool {
	return &SearchTool{Memory: manager}
}

func NewRememberTool(manager *memory.Manager) *RememberTool {
	return &RememberTool{Memory: manager}
}

func NewForgetTool(manager *memory.Manager) *ForgetTool {
	return &ForgetTool{Memory: manager}
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
	if t == nil || t.Memory == nil {
		return failure(invocation.Call, "memory repository is unavailable"), nil
	}
	kinds, err := parseKinds(toolutil.StringSlice(invocation.Call.Arguments, "kinds"))
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	// scope 交給 Manager 解析：回憶空間開啟時寫入會收斂到專案，這裡若自己帶
	// 舊的 Agent scope，模型就找不到自己剛寫進去的記憶。
	values, err := t.Memory.Search(ctx, invocation.Session, domain.MemoryQuery{
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
		Description:  rememberDescription(t != nil && t.Memory.SpaceEnabled()),
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

// rememberDescription 在回憶空間開啟時先講清楚准入條件。
//
// 不講的話模型只會寫進來、被擋下、再換句話說寫一次——三次工具呼叫換不到任何東西。
func rememberDescription(spaceEnabled bool) string {
	if !spaceEnabled {
		return "保存跨 session 仍有價值的事實、偏好、決策、程序或限制。不要保存暫時資訊、憑證、金鑰、密碼或其他敏感資料。"
	}
	return "保存跨對話仍有價值的偏好、決策、限制或作法。回憶空間已開啟，只收 preference、decision、constraint、procedure 四類；" +
		"fact 會被拒絕——會過期，需要時重新查詢即可。內容必須換一個對話、換一天仍然成立；" +
		"只對這次任務成立的路徑、錯誤訊息與暫時狀態，以及倉庫裡查得到的程式結構或 git 記錄都不要寫。" +
		"憑證、金鑰、密碼一律擋下。改變既有決策時用 supersedes 指定被取代的記憶 ID，不要只是再寫一則。"
}

func (t *RememberTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Memory == nil {
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
	value, err := t.Memory.Remember(ctx, invocation.Session, domain.RememberMemoryInput{
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
	if t == nil || t.Memory == nil {
		return failure(invocation.Call, "memory repository is unavailable"), nil
	}
	id := toolutil.String(invocation.Call.Arguments, "id")
	reason := toolutil.String(invocation.Call.Arguments, "reason")
	if id == "" || reason == "" {
		return failure(invocation.Call, "id and reason are required"), nil
	}
	value, err := t.Memory.Forget(ctx, invocation.Session, id, reason)
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
