package openaicompat

import (
	"AgenticService/src/domain"
	"fmt"
	"strings"
	"testing"
)

// 同時覆蓋完整區塊與訊息先結束的封包；工具僅能在明確終態及完整參數下交付。
func TestClaudeTerminalSnapshotSmoke(t *testing.T) {
	for _, closeBlock := range []bool{false, true} {
		for _, tc := range []struct {
			name, block, delta, reason, wantReason string
			wantCalls                              int
			wantError                              bool
		}{
			{"text", `{"type":"text","text":"中文"}`, `{"type":"text_delta","text":"回答"}`, "end_turn", "stop", 0, false},
			{"tool", `{"type":"tool_use","id":"t1","name":"write","input":{}}`, `{"type":"input_json_delta","partial_json":"{\"text\":\"完成\"}"}`, "tool_use", "tool_calls", 1, false},
			{"partial_tool", `{"type":"tool_use","id":"t1","name":"write","input":{}}`, `{"type":"input_json_delta","partial_json":"{\"text\":"}`, "tool_use", "", 0, true},
			{"truncated_tool", `{"type":"tool_use","id":"t1","name":"write","input":{}}`, `{"type":"input_json_delta","partial_json":"{\"text\":"}`, "max_tokens", "length", 0, false},
			{"context_limit", `{"type":"tool_use","id":"t1","name":"write","input":{}}`, `{"type":"input_json_delta","partial_json":"{\"text\":"}`, "model_context_window_exceeded", "length", 0, false},
			{"truncated_thinking", `{"type":"thinking","thinking":""}`, `{"type":"thinking_delta","thinking":"尚未完成"}`, "max_tokens", "length", 0, false},
			{"unsigned_thinking", `{"type":"thinking","thinking":""}`, `{"type":"thinking_delta","thinking":"內容"}`, "end_turn", "", 0, true},
			{"missing_reason", `{"type":"text","text":"文字"}`, `{"type":"text_delta","text":""}`, "", "", 0, true},
			{"unknown_reason", `{"type":"text","text":"文字"}`, `{"type":"text_delta","text":""}`, "unknown", "", 0, true},
			{"missing_tool", `{"type":"text","text":"文字"}`, `{"type":"text_delta","text":""}`, "tool_use", "", 0, true},
		} {
			t.Run(fmt.Sprintf("%s/block_stop=%t", tc.name, closeBlock), func(t *testing.T) {
				events := []string{`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`, `{"type":"content_block_start","index":0,"content_block":` + tc.block + `}`, `{"type":"content_block_delta","index":0,"delta":` + tc.delta + `}`}
				if closeBlock {
					events = append(events, `{"type":"content_block_stop","index":0}`)
				}
				events = append(events, fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":4}}`, tc.reason), `{"type":"message_delta","delta":{},"usage":{"output_tokens":5}}`, `{"type":"message_stop"}`)
				progress := 0
				result, err := decodeClaude(fragmentedReader{strings.NewReader(claudeSmokeStream(events...))}, true, "synthetic", "", func(e domain.ModelEvent) error {
					if e.Type == domain.ModelEventProgress {
						progress++
					}
					return nil
				})
				if (err != nil) != tc.wantError {
					t.Fatalf("錯誤不符預期：%v", err)
				}
				if len(result.ToolCalls) != tc.wantCalls {
					t.Fatalf("工具交付不符預期：%d", len(result.ToolCalls))
				}
				if tc.wantError {
					return
				}
				if result.StopReason != tc.wantReason || result.Usage.TotalTokens != 8 {
					t.Fatalf("終態或累計用量不正確：%+v", result)
				}
				if tc.name == "text" && result.Content != "中文回答" {
					t.Fatal("文字未完整累積")
				}
				if tc.wantReason == "length" && (result.Continuation != nil || progress != 1) {
					t.Fatal("截斷仍保留續接狀態或未提示")
				}
			})
		}
	}
}

func TestClaudeJSONTruncationSmoke(t *testing.T) {
	for _, reason := range []string{"max_tokens", "model_context_window_exceeded"} {
		body := fmt.Sprintf(`{"type":"message","content":[{"type":"thinking","thinking":"未完成"},{"type":"tool_use","id":"t1","name":"write","input":null}],"stop_reason":%q}`, reason)
		result, err := decodeClaude(strings.NewReader(body), false, "synthetic", "", nil)
		if err != nil || result.StopReason != "length" || len(result.ToolCalls) != 0 || result.Continuation != nil {
			t.Fatalf("截斷未正確處理：%+v %v", result, err)
		}
	}
}

func TestClaudeNoTerminalNoToolsSmoke(t *testing.T) {
	body := claudeSmokeStream(`{"type":"message_start","message":{}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"write","input":{}}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
	result, err := decodeClaude(strings.NewReader(body), true, "synthetic", "", nil)
	if err == nil || len(result.ToolCalls) != 0 {
		t.Fatal("缺少訊息終態仍交付工具")
	}
}
