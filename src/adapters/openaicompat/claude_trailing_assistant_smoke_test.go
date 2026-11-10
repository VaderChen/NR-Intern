package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"testing"
)

// Harness 的完成度追問、計畫追問、工具協定修復與截斷重試，都會把上一輪的
// 內部 assistant 訊息留在歷史尾端，再只靠 system／context 指示重跑一輪。
// Claude 要求對話以 user 訊息結尾，否則回 400（不支援 assistant prefill）。
func TestClaudeTrailingAssistantSmoke(t *testing.T) {
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	toolCall := []domain.ToolCall{{ID: "call", Name: "lookup", Arguments: map[string]any{}}}
	cases := []struct {
		name        string
		request     domain.ModelRequest
		wantRoles   []string
		wantSynth   bool
		wantLastTxt string
	}{
		{
			name: "completion_check_retry",
			request: domain.ModelRequest{SystemPrompt: "系統", ContextPrompt: "追問指示", History: []domain.Message{
				{Role: "user", Content: "問題"},
				{Role: "assistant", Content: "過早的回答"},
			}},
			wantRoles: []string{"user", "assistant", "user"},
			wantSynth: true,
		},
		{
			name: "after_tool_round",
			request: domain.ModelRequest{History: []domain.Message{
				{Role: "user", Content: "問題"},
				{Role: "assistant", ToolCalls: toolCall},
				{Role: "tool", ToolCallID: "call", Content: "結果"},
				{Role: "assistant", Content: "過早的回答"},
			}},
			wantRoles: []string{"user", "assistant", "user", "assistant", "user"},
			wantSynth: true,
		},
		{
			name: "ends_with_tool_result",
			request: domain.ModelRequest{History: []domain.Message{
				{Role: "user", Content: "問題"},
				{Role: "assistant", ToolCalls: toolCall},
				{Role: "tool", ToolCallID: "call", Content: "結果"},
			}},
			wantRoles:   []string{"user", "assistant", "user"},
			wantLastTxt: "",
		},
		{
			name: "ends_with_user_prompt",
			request: domain.ModelRequest{UserPrompt: "新問題", History: []domain.Message{
				{Role: "user", Content: "問題"},
				{Role: "assistant", Content: "回答"},
			}},
			wantRoles:   []string{"user", "assistant", "user"},
			wantLastTxt: "新問題",
		},
		{
			// 未配對的 tool_use 是另一種協定錯誤，不以續接訊息掩蓋。
			name: "unanswered_tool_use_untouched",
			request: domain.ModelRequest{History: []domain.Message{
				{Role: "user", Content: "問題"},
				{Role: "assistant", ToolCalls: toolCall},
			}},
			wantRoles: []string{"user", "assistant"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := m.claudeRequest(tc.request, "synthetic", true)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Messages []claudeMessage `json:"messages"`
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			roles := make([]string, 0, len(body.Messages))
			for _, message := range body.Messages {
				roles = append(roles, message.Role)
			}
			if len(roles) != len(tc.wantRoles) {
				t.Fatalf("訊息角色序列為 %v，預期 %v", roles, tc.wantRoles)
			}
			for index := range roles {
				if roles[index] != tc.wantRoles[index] {
					t.Fatalf("訊息角色序列為 %v，預期 %v", roles, tc.wantRoles)
				}
			}
			last := body.Messages[len(body.Messages)-1]
			synthetic := len(last.Content) == 1 && last.Content[0].Type == "text" && last.Content[0].Text == claudeContinuationPrompt
			if synthetic != tc.wantSynth {
				t.Fatalf("續接訊息出現與否不符：got %v want %v", synthetic, tc.wantSynth)
			}
			if tc.wantLastTxt != "" && (len(last.Content) != 1 || last.Content[0].Text != tc.wantLastTxt) {
				t.Fatalf("尾端 user 訊息被改動：%+v", last.Content)
			}
			if tc.wantSynth {
				previous := body.Messages[len(body.Messages)-2]
				if previous.Role != "assistant" || len(previous.Content) != 1 || previous.Content[0].Text != "過早的回答" {
					t.Fatalf("內部 assistant 訊息未原樣保留：%+v", previous)
				}
			}
		})
	}
}

// 追問後的下一輪若呼叫工具，再下一次請求必須在相同位置重現續接訊息，
// 否則前綴摘要改變，該輪的思考簽章會被當成失效而丟棄。
func TestClaudeContinuationKeepsPrefixBindingSmoke(t *testing.T) {
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	history := []domain.Message{{Role: "user", Content: "問題"}, {Role: "assistant", Content: "過早的回答"}}
	retry, err := m.claudeRequest(domain.ModelRequest{SystemPrompt: "系統", History: history}, "synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := claudeRequestPrefixHash(retry)
	if err != nil {
		t.Fatal(err)
	}
	state := &domain.ModelContinuation{Protocol: "claude-messages", Scope: m.continuationScope, Model: "synthetic", PrefixHash: hash, Items: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"摘要","signature":"synthetic-signature"}`)}}
	next := append(append([]domain.Message{}, history...),
		domain.Message{Role: "assistant", Continuation: state, ToolCalls: []domain.ToolCall{{ID: "call", Name: "lookup", Arguments: map[string]any{}}}},
		domain.Message{Role: "tool", ToolCallID: "call", Content: "結果"},
	)
	data, err := m.claudeRequest(domain.ModelRequest{SystemPrompt: "系統", History: next}, "synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []claudeMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 5 {
		t.Fatalf("預期 user／assistant／續接／assistant／tool_result 共 5 則，實際 %d 則", len(body.Messages))
	}
	if body.Messages[2].Role != "user" || body.Messages[2].Content[0].Text != claudeContinuationPrompt {
		t.Fatalf("續接訊息未在原位置重現：%+v", body.Messages[2])
	}
	blocks := body.Messages[3].Content
	if body.Messages[3].Role != "assistant" || len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[0].Signature != "synthetic-signature" || blocks[1].Type != "tool_use" {
		t.Fatalf("思考簽章未隨前綴保留：%+v", blocks)
	}
	if last := body.Messages[4]; last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "tool_result" {
		t.Fatalf("工具結果後不應再補續接訊息：%+v", last)
	}
}
