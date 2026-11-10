package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunPersistsUndispatchedCallsOnExitSmoke(t *testing.T) {
	for _, scenario := range []string{"assistant-event", "tool-start", "tool-end", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sessions := newMemorySessions(testSession())
			calls := []domain.ToolCall{{ID: "first", Name: "mutate"}, {ID: "second", Name: "mutate"}}
			model := &scriptedModel{responses: []domain.ModelResponse{{ToolCalls: calls}, {Content: "結束"}}}
			executed := 0
			runtime := &fakeTools{definitions: []domain.ToolDefinition{{Name: "mutate"}}, execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
				executed++
				if scenario == "cancel" {
					cancel()
				}
				return domain.ToolExecution{Content: "已確認成功", Details: map[string]any{"execution_state": domain.ToolSucceeded}}, nil
			}}
			runner := newTestRunner(sessions, model, runtime)
			runner.Logger = logging.Discard()
			_, err := runner.Run(ctx, Input{Session: testSession(), UserInput: "執行兩項工作"}, func(event domain.EngineEvent) error {
				if scenario == "assistant-event" && event.Type == "message.end" {
					if message, ok := event.Payload["message"].(domain.Message); ok && len(message.ToolCalls) > 0 {
						return errors.New("synthetic assistant event failure")
					}
				}
				if (scenario == "tool-start" && event.Type == "tool.execution.start") || (scenario == "tool-end" && event.Type == "tool.execution.end") {
					return errors.New("synthetic tool event failure")
				}
				return nil
			})
			if err == nil {
				t.Fatal("預期失敗或取消")
			}
			messages, err := sessions.ListMessages(context.Background(), testSession().ID)
			if err != nil {
				t.Fatal(err)
			}
			assertToolCallProtocol(t, messages)
			states := map[string]string{}
			for _, message := range messages {
				if message.Role == "tool" {
					states[message.ToolCallID] = domain.ToolExecutionState(message.Metadata)
				}
			}
			wantExecuted := 0
			if scenario == "tool-end" || scenario == "cancel" {
				wantExecuted = 1
				if states["first"] != domain.ToolSucceeded {
					t.Fatalf("已派送結果被改寫：%v", states)
				}
			} else if states["first"] != domain.ToolNotDispatched {
				t.Fatalf("未派送事實未保存：%v", states)
			}
			if executed != wantExecuted || states["second"] != domain.ToolNotDispatched {
				t.Fatalf("離開後仍有未配對呼叫：executed=%d states=%v", executed, states)
			}
			if _, err := sessions.LatestEntryOfType(context.Background(), testSession().ID, domain.SessionEntryTurnFinished); err != nil {
				t.Fatalf("缺少回合收尾：%v", err)
			}
		})
	}
}

func TestBudgetPersistsPendingBeforeEventsSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{{ToolCalls: []domain.ToolCall{{ID: "first", Name: "mutate"}, {ID: "second", Name: "mutate"}, {ID: "third", Name: "mutate"}}}}}
	runner := newTestRunner(sessions, model, &fakeTools{definitions: []domain.ToolDefinition{{Name: "mutate"}}})
	runner.Logger = logging.Discard()
	runner.Budget.MaxToolCalls = 1
	_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "工作"}, func(event domain.EngineEvent) error {
		if message, ok := event.Payload["message"].(domain.Message); ok && message.Role == "tool" && message.ToolCallID == "second" {
			return errors.New("synthetic pending event failure")
		}
		return nil
	})
	if err == nil {
		t.Fatal("預期事件失敗")
	}
	messages, _ := sessions.ListMessages(context.Background(), testSession().ID)
	assertToolCallProtocol(t, messages)
}

func TestRunPanicRecordsFailureSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{onStream: func(int) { panic("synthetic-private-value") }}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Logger = logging.Discard()
	var outcome any
	func() {
		defer func() { _ = recover() }()
		_, _ = runner.Run(context.Background(), Input{Session: testSession(), UserInput: "工作"}, func(event domain.EngineEvent) error {
			if event.Type == "agent.end" {
				outcome = event.Payload["outcome"]
			}
			return nil
		})
	}()
	finished, err := sessions.LatestEntryOfType(context.Background(), testSession().ID, domain.SessionEntryOperationFinished)
	if err != nil || finished.Data["status"] != domain.OperationStatusFailed || outcome != domain.OperationStatusFailed {
		t.Fatalf("panic 被記為正常完成：entry=%+v outcome=%v err=%v", finished.Data, outcome, err)
	}
	if text, _ := finished.Data["error"].(string); strings.Contains(text, "synthetic-private-value") {
		t.Fatal("panic 內容洩漏")
	}
}

