package harness

import (
	"AgenticService/src/domain"
	"context"
	"testing"
)

func TestModelSwitchAtTurnBoundarySmoke(t *testing.T) {
	selection := &domain.RunModelSelection{}
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_switch", Name: "shell_exec", Arguments: map[string]any{"command": "pwd"}}}},
		{Content: "完成"},
	}}
	calls := 0
	runner := newTestRunner(sessions, model, &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, session domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			calls++
			selection.Set("intermediate")
			selection.Set("new-model")
			if model.requests[0].Model != "old-model" {
				t.Fatal("切換改寫了當前請求")
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	})
	changes := 0
	_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "開始", Model: "old-model", ModelSelection: selection}, func(event domain.EngineEvent) error {
		if event.Type == "run.model_changed" {
			changes++
			if event.Payload["model"] != "new-model" || event.Payload["turn"] != 2 {
				t.Fatalf("切換事件：%v", event.Payload)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 || model.requests[1].Model != "new-model" || calls != 1 || changes != 1 {
		t.Fatalf("requests=%d calls=%d changes=%d", len(model.requests), calls, changes)
	}
	if got := historySummary(model.requests[1].History); got != "user:開始,assistant+calls,tool" {
		t.Fatalf("工具歷史配對遭破壞：%s", got)
	}
}

func TestThinkingSwitchAtTurnBoundarySmoke(t *testing.T) {
	for _, mode := range []string{"high", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			selection := &domain.RunModelSelection{}
			model := &scriptedModel{responses: []domain.ModelResponse{
				{ToolCalls: []domain.ToolCall{{ID: "call_thinking", Name: "shell_exec", Arguments: map[string]any{"command": "pwd"}}}},
				{Content: "完成"},
			}}
			calls := 0
			runner := newTestRunner(newMemorySessions(testSession()), model, &fakeTools{
				definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
				execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
					calls++
					selection.Set("new-model")
					selection.Update(nil, &mode)
					return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
				},
			})
			_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "開始", Model: "old-model", ThinkingMode: "low", ModelSelection: selection}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(model.requests) != 2 {
				t.Fatalf("calls=%d requests=%d", calls, len(model.requests))
			}
			if model.requests[0].ThinkingMode != "low" || model.requests[1].ThinkingMode != mode || model.requests[1].Model != "new-model" {
				t.Fatal("回合切換沒有合併模型與 Thinking，或改寫了當前請求")
			}
		})
	}
}
