package harness

import (
	"AgenticService/src/domain"
	"context"
	"strings"
	"testing"
	"time"
)

func historySummary(history []domain.Message) string {
	parts := make([]string, 0, len(history))
	for _, message := range history {
		label := message.Role
		if message.Role == "user" {
			label += ":" + message.Content
		}
		if len(message.ToolCalls) > 0 {
			label += "+calls"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ",")
}

// 工具執行期間送達的訊息不能打斷工具，必須在下一輪請求中出現，且排在工具結果之後。
func TestRunInputInsertedAfterToolResults(t *testing.T) {
	sessions := newMemorySessions(testSession())
	queue := domain.NewRunInputQueue()
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "shell_exec", Arguments: map[string]any{"command": "pwd"}}}},
		{Content: "完成"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			if !queue.Push(domain.RunInputMessage{ID: "input_1", Content: "順便看一下 README", ClientID: "c1", CreatedAt: time.Now()}) {
				t.Error("push rejected while run active")
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	})
	var applied []map[string]any
	var userEvents int
	result, err := runner.Run(context.Background(), Input{RunID: "run_1", Session: testSession(), UserInput: "開始", Inputs: queue}, func(event domain.EngineEvent) error {
		switch event.Type {
		case "run.input_applied":
			applied = append(applied, event.Payload)
		case "message.end":
			if message, ok := event.Payload["message"].(domain.Message); ok && message.Role == "user" {
				userEvents++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Message.Content != "完成" {
		t.Fatalf("final message = %q", result.Message.Content)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	got := historySummary(model.requests[1].History)
	want := "user:開始,assistant+calls,tool,user:順便看一下 README"
	if got != want {
		t.Fatalf("second request history = %s, want %s", got, want)
	}
	inserted := model.requests[1].History[3]
	if inserted.Metadata["inserted"] != true || inserted.Metadata["input_id"] != "input_1" || inserted.Metadata["client_id"] != "c1" {
		t.Fatalf("inserted metadata = %#v", inserted.Metadata)
	}
	// 第一輪的使用者輸入走 UserPrompt，不在 History；插入的訊息不可提前出現。
	if strings.Contains(historySummary(model.requests[0].History), "README") || strings.Contains(model.requests[0].UserPrompt, "README") {
		t.Fatalf("first request should not contain inserted input: %s", historySummary(model.requests[0].History))
	}
	if len(applied) != 1 || applied[0]["turn"] != 2 {
		t.Fatalf("input_applied events = %#v", applied)
	}
	if userEvents != 2 {
		t.Fatalf("user message.end events = %d, want 2", userEvents)
	}
	if queue.Pending() {
		t.Fatal("queue should be drained")
	}
}

// 模型準備收尾時送達的訊息會讓 Run 多跑一輪，最後的回答回應補充內容。
func TestRunInputBeforeCompletionAddsTurn(t *testing.T) {
	sessions := newMemorySessions(testSession())
	queue := domain.NewRunInputQueue()
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "第一版回答"},
		{Content: "已依補充修正"},
	}}
	model.onStream = func(turn int) {
		if turn == 0 {
			queue.Push(domain.RunInputMessage{ID: "input_late", Content: "改用表格", CreatedAt: time.Now()})
		}
	}
	runner := newTestRunner(sessions, model, &fakeTools{})
	result, err := runner.Run(context.Background(), Input{RunID: "run_2", Session: testSession(), UserInput: "整理", Inputs: queue}, func(event domain.EngineEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Message.Content != "已依補充修正" {
		t.Fatalf("final message = %q", result.Message.Content)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	got := historySummary(model.requests[1].History)
	want := "user:整理,assistant,user:改用表格"
	if got != want {
		t.Fatalf("second request history = %s, want %s", got, want)
	}
}

// 沒有插入佇列（nil）時行為與原本相同。
func TestRunWithoutInputQueueUnchanged(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "完成"}}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	result, err := runner.Run(context.Background(), Input{RunID: "run_3", Session: testSession(), UserInput: "hi"}, nil)
	if err != nil || result.Message.Content != "完成" || len(model.requests) != 1 {
		t.Fatalf("result=%q err=%v requests=%d", result.Message.Content, err, len(model.requests))
	}
}

func TestRunInputQueueCloseReturnsPending(t *testing.T) {
	queue := domain.NewRunInputQueue()
	queue.Push(domain.RunInputMessage{ID: "a", Content: "one"})
	queue.Push(domain.RunInputMessage{ID: "b", Content: "two"})
	if queue.Push(domain.RunInputMessage{ID: "c", Content: "   "}) {
		t.Fatal("blank input accepted")
	}
	remaining := queue.Close()
	if len(remaining) != 2 || remaining[0].ID != "a" || remaining[1].ID != "b" {
		t.Fatalf("remaining = %#v", remaining)
	}
	if queue.Push(domain.RunInputMessage{ID: "d", Content: "late"}) {
		t.Fatal("push accepted after close")
	}
	if len(queue.Close()) != 0 {
		t.Fatal("second close should return nothing")
	}
}
