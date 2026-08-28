package harness

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

func instructionTestDefinitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{
		Name:        "directory_list",
		Description: "列出目錄",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}}
}

func TestToolInstructionPromptPublishesStrictDecisionProtocol(t *testing.T) {
	prompt := toolInstructionPrompt(instructionTestDefinitions())
	for _, expected := range []string{
		`{"type":"tool_use","tool":"工具名稱","input":{},"reason":"簡短理由"}`,
		`"name":"directory_list"`,
		`"path":{"type":"string"}`,
		"每一輪只能輸出一個嚴格 JSON object",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestParseInstructionToolCallSupportsStrictJSON(t *testing.T) {
	call, matched, err := parseInstructionToolCall(`{"type":"tool_use","tool":"directory_list","input":{"path":"."},"reason":"inspect"}`, instructionTestDefinitions())
	if err != nil {
		t.Fatalf("parseInstructionToolCall: %v", err)
	}
	if !matched || call.Name != "directory_list" || call.Arguments["path"] != "." || call.ID == "" {
		t.Fatalf("call = %+v, matched=%v", call, matched)
	}
}

func TestParseInstructionToolCallSupportsAgenticServiceTags(t *testing.T) {
	content := "[TOOL_CALL]\n" + `{"tool":"directory_list","input":{"path":"docs"}}` + "\n[/TOOL_CALL]"
	call, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err != nil || !matched {
		t.Fatalf("call=%+v matched=%v err=%v", call, matched, err)
	}
	if call.Name != "directory_list" || call.Arguments["path"] != "docs" {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseInstructionToolCallSupportsProviderToEnvelope(t *testing.T) {
	content := "to=directory_list code\n" + `{"path":".","max_depth":3}` + "\n後續文字不應成為工具參數"
	call, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err != nil || !matched {
		t.Fatalf("call=%+v matched=%v err=%v", call, matched, err)
	}
	if call.Name != "directory_list" || call.Arguments["path"] != "." {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseInstructionToolCallIgnoresPrematureTextAfterJSONDecision(t *testing.T) {
	content := `{"type":"tool_use","tool":"directory_list","input":{"path":"."}}` + "未經工具驗證的提前答案"
	call, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err != nil || !matched {
		t.Fatalf("call=%+v matched=%v err=%v", call, matched, err)
	}
	if call.Name != "directory_list" || call.Arguments["path"] != "." {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseInstructionToolCallSupportsPrefaceBeforeJSONDecision(t *testing.T) {
	content := `我已取得部分資料，接著補查輸出目錄。{"type":"tool_use","tool":"directory_list","input":{"path":"output","max_depth":3},"reason":"確認報告類型"}`
	call, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err != nil || !matched {
		t.Fatalf("call=%+v matched=%v err=%v", call, matched, err)
	}
	if call.Name != "directory_list" || call.Arguments["path"] != "output" {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseInstructionToolCallSkipsUnbalancedPrefaceBrace(t *testing.T) {
	content := `前言含有不完整物件 {not-json，後方才是指令：{"type":"tool_use","tool":"directory_list","input":{"path":"docs"}}`
	call, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err != nil || !matched {
		t.Fatalf("call=%+v matched=%v err=%v", call, matched, err)
	}
	if call.Arguments["path"] != "docs" {
		t.Fatalf("call = %+v", call)
	}
}

func TestParseInstructionToolCallRejectsUnavailableEmbeddedTool(t *testing.T) {
	content := `準備執行：{"type":"tool_use","tool":"unknown_tool","input":{}}`
	_, matched, err := parseInstructionToolCall(content, instructionTestDefinitions())
	if err == nil || matched || !strings.Contains(err.Error(), "unavailable tool") {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
}

func TestInstructionMessagesAvoidNativeToolProtocol(t *testing.T) {
	messages := instructionMessages([]domain.Message{
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "directory_list", Arguments: map[string]any{"path": "."}}}},
		{Role: "tool", ToolCallID: "call_1", ToolName: "directory_list", Content: `{"entries":["target.txt"]}`},
	})
	if len(messages) != 2 || messages[0].Role != "assistant" || messages[1].Role != "user" {
		t.Fatalf("messages = %+v", messages)
	}
	for _, message := range messages {
		if len(message.ToolCalls) != 0 || message.ToolCallID != "" {
			t.Fatalf("native tool fields leaked into instruction transcript: %+v", message)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(message.Content), &payload); err != nil {
			t.Fatalf("instruction transcript is not JSON: %v", err)
		}
	}
	if !strings.Contains(messages[1].Content, "target.txt") || !strings.Contains(messages[1].Content, `"type":"tool_result"`) {
		t.Fatalf("tool result was not preserved: %s", messages[1].Content)
	}
}
