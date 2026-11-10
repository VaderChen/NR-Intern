package harness

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestInstructionBatchValidationSmoke(t *testing.T) {
	definitions := []domain.ToolDefinition{{Name: "mutate"}}
	valid := `{"type":"tool_use","tool":"mutate","input":{}}`
	many := make([]string, 10)
	for i := range many {
		many[i] = fmt.Sprintf(`{"id":"call_%d","type":"tool_use","tool":"mutate","input":{}}`, i)
	}
	t.Run("保留超過並行上限的呼叫", func(t *testing.T) {
		calls, matched, err := parseInstructionToolCalls("["+strings.Join(many, ",")+"]", definitions)
		if err != nil || !matched || len(calls) != len(many) {
			t.Fatalf("呼叫被截斷：count=%d matched=%v err=%v", len(calls), matched, err)
		}
	})
	for name, content := range map[string]string{
		"後段未完成":     valid + ` {"type":"tool_use","tool":"mutate","input":{"value":"`,
		"ID 在前的截斷":  valid + ` {"id":"later","tool":"mutate","input":`,
		"後段無效 JSON": valid + ` {"type":"tool_use","tool":"mutate","input":{broken}}`,
		"陣列未結束":     "[" + valid,
		"陣列漏逗號":     "[" + valid + valid + "]",
		"重複 ID":     `{"id":"same","tool":"mutate"} {"id":"same","tool":"mutate"}`,
		"第九筆工具不存在":  strings.Join(many[:8], " ") + ` {"tool":"missing"}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls, matched, err := parseInstructionToolCalls(content, definitions)
			if len(calls) != 0 || matched || !errors.Is(err, domain.ErrProviderProtocol) {
				t.Fatalf("不完整批次未被拒絕：count=%d matched=%v err=%v", len(calls), matched, err)
			}
		})
	}
	t.Run("數字精度與名稱正規化", func(t *testing.T) {
		calls, matched, err := parseInstructionToolCalls(`{"tool":"MUTATE","input":{"id":9007199254740993}}`, definitions)
		if err != nil || !matched || len(calls) != 1 {
			t.Fatalf("parse: %v", err)
		}
		if calls[0].Name != "mutate" || calls[0].Arguments["id"] != json.Number("9007199254740993") {
			t.Fatalf("工具或數字被改變：%+v", calls[0])
		}
	})
	t.Run("正常 JSON 片段不誤判", func(t *testing.T) {
		for _, content := range []string{`這只是範例：{"type":"person","name":"`, `{"name":"一般報告"}`, `[{"name":"mutate"}]`, `{"text":"陣列 [ 與物件 { 是範例"}`} {
			_, matched, err := parseInstructionToolCalls(content, definitions)
			if matched || err != nil {
				t.Fatalf("正常回答誤判：matched=%v err=%v", matched, err)
			}
		}
	})
}

func TestInstructionRepairDoesNotDispatchPartialBatchSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	executed := 0
	runtime := &fakeTools{definitions: []domain.ToolDefinition{{Name: "mutate"}}, execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
		executed++
		return domain.ToolExecution{Content: "已執行"}, nil
	}}
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: `{"tool":"mutate"} {"type":"tool_use","tool":"mutate","input":`}}}
	runner := newTestRunner(sessions, model, runtime)
	runner.ToolCallMode = ToolCallModeInstruction
	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行工作"}, nil)
	if err != nil || executed != 0 || len(model.requests) != maxToolProtocolRepairAttempts+1 {
		t.Fatalf("修復耗盡後不得執行前段：executed=%d requests=%d err=%v", executed, len(model.requests), err)
	}
	if !strings.Contains(result.Message.Content, "無法繼續執行") {
		t.Fatalf("缺少可見的收尾：%q", result.Message.Content)
	}
}
