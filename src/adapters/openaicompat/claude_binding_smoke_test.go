package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudePrefixBindingSmoke(t *testing.T) {
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	tool := domain.ToolDefinition{Name: "lookup", Description: "查詢", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
	base := domain.ModelRequest{SystemPrompt: "固定系統", Tools: []domain.ToolDefinition{tool}, UserPrompt: "查詢"}
	body, err := m.claudeRequest(base, "synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := claudeRequestPrefixHash(body)
	if err != nil {
		t.Fatal(err)
	}
	state := &domain.ModelContinuation{Protocol: "claude-messages", Scope: m.continuationScope, Model: "synthetic", PrefixHash: hash, Items: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"摘要","signature":"synthetic-signature"}`)}}
	for _, scenario := range []string{"unchanged", "system", "phase", "context", "tools", "history", "compacted", "legacy", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			req := domain.ModelRequest{SystemPrompt: base.SystemPrompt, Tools: []domain.ToolDefinition{tool}, History: []domain.Message{{Role: "user", Content: "查詢"}, {Role: "assistant", Continuation: state.Clone(), ToolCalls: []domain.ToolCall{{ID: "call", Name: "lookup", Arguments: map[string]any{}}}}, {Role: "tool", ToolCallID: "call", Content: "結果"}}}
			switch scenario {
			case "system":
				req.SystemPrompt = "已改變"
			case "phase":
				req.PhasePrompt = "現在收尾"
			case "context":
				req.ContextPrompt = "新的記憶"
			case "tools":
				req.Tools[0].Description = "新工具定義"
			case "history":
				req.History[0].Content = "不同問題"
			case "compacted":
				req.History = req.History[1:]
			case "legacy":
				req.History[1].Continuation.PrefixHash = ""
			case "foreign":
				req.History[1].Continuation.Scope = "other"
			}
			data, err := m.claudeRequest(req, "synthetic", false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "synthetic-signature") != (scenario == "unchanged") {
				t.Fatal("思考狀態的前綴綁定不正確")
			}
			var payload struct {
				Messages []claudeMessage `json:"messages"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			calls, results := 0, 0
			for _, message := range payload.Messages {
				for _, block := range message.Content {
					if block.Type == "tool_use" {
						calls++
					}
					if block.Type == "tool_result" {
						results++
					}
				}
			}
			if calls != 1 || results != 1 {
				t.Fatal("移除失效思考時破壞工具結果配對")
			}
			if state.PrefixHash != hash {
				t.Fatal("污染原始續接狀態")
			}
		})
	}
}

func TestClaudeInterleavedThinkingNotReorderedSmoke(t *testing.T) {
	body := `{"type":"message","content":[{"type":"text","text":"先說明"},{"type":"thinking","thinking":"摘要","signature":"synthetic-signature"},{"type":"tool_use","id":"call","name":"lookup","input":{}}],"stop_reason":"tool_use"}`
	response, err := decodeClaude(strings.NewReader(body), false, "synthetic", "", nil)
	if err != nil || response.Continuation != nil || len(response.ToolCalls) != 1 || response.Content != "先說明" {
		t.Fatalf("交錯區塊不應重新排列為續接狀態：%v", err)
	}
}
