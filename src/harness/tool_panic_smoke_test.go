package harness

import (
	"AgenticService/src/domain"
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParallelToolPanicPreservesResultsSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	runtime := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			if call.ID == "panic" {
				panic("synthetic-private-payload")
			}
			return domain.ToolExecution{Content: "正常結果"}, nil
		},
	}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "panic", Name: "file_read"}, {ID: "ok", Name: "file_read"}}},
		{Content: "部分讀取失敗"},
	}}
	runner := newTestRunner(sessions, model, runtime)
	var logs bytes.Buffer
	runner.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, Input{Session: testSession(), UserInput: "讀取"}, nil); err != nil {
		t.Fatal(err)
	}
	messages, err := sessions.ListMessages(ctx, testSession().ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		count++
		if m.ToolCallID == "panic" && (!m.IsError || domain.ToolExecutionState(m.Metadata) != domain.ToolOutcomeUnknown) {
			t.Fatalf("未保留未知狀態：%+v", m)
		}
		if m.ToolCallID == "ok" && (m.IsError || m.Content != "正常結果") {
			t.Fatalf("正常結果遺失：%+v", m)
		}
		if strings.Contains(m.Content, "synthetic-private-payload") {
			t.Fatal("洩漏 panic 內容")
		}
	}
	if count != 2 {
		t.Fatalf("結果數量=%d", count)
	}
	if strings.Contains(logs.String(), "synthetic-private-payload") {
		t.Fatal("日誌洩漏 panic 內容")
	}
	assertToolCallProtocol(t, messages)
}

func TestToolPanicDispatchBoundaryAndReplaySmoke(t *testing.T) {
	for _, phase := range []string{"before", "execute", "after"} {
		t.Run(phase, func(t *testing.T) {
			calls := 0
			runtime := &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
				calls++
				if phase == "execute" {
					panic("synthetic")
				}
				return domain.ToolExecution{Content: "已執行"}, nil
			}}
			runner := &Runner{Tools: runtime}
			if phase == "before" {
				runner.BeforeTool = func(context.Context, domain.Session, domain.ToolCall) error { panic("synthetic") }
			}
			if phase == "after" {
				runner.AfterTool = func(context.Context, domain.Session, domain.ToolCall, domain.ToolExecution) domain.ToolExecution {
					panic("synthetic")
				}
			}
			guard := newToolLoopGuard([]domain.ToolDefinition{{Name: "mutate"}})
			call := domain.ToolCall{ID: "call", Name: "mutate"}
			outcome := runner.executeToolCall(context.Background(), testSession(), call, &serializedSink{}, "run", true, true, newRunApprovalState(false), guard, nil)
			state := domain.ToolExecutionState(outcome.result.Details)
			if outcome.err != nil || !outcome.result.IsError {
				t.Fatalf("未轉換為工具觀察：%+v", outcome)
			}
			if phase == "before" {
				if state != domain.ToolNotDispatched || calls != 0 {
					t.Fatal("派送前異常被誤判", state, calls)
				}
			} else {
				if state != domain.ToolOutcomeUnknown || calls != 1 {
					t.Fatal("派送後異常被誤判", state, calls)
				}
				guarded, skip := guard.before(call)
				if !skip || guarded.Details["outcome_unknown"] != true {
					t.Fatal("未知副作用可被自動重送")
				}
			}
		})
	}
}

func TestUnknownOrUndispatchedShellDoesNotEnableFallbackSmoke(t *testing.T) {
	for _, state := range []string{domain.ToolNotDispatched, domain.ToolOutcomeUnknown, domain.ToolFailed} {
		result := domain.ToolExecution{IsError: true, Details: map[string]any{"execution_state": state}}
		got := shouldEnableBuiltinFallback(domain.ToolCall{Name: systemShellToolName}, result)
		if got != (state == domain.ToolFailed) {
			t.Fatalf("state=%s fallback=%v", state, got)
		}
	}
}
