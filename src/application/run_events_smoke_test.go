package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type terminalEventStore struct {
	ports.RunEventRepository
	entered          chan struct{}
	release          chan struct{}
	once             sync.Once
	failOnce         bool
	writeBeforeError bool
	panicOnce        bool
}

func (r *terminalEventStore) Append(ctx context.Context, event domain.Event) error {
	if terminalEvent(event.Type) {
		first := false
		r.once.Do(func() { first = true })
		if first {
			if r.panicOnce {
				panic("synthetic-event-store-panic")
			}
			if r.entered != nil {
				close(r.entered)
			}
			if r.release != nil {
				<-r.release
			}
			if r.failOnce {
				if r.writeBeforeError {
					if err := r.RunEventRepository.Append(ctx, event); err != nil {
						return err
					}
				}
				return errors.New("synthetic terminal write failure")
			}
		}
	}
	return r.RunEventRepository.Append(ctx, event)
}

func eventSmokeService(t *testing.T) (*Service, *fakeEngine, *terminalEventStore) {
	t.Helper()
	s, engine := newTestService(t, lockedPolicy())
	runs, err := filestore.NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	events, err := filestore.NewRunEventRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &terminalEventStore{RunEventRepository: events}
	s.runs, s.events = runs, store
	engine.sessions["session_events"] = domain.Session{ID: "session_events", AgentID: "agent_test"}
	return s, engine, store
}

func TestWaitRunDeliversTerminalAfterCommitBarrierSmoke(t *testing.T) {
	s, _, store := eventSmokeService(t)
	store.entered, store.release = make(chan struct{}), make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(store.release) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := s.StartRun(ctx, domain.RunInput{SessionID: "session_events", UserInput: "合成測試"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("未進入終態寫入")
	}
	result := make(chan error, 1)
	go func() {
		last := ""
		finished, err := s.WaitRun(ctx, run.ID, 0, func(event domain.Event) error { last = event.Type; return nil })
		if err == nil && (last != "run.completed" || finished.Status != domain.RunStatusCompleted) {
			err = errors.New("串流漏送完成事件")
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("事件尚未寫入就提前返回：%v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release.Do(func() { close(store.release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("終態等待卡住")
	}
}

func TestTerminalEventFailureWakesAndRepairsWithoutReplaySmoke(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled} {
		for _, written := range []bool{false, true} {
			t.Run(string(status)+map[bool]string{false: "/unwritten", true: "/sync-uncertain"}[written], func(t *testing.T) {
				s, _, store := eventSmokeService(t)
				store.failOnce, store.writeBeforeError = true, written
				run := domain.Run{ID: "run_terminal", SessionID: "session_events", AgentID: "agent_test", Status: domain.RunStatusRunning}
				if err := s.runs.Save(context.Background(), run); err != nil {
					t.Fatal(err)
				}
				updates, unsubscribe, err := s.SubscribeRunEvents(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer unsubscribe()
				sequence := int64(0)
				if status == domain.RunStatusCanceled {
					if _, err := s.persistImmediateCancellation(run); err != nil {
						t.Fatal(err)
					}
				} else {
					run.Status = status
					s.finishTerminalRun(run, &sequence)
				}
				select {
				case <-updates:
				case <-time.After(time.Second):
					t.Fatal("終態落盤後未喚醒等待者")
				}
				for i := 0; i < 3; i++ {
					current, events, err := s.ReadRunEventBatch(context.Background(), run.ID, 0)
					if err != nil || current.Status != status || len(events) != 1 || events[0].Type != "run."+string(status) {
						t.Fatalf("終態補齊不正確：%+v %v", events, err)
					}
				}
				_, events, err := s.ReadRunEventBatch(context.Background(), run.ID, 1)
				if err != nil || len(events) != 0 {
					t.Fatalf("已收到終態卻重複補寫：%v %v", events, err)
				}
			})
		}
	}
}

func TestCompletedRunEventFailureDoesNotRepeatEngineSmoke(t *testing.T) {
	s, engine, store := eventSmokeService(t)
	store.failOnce = true
	calls := 0
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		calls++
		return domain.RunResult{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := s.ExecuteRun(ctx, domain.RunInput{SessionID: "session_events", UserInput: "合成測試"}, nil)
	if err != nil || run.Status != domain.RunStatusCompleted {
		t.Fatalf("%+v %v", run, err)
	}
	// Close 等執行緒退場後才讀計數，避免測試本身與收尾 goroutine 競爭。
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("引擎被重送 %d 次", calls)
	}
}

func TestRunPanicDoesNotExposePayloadSmoke(t *testing.T) {
	s, engine, _ := eventSmokeService(t)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		panic("synthetic-private-panic-value")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := s.ExecuteRun(ctx, domain.RunInput{SessionID: "session_events", UserInput: "合成測試"}, nil)
	if err != nil || run.Status != domain.RunStatusFailed || run.Error == nil || run.Error.Code != "agent_panic" {
		t.Fatalf("%+v %v", run, err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run.Error.Message, "synthetic-private-panic-value") || strings.Contains(logs.String(), "synthetic-private-panic-value") {
		t.Fatal("panic 值外洩")
	}
}

func TestTerminalRepairRechecksDeletedRunSmoke(t *testing.T) {
	s, _, store := eventSmokeService(t)
	run := domain.Run{ID: "run_deleted", SessionID: "session_events", AgentID: "agent_test", Status: domain.RunStatusCompleted}
	if err := s.runs.Save(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.readRunEventBatch(context.Background(), run.ID, 0, false); !errors.Is(err, errMissingTerminalEvent) {
		t.Fatal("未辨識缺少終態", err)
	}
	// 模擬第一階段讀取後，Session 刪除先取得准入鎖。
	if err := s.DeleteSession(context.Background(), run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReadRunEventBatch(context.Background(), run.ID, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("已刪除 Run 被修復", err)
	}
	if events, err := store.List(context.Background(), run.ID, 0); err != nil || len(events) != 0 {
		t.Fatalf("刪除後重建事件：%v %v", events, err)
	}
}

func TestNonCompletedTerminalClearsCandidateResultSmoke(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunStatusCanceled, domain.RunStatusFailed} {
		s, _, _ := eventSmokeService(t)
		run := domain.Run{ID: "run_candidate", SessionID: "session_events", AgentID: "agent_test", Status: domain.RunStatusRunning}
		if err := s.runs.Save(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		run.Result = &domain.RunResult{Message: domain.Message{Content: "未提交的完成結果"}}
		sequence := int64(0)
		if status == domain.RunStatusCanceled {
			s.finishCanceled(run, context.Canceled, &sequence)
		} else {
			s.finishFailed(run, "synthetic", errors.New("synthetic"), false, &sequence)
		}
		current, err := s.GetRun(context.Background(), run.ID)
		if err != nil || current.Status != status || current.Result != nil {
			t.Fatalf("非完成終態仍帶候選結果：%+v %v", current, err)
		}
	}
}

func TestTerminalStorePanicReleasesLocksSmoke(t *testing.T) {
	s, _, store := eventSmokeService(t)
	store.panicOnce = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := s.ExecuteRun(ctx, domain.RunInput{SessionID: "session_events", UserInput: "合成測試"}, nil)
	if err != nil || run.Status != domain.RunStatusCompleted {
		t.Fatalf("事件 panic 後無法補齊：%+v %v", run, err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal("收尾鎖未釋放", err)
	}
}
