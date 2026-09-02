package harness

import (
	"AgenticService/src/domain"
	"errors"
	"strings"
	"testing"
)

func instructionDefinitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{Name: "shell_exec"},
		{Name: "mcp__IntegTERM__execute_single_ssh_command"},
	}
}

// 被輸出長度上限截斷的工具指令不能當成最終回答：那會把半截 JSON（連同參數裡的
// 憑證）直接顯示給使用者，而且該做的工作根本沒有執行。
func TestTruncatedToolInstructionIsAProtocolError(t *testing.T) {
	content := `照你的要求用 sudo kill 停止並以 sudo 重啟。
{"type":"tool_use","tool":"mcp__IntegTERM__execute_single_ssh_command","input":{"command":"set -eu\nsudo python3 - <<'PY'\nimport json\nroot = '/home/ubuntu/services","site":{"folder":"新版系統","password":"`

	_, matched, err := parseInstructionToolCall(content, instructionDefinitions())

	if matched {
		t.Fatal("a truncated instruction must not be treated as a tool call")
	}
	if !errors.Is(err, domain.ErrProviderProtocol) {
		t.Fatalf("expected a provider protocol error, got %v", err)
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error should explain the truncation: %v", err)
	}
}

func TestCompleteInstructionAfterProseStillRuns(t *testing.T) {
	content := `我先看一下目錄。
{"type":"tool_use","tool":"shell_exec","input":{"command":"ls"},"reason":"列出檔案"}`

	call, matched, err := parseInstructionToolCall(content, instructionDefinitions())

	if err != nil || !matched {
		t.Fatalf("expected a tool call, got matched=%v err=%v", matched, err)
	}
	if call.Name != "shell_exec" {
		t.Fatalf("call name = %q", call.Name)
	}
}

// 一般回答就算提到大括號或半段 JSON 範例，也不能被誤判成截斷的工具指令。
func TestPlainAnswersAreNotTreatedAsTruncatedInstructions(t *testing.T) {
	cases := map[string]string{
		"prose":                "已完成，設定檔的 { 需要成對出現。",
		"balanced json sample": `結果如下：{"status":"ok","items":[1,2]}`,
		"unrelated fragment":   `輸出被截斷了：{"status":"ok",`,
		"code fence":           "```json\n{\"status\": \"ok\"}\n```",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, matched, err := parseInstructionToolCall(content, instructionDefinitions())
			if matched || err != nil {
				t.Fatalf("expected a plain answer, got matched=%v err=%v", matched, err)
			}
		})
	}
}

// 複合問題需要兩份彼此獨立的資料。協定必須允許同一輪送出多個工具指令，
// 否則模型只能花一輪去規劃怎麼分次查。
func TestParseInstructionToolCallsAcceptsMultipleInstructions(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "mcp__mars-mes__query_departments"},
		{Name: "mcp__mars-mes__query_operators"},
	}
	content := `[{"type":"tool_use","tool":"mcp__mars-mes__query_departments","input":{}},
{"type":"tool_use","tool":"mcp__mars-mes__query_operators","input":{"active":true}}]`

	calls, matched, err := parseInstructionToolCalls(content, definitions)

	if err != nil || !matched {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].Name != "mcp__mars-mes__query_departments" || calls[1].Name != "mcp__mars-mes__query_operators" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[1].Arguments["active"] != true {
		t.Fatalf("second call arguments = %+v", calls[1].Arguments)
	}
	if calls[0].ID == calls[1].ID {
		t.Fatal("each instruction needs its own tool call id")
	}
}

func TestParseInstructionToolCallsKeepsSingleInstructionBehaviour(t *testing.T) {
	definitions := []domain.ToolDefinition{{Name: "shell_exec"}}

	calls, matched, err := parseInstructionToolCalls(`我先看一下。
{"type":"tool_use","tool":"shell_exec","input":{"command":"ls"}}`, definitions)

	if err != nil || !matched || len(calls) != 1 || calls[0].Name != "shell_exec" {
		t.Fatalf("calls=%+v matched=%v err=%v", calls, matched, err)
	}
}

// 多個指令中只要有一個工具不存在，就必須走協定修正，不能執行一半。
func TestParseInstructionToolCallsRejectsUnknownToolInBatch(t *testing.T) {
	definitions := []domain.ToolDefinition{{Name: "mcp__mars-mes__query_departments"}}
	content := `{"type":"tool_use","tool":"mcp__mars-mes__query_departments","input":{}}
{"type":"tool_use","tool":"mcp__other__query","input":{}}`

	_, matched, err := parseInstructionToolCalls(content, definitions)

	if matched || !errors.Is(err, domain.ErrProviderProtocol) {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
}
