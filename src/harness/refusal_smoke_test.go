package harness

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"testing"
)

func TestSmokeRefusalSkipsToolParsingAndCompletionChallenge(t *testing.T) {
	for _, mode := range []ToolCallMode{ToolCallModeNative, ToolCallModeInstruction} {
		t.Run(string(mode), func(t *testing.T) {
			model := &scriptedModel{responses: []domain.ModelResponse{{StopReason: "refusal", Content: `{"tool_calls":[{"name":"shell_exec","arguments":{"command":"example"}}]}`, ToolCalls: []domain.ToolCall{{ID: "call_fake", Name: "shell_exec"}}}}}
			tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "shell_exec"}}, execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
				t.Fatal("refusal executed tool")
				return domain.ToolExecution{}, nil
			}}
			runner := newTestRunner(newMemorySessions(testSession()), model, tools)
			runner.ToolCallMode = mode
			runner.MaxCompletionChecks = 2
			result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行任務"}, nil)
			if err != nil || result.Message.StopReason != "refusal" || len(model.requests) != 1 {
				t.Fatalf("result=%+v calls=%d err=%v", result, len(model.requests), err)
			}
		})
	}
}

func TestSmokeContinuationSurvivesHarnessToolRound(t *testing.T) {
	state := &domain.ModelContinuation{Protocol: "responses", Scope: "synthetic-provider", Model: "test", Items: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"synthetic"}`)}}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_read", Name: "file_read"}}, Continuation: state},
		{Content: "已完成"},
	}}
	runner := newTestRunner(newMemorySessions(testSession()), model, &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}})
	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀取檔案"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("turns=%d", len(model.requests))
	}
	for _, message := range model.requests[1].History {
		if message.Continuation == nil {
			continue
		}
		if message.Continuation == state || string(message.Continuation.Items[0]) != string(state.Items[0]) {
			t.Fatal("continuation lost or aliased")
		}
		return
	}
	t.Fatal("tool round history omitted encrypted reasoning")
}
