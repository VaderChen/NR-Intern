package harness

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type selectiveToolStoreFailure struct {
	*memorySessions
	failDispatch bool
	failMessage  bool
}

func (s *selectiveToolStoreFailure) AppendEntry(ctx context.Context, sessionID string, entry domain.SessionEntry) (domain.SessionEntry, error) {
	if s.failDispatch && entry.Type == domain.SessionEntryToolDispatched && entry.Data["tool_call_id"] == "first" {
		return domain.SessionEntry{}, errors.New("synthetic dispatch journal failure")
	}
	if s.failMessage && entry.Message != nil && entry.Message.Role == "tool" && entry.Message.ToolCallID == "first" {
		return domain.SessionEntry{}, errors.New("synthetic result write failure")
	}
	return s.memorySessions.AppendEntry(ctx, sessionID, entry)
}

func TestToolBatchPreservesOtherResultsOnFailureSmoke(t *testing.T) {
	for _, scenario := range []string{"event", "cancel", "dispatch", "result-write"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			sessions := newMemorySessions(testSession())
			var executed atomic.Int32
			release := make(chan struct{})
			runtime := &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}, execute: func(ctx context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
				if executed.Add(1) == 2 {
					close(release)
				}
				if scenario == "cancel" {
					select {
					case <-release:
					case <-ctx.Done():
						return domain.ToolExecution{}, ctx.Err()
					}
					cancel()
				}
				return domain.ToolExecution{Content: call.ID, Details: map[string]any{"execution_state": domain.ToolSucceeded}}, nil
			}}
			model := &scriptedModel{responses: []domain.ModelResponse{{ToolCalls: []domain.ToolCall{{ID: "first", Name: "file_read"}, {ID: "second", Name: "file_read"}}}}}
			runner := newTestRunner(sessions, model, runtime)
			runner.Sessions = &selectiveToolStoreFailure{memorySessions: sessions, failDispatch: scenario == "dispatch", failMessage: scenario == "result-write"}
			_, err := runner.Run(ctx, Input{Session: testSession(), UserInput: "合成批次"}, func(event domain.EngineEvent) error {
				if event.Type == "tool.execution.end" && scenario == "event" {
					return errors.New("synthetic event failure")
				}
				if event.Type == "tool.execution.end" && scenario == "cancel" {
					return ctx.Err()
				}
				return nil
			})
			if err == nil {
				t.Fatal("應保留並回報失敗")
			}
			messages, _ := sessions.ListMessages(context.Background(), testSession().ID)
			results := map[string]domain.Message{}
			for _, m := range messages {
				if m.Role == "tool" {
					results[m.ToolCallID] = m
				}
			}
			if result, ok := results["second"]; !ok || result.Content != "second" {
				t.Fatalf("其他已執行結果遺失：%v", results)
			}
			if scenario == "dispatch" {
				if executed.Load() != 1 || domain.ToolExecutionState(results["first"].Metadata) != domain.ToolNotDispatched {
					t.Fatal("未派送被誤記為已執行", results)
				}
			} else if executed.Load() != 2 {
				t.Fatal("合成工具未全部執行")
			}
			if scenario != "result-write" {
				assertToolCallProtocol(t, messages)
			}
		})
	}
}

func TestAfterToolCannotEraseDispatchFactsSmoke(t *testing.T) {
	for _, state := range []string{domain.ToolNotDispatched, domain.ToolOutcomeUnknown} {
		runner := &Runner{Tools: &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{IsError: true, Details: map[string]any{"execution_state": state}}, nil
		}}, AfterTool: func(context.Context, domain.Session, domain.ToolCall, domain.ToolExecution) domain.ToolExecution {
			return domain.ToolExecution{Content: "格式化回覆", Details: map[string]any{"execution_state": domain.ToolSucceeded}}
		}}
		guard := newToolLoopGuard([]domain.ToolDefinition{{Name: "mutate"}})
		outcome := runner.executeToolCall(context.Background(), testSession(), domain.ToolCall{ID: "call", Name: "mutate"}, &serializedSink{}, "run", true, true, newRunApprovalState(false), guard, nil)
		if !outcome.result.IsError || domain.ToolExecutionState(outcome.result.Details) != state {
			t.Fatalf("hook 抹除派送事實：%+v", outcome.result)
		}
	}
}