type harnessJournalProbe struct {
	*memorySessions
	afterAppend  func(domain.SessionEntry)
	beforeAppend func(domain.SessionEntry) error
	page         func(context.Context, int64) ([]domain.SessionEntry, bool, error)
}

func (s *harnessJournalProbe) AppendEntry(ctx context.Context, id string, entry domain.SessionEntry) (domain.SessionEntry, error) {
	if s.beforeAppend != nil {
		if err := s.beforeAppend(entry); err != nil {
			return domain.SessionEntry{}, err
		}
	}
	saved, err := s.memorySessions.AppendEntry(ctx, id, entry)
	if err == nil && s.afterAppend != nil {
		s.afterAppend(entry)
	}
	return saved, err
}

func (s *harnessJournalProbe) ListEntriesPage(ctx context.Context, id string, after int64, limit int) ([]domain.SessionEntry, bool, error) {
	if s.page != nil {
		return s.page(ctx, after)
	}
	return s.memorySessions.ListEntriesPage(ctx, id, after, limit)
}

func TestCancelAfterDispatchIntentPreventsExecutionSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executed := false
	runner := &Runner{
		Sessions: &harnessJournalProbe{memorySessions: newMemorySessions(testSession()), afterAppend: func(entry domain.SessionEntry) {
			if entry.Type == domain.SessionEntryToolDispatched {
				cancel()
			}
		}},
		Tools: &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
			executed = true
			return domain.ToolExecution{Content: "不應執行"}, nil
		}},
	}
	outcome := runner.executeToolCall(ctx, testSession(), domain.ToolCall{ID: "call", Name: "mutate"}, &serializedSink{}, "run", true, true, newRunApprovalState(false), nil, nil)
	if executed || domain.ToolExecutionState(outcome.result.Details) != domain.ToolNotDispatched {
		t.Fatalf("取消後仍派送：executed=%v result=%+v", executed, outcome.result)
	}
}

func TestAfterHookCannotInventExecutionSuccessSmoke(t *testing.T) {
	for _, scenario := range []string{"cancel", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := &Runner{Tools: &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
				if scenario == "cancel" {
					cancel()
					return domain.ToolExecution{}, nil
				}
				return domain.ToolExecution{IsError: true, Content: "確認失敗"}, nil
			}}, AfterTool: func(context.Context, domain.Session, domain.ToolCall, domain.ToolExecution) domain.ToolExecution {
				return domain.ToolExecution{Content: "成功", Details: map[string]any{"execution_state": domain.ToolSucceeded}}
			}}
			outcome := runner.executeToolCall(ctx, testSession(), domain.ToolCall{ID: "call", Name: "mutate"}, &serializedSink{}, "run", true, true, newRunApprovalState(false), nil, nil)
			want := domain.ToolFailed
			if scenario == "cancel" {
				want = domain.ToolOutcomeUnknown
			}
			if !outcome.result.IsError || domain.ToolExecutionState(outcome.result.Details) != want {
				t.Fatalf("hook 改報成功：%+v", outcome.result)
			}
		})
	}
}

