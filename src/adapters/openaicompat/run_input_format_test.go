package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

// 執行中插入的使用者訊息會緊接在工具結果之後。以下測試固定兩種協定對這個
// 排列的編碼方式，避免日後調整轉接層時產生上游拒收的請求。

type runInputClaudeMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

func runInputClaudeMessages(t *testing.T, request domain.ModelRequest) []runInputClaudeMessage {
	t.Helper()
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := m.claudeRequest(request, "synthetic", false)
	if err != nil {
		t.Fatalf("claudeRequest: %v", err)
	}
	var payload struct {
		Messages []runInputClaudeMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return payload.Messages
}

func runInputClaudeShape(messages []runInputClaudeMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		types := make([]string, 0, len(message.Content))
		for _, block := range message.Content {
			blockType, _ := block["type"].(string)
			types = append(types, blockType)
		}
		parts = append(parts, message.Role+"["+strings.Join(types, " ")+"]")
	}
	return strings.Join(parts, " ")
}

// Claude 要求 tool_use 的下一則 user 訊息以 tool_result 開頭，且角色必須交替：
// 插入的文字要併入同一則 user 訊息，並排在所有 tool_result 之後。
func TestClaudeRunInputAfterToolResults(t *testing.T) {
	calls := []domain.ToolCall{
		{ID: "call_a", Name: "lookup", Arguments: map[string]any{}},
		{ID: "call_b", Name: "lookup", Arguments: map[string]any{}},
	}
	messages := runInputClaudeMessages(t, domain.ModelRequest{SystemPrompt: "系統", History: []domain.Message{
		{Role: "user", Content: "問題"},
		{Role: "assistant", ToolCalls: calls},
		{Role: "tool", ToolCallID: "call_a", Content: "結果 A"},
		{Role: "tool", ToolCallID: "call_b", Content: "結果 B"},
		{Role: "user", Content: "插入：改用表格"},
		{Role: "user", Content: "插入：再加總計"},
	}})
	got := runInputClaudeShape(messages)
	want := "user[text] assistant[tool_use tool_use] user[tool_result tool_result text text]"
	if got != want {
		t.Fatalf("shape = %s, want %s", got, want)
	}
	last := messages[len(messages)-1].Content
	if last[0]["tool_use_id"] != "call_a" || last[1]["tool_use_id"] != "call_b" {
		t.Fatalf("tool_result order = %v, %v", last[0]["tool_use_id"], last[1]["tool_use_id"])
	}
	if last[2]["text"] != "插入：改用表格" || last[3]["text"] != "插入：再加總計" {
		t.Fatalf("inserted text order = %v, %v", last[2]["text"], last[3]["text"])
	}
}

// 模型已經回答後才插入：對話以使用者訊息結尾，不需要也不應補系統續接訊息。
func TestClaudeRunInputAfterAnswer(t *testing.T) {
	messages := runInputClaudeMessages(t, domain.ModelRequest{History: []domain.Message{
		{Role: "user", Content: "問題"},
		{Role: "assistant", Content: "第一版回答"},
		{Role: "user", Content: "插入：改用表格"},
	}})
	if got, want := runInputClaudeShape(messages), "user[text] assistant[text] user[text]"; got != want {
		t.Fatalf("shape = %s, want %s", got, want)
	}
	if text := messages[2].Content[0]["text"]; text != "插入：改用表格" {
		t.Fatalf("last user text = %v", text)
	}
}

// 插入之後又跑了工具回合：後續歷史仍須維持角色交替與 tool_use／tool_result 配對。
func TestClaudeRunInputFollowedByToolRound(t *testing.T) {
	first := []domain.ToolCall{{ID: "call_1", Name: "lookup", Arguments: map[string]any{}}}
	second := []domain.ToolCall{{ID: "call_2", Name: "lookup", Arguments: map[string]any{}}}
	messages := runInputClaudeMessages(t, domain.ModelRequest{History: []domain.Message{
		{Role: "user", Content: "問題"},
		{Role: "assistant", ToolCalls: first},
		{Role: "tool", ToolCallID: "call_1", Content: "結果 1"},
		{Role: "user", Content: "插入：順便查 B"},
		{Role: "assistant", Content: "好的", ToolCalls: second},
		{Role: "tool", ToolCallID: "call_2", Content: "結果 2"},
	}})
	got := runInputClaudeShape(messages)
	want := "user[text] assistant[tool_use] user[tool_result text] assistant[text tool_use] user[tool_result]"
	if got != want {
		t.Fatalf("shape = %s, want %s", got, want)
	}
}

// Codex Responses：function_call_output 緊跟對應的 function_call，插入的訊息是
// 之後獨立的 user message。
func TestCodexRunInputAfterToolResults(t *testing.T) {
	items := codexInput(domain.ModelRequest{History: []domain.Message{
		{Role: "user", Content: "問題"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{
			{ID: "call_a", Name: "lookup", Arguments: map[string]any{}},
			{ID: "call_b", Name: "lookup", Arguments: map[string]any{}},
		}},
		{Role: "tool", ToolCallID: "call_a", Content: "結果 A"},
		{Role: "tool", ToolCallID: "call_b", Content: "結果 B"},
		{Role: "user", Content: "插入：改用表格"},
	}})
	shape := make([]string, 0, len(items))
	for _, item := range items {
		label := item.Type
		if item.Type == "message" {
			label += ":" + item.Role
		}
		shape = append(shape, label)
	}
	got := strings.Join(shape, " ")
	want := "message:user function_call function_call function_call_output function_call_output message:user"
	if got != want {
		t.Fatalf("shape = %s, want %s", got, want)
	}
	if items[3].CallID != "call_a" || items[4].CallID != "call_b" {
		t.Fatalf("output call ids = %s, %s", items[3].CallID, items[4].CallID)
	}
	last := items[len(items)-1]
	if len(last.Content) != 1 || last.Content[0].Type != "input_text" || last.Content[0].Text != "插入：改用表格" {
		t.Fatalf("inserted item = %#v", last)
	}
	if _, err := json.Marshal(items); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}