func TestRecoveryRejectsInvalidPagesSmoke(t *testing.T) {
	for _, scenario := range []string{"empty-more", "repeated-sequence", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			store := &harnessJournalProbe{memorySessions: newMemorySessions(testSession()), page: func(_ context.Context, _ int64) ([]domain.SessionEntry, bool, error) {
				calls++
				if scenario == "empty-more" {
					return nil, true, nil
				}
				if scenario == "canceled" {
					return nil, false, nil
				}
				if calls > 2 {
					return nil, false, errors.New("synthetic loop escape")
				}
				return []domain.SessionEntry{{Sequence: 1}}, true, nil
			}}
			if scenario == "canceled" {
				cancel()
			}
			guard := newToolLoopGuard(nil)
			err := guard.restoreUncertain(ctx, store, testSession().ID)
			if scenario == "canceled" {
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatalf("取消後仍掃描：calls=%d err=%v", calls, err)
				}
			} else if !errors.Is(err, domain.ErrConflict) || calls > 2 {
				t.Fatalf("分頁未 fail closed：calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestUnknownFailureSurvivesReusedCallIDSmoke(t *testing.T) {
	tracker := newCompletionTracker([]domain.ToolDefinition{{Name: "mutate"}})
	call := domain.ToolCall{ID: "same", Name: "mutate", Arguments: map[string]any{"target": "A"}}
	tracker.observe(call, domain.ToolExecution{IsError: true, Content: "第一筆結果未知", Details: map[string]any{"execution_state": domain.ToolOutcomeUnknown}})
	call.Arguments = map[string]any{"target": "B"}
	tracker.observe(call, domain.ToolExecution{IsError: true, Content: "第二筆失敗", Details: map[string]any{"execution_state": domain.ToolFailed}})
	tracker.observe(call, domain.ToolExecution{Content: "第二筆成功", Details: map[string]any{"execution_state": domain.ToolSucceeded}})
	remaining := tracker.unresolved()
	if len(remaining) != 1 || remaining[0].Summary != "第一筆結果未知" {
		t.Fatalf("重複 ID 抹除了舊未知操作：%+v", remaining)
	}
}

func TestInvalidApprovalCannotDispatchSmoke(t *testing.T) {
	executed := false
	runner := &Runner{
		Approvals: &fakeApprovals{required: map[string]bool{"mutate": true}, decision: domain.ToolApprovalDecision{Decision: "invalid"}},
		Tools: &fakeTools{execute: func(context.Context, domain.Session, domain.ToolCall) (domain.ToolExecution, error) {
			executed = true
			return domain.ToolExecution{}, nil
		}},
	}
	outcome := runner.executeToolCall(context.Background(), testSession(), domain.ToolCall{ID: "call", Name: "mutate"}, &serializedSink{}, "run", true, false, newRunApprovalState(false), nil, nil)
	if executed || domain.ToolExecutionState(outcome.result.Details) != domain.ToolNotDispatched || !outcome.result.IsError {
		t.Fatalf("無效核准仍派送：executed=%v result=%+v", executed, outcome.result)
	}
}

func TestAgentEndCannotPrecedeFailedTerminalWriteSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	runner := newTestRunner(sessions, &scriptedModel{responses: []domain.ModelResponse{{Content: "完成"}}}, &fakeTools{})
	runner.Logger = logging.Discard()
	for _, fail := range []string{"journal", "event"} {
		t.Run(fail, func(t *testing.T) {
			terminalWrites := 0
			runner.Sessions = &harnessJournalProbe{memorySessions: sessions, beforeAppend: func(entry domain.SessionEntry) error {
				if entry.Type == domain.SessionEntryOperationFinished {
					terminalWrites++
					if fail == "journal" {
						return errors.New("synthetic terminal write failure")
					}
				}
				return nil
			}}
			outcome := ""
			_, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "工作"}, func(event domain.EngineEvent) error {
				if event.Type == "agent.end" {
					if terminalWrites == 0 {
						t.Error("終態尚未保存就送出 agent.end")
					}
					outcome, _ = event.Payload["outcome"].(string)
					if fail == "event" {
						return errors.New("synthetic end event failure")
					}
				}
				return nil
			})
			if err == nil {
				t.Fatal("未回報終態保存或事件錯誤")
			}
			if fail == "journal" && outcome != domain.OperationStatusFailed {
				t.Fatalf("寫入失敗卻宣告成功：%s", outcome)
			}
			if fail == "event" {
				entry, err := sessions.LatestEntryOfType(context.Background(), testSession().ID, domain.SessionEntryOperationFinished)
				if err != nil || entry.Data["status"] != domain.OperationStatusFailed {
					t.Fatalf("事件失敗未反映到紀錄：%v %v", entry.Data, err)
				}
			}
		})
	}
}

func TestRecoveryCannotEraseUnknownWithLateDuplicateResultSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	call := domain.ToolCall{ID: "call", Name: "mutate", Arguments: map[string]any{"target": "A"}}
	appendTestMessage(t, sessions, assistantWithCalls("assistant", call))
	for _, state := range []string{domain.ToolOutcomeUnknown, domain.ToolSucceeded} {
		appendTestMessage(t, sessions, domain.Message{Role: "tool", ToolCallID: call.ID, Metadata: map[string]any{"execution_state": state}})
	}
	guard := newToolLoopGuard([]domain.ToolDefinition{{Name: "mutate"}})
	if err := guard.restoreUncertain(context.Background(), sessions, testSession().ID); err != nil {
		t.Fatal(err)
	}
	if !guard.uncertainSignatures[toolCallSignature(call)] {
		t.Fatal("晚到的重複結果抹除了已保存的未知事實")
	}
}
