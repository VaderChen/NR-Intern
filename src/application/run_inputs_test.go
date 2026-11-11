package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"errors"
	"testing"
	"time"
)

type runInputFixture struct {
	service *Service
	events  *filestore.RunEventRepository
	started chan *domain.RunInputQueue
	release chan struct{}
	taken   chan []domain.RunInputMessage
}

// newRunInputFixture 建立一個引擎會停在 release 之前的服務；takeBeforeReturn 決定
// 引擎放行後是否像 Harness 一樣在回合邊界取走插入訊息。
func newRunInputFixture(t *testing.T, sessionID string, takeBeforeReturn bool) *runInputFixture {
	t.Helper()
	fixture := &runInputFixture{
		started: make(chan *domain.RunInputQueue, 1),
		release: make(chan struct{}),
		taken:   make(chan []domain.RunInputMessage, 1),
	}
	engine := newFakeEngine()
	engine.sessions[sessionID] = domain.Session{ID: sessionID, AgentID: "agent_test", WorkspaceID: "workspace_1"}
	engine.run = func(ctx context.Context, input domain.RunInput, _ ports.AgentEventSink) (domain.RunResult, error) {
		fixture.started <- input.Inputs
		select {
		case <-fixture.release:
		case <-ctx.Done():
			return domain.RunResult{}, ctx.Err()
		}
		if takeBeforeReturn {
			fixture.taken <- input.Inputs.Take()
		}
		return domain.RunResult{Message: domain.Message{ID: "msg_final", Role: "assistant", Content: "完成"}}, nil
	}
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	dataDir := t.TempDir()
	runs, err := filestore.NewRunRepository(dataDir)
	if err != nil {
		t.Fatalf("NewRunRepository: %v", err)
	}
	events, err := filestore.NewRunEventRepository(dataDir)
	if err != nil {
		t.Fatalf("NewRunEventRepository: %v", err)
	}
	plans, err := filestore.NewPlanRepository(dataDir)
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry: registry, Runs: runs, Events: events, Projects: fakeProjects{}, Workspaces: fakeWorkspaces{},
		Providers: fakeProviders{}, Plans: plans, Permissions: lockedPolicy(), Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	fixture.service = service
	fixture.events = events
	t.Cleanup(func() {
		select {
		case <-fixture.release:
		default:
			close(fixture.release)
		}
		_ = service.Close(context.Background())
	})
	return fixture
}

func (f *runInputFixture) start(t *testing.T, sessionID string) domain.Run {
	t.Helper()
	run, err := f.service.StartRun(context.Background(), domain.RunInput{SessionID: sessionID, UserInput: "開始"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case queue := <-f.started:
		if queue == nil {
			t.Fatal("engine received no input queue")
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	waitForRunStatus(t, f.service, run.ID, domain.RunStatusRunning)
	return run
}

// event 等待指定事件出現：Run 狀態先落盤、終止事件隨後才寫入，兩者之間有極短的空窗。
func (f *runInputFixture) event(t *testing.T, runID, eventType string) (domain.Event, bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		events, err := f.events.List(context.Background(), runID, 0)
		if err != nil {
			t.Fatalf("List events: %v", err)
		}
		for _, event := range events {
			if event.Type == eventType {
				return event, true
			}
		}
		if time.Now().After(deadline) {
			return domain.Event{}, false
		}
		time.Sleep(time.Millisecond)
	}
}

func returnedInputIDs(t *testing.T, event domain.Event) []string {
	t.Helper()
	raw, _ := event.Payload["returned_inputs"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// 執行中送出的訊息交給引擎，並留下 run.input_queued 事件；完成後不再有退回。
func TestAppendRunInputReachesEngine(t *testing.T) {
	const sessionID = "session_input_reach"
	fixture := newRunInputFixture(t, sessionID, true)
	run := fixture.start(t, sessionID)

	message, err := fixture.service.AppendRunInput(context.Background(), run.ID, "  補充 用表格  ", "client_1")
	if err != nil {
		t.Fatalf("AppendRunInput: %v", err)
	}
	if message.ID == "" || message.Content != "補充 用表格" || message.ClientID != "client_1" {
		t.Fatalf("message = %#v", message)
	}
	if _, err := fixture.service.AppendRunInput(context.Background(), run.ID, "   ", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("blank input error = %v, want ErrInvalidInput", err)
	}
	close(fixture.release)
	select {
	case taken := <-fixture.taken:
		if len(taken) != 1 || taken[0].ID != message.ID {
			t.Fatalf("engine took %#v", taken)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not take inputs")
	}
	waitForRunStatus(t, fixture.service, run.ID, domain.RunStatusCompleted)
	if _, ok := fixture.event(t, run.ID, "run.input_queued"); !ok {
		t.Fatal("run.input_queued event missing")
	}
	completed, ok := fixture.event(t, run.ID, "run.completed")
	if !ok {
		t.Fatal("run.completed event missing")
	}
	if ids := returnedInputIDs(t, completed); len(ids) != 0 {
		t.Fatalf("consumed input was returned: %v", ids)
	}
}

// 引擎沒有取用的訊息在完成事件中退回；Run 結束後再送一律是 ErrConflict（HTTP 409）。
func TestAppendRunInputReturnedWhenUnconsumed(t *testing.T) {
	const sessionID = "session_input_returned"
	fixture := newRunInputFixture(t, sessionID, false)
	run := fixture.start(t, sessionID)

	first, err := fixture.service.AppendRunInput(context.Background(), run.ID, "第一則", "")
	if err != nil {
		t.Fatalf("AppendRunInput first: %v", err)
	}
	second, err := fixture.service.AppendRunInput(context.Background(), run.ID, "第二則", "")
	if err != nil {
		t.Fatalf("AppendRunInput second: %v", err)
	}
	close(fixture.release)
	waitForRunStatus(t, fixture.service, run.ID, domain.RunStatusCompleted)

	completed, ok := fixture.event(t, run.ID, "run.completed")
	if !ok {
		t.Fatal("run.completed event missing")
	}
	ids := returnedInputIDs(t, completed)
	if len(ids) != 2 || ids[0] != first.ID || ids[1] != second.ID {
		t.Fatalf("returned_inputs = %v, want [%s %s]", ids, first.ID, second.ID)
	}
	if _, err := fixture.service.AppendRunInput(context.Background(), run.ID, "太晚了", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("append after completion error = %v, want ErrConflict", err)
	}
	if _, err := fixture.service.AppendRunInput(context.Background(), "run_missing", "x", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("append to missing run error = %v, want ErrNotFound", err)
	}
}

// 停止 Run 時，尚未插入的訊息隨 run.canceled 退回，之後不再接受。
func TestAppendRunInputReturnedOnCancel(t *testing.T) {
	const sessionID = "session_input_cancel"
	fixture := newRunInputFixture(t, sessionID, false)
	run := fixture.start(t, sessionID)

	message, err := fixture.service.AppendRunInput(context.Background(), run.ID, "還沒用到", "")
	if err != nil {
		t.Fatalf("AppendRunInput: %v", err)
	}
	if _, err := fixture.service.CancelRun(context.Background(), run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	waitForRunStatus(t, fixture.service, run.ID, domain.RunStatusCanceled)
	canceled, ok := fixture.event(t, run.ID, "run.canceled")
	if !ok {
		t.Fatal("run.canceled event missing")
	}
	ids := returnedInputIDs(t, canceled)
	if len(ids) != 1 || ids[0] != message.ID {
		t.Fatalf("returned_inputs = %v, want [%s]", ids, message.ID)
	}
	if _, err := fixture.service.AppendRunInput(context.Background(), run.ID, "太晚了", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("append after cancel error = %v, want ErrConflict", err)
	}
}

// 待插入訊息有數量上限，超過時回 ErrConflict，由前端改走一般佇列。
func TestAppendRunInputPendingLimit(t *testing.T) {
	const sessionID = "session_input_limit"
	fixture := newRunInputFixture(t, sessionID, false)
	run := fixture.start(t, sessionID)
	for index := 0; index < maxPendingRunInputs; index++ {
		if _, err := fixture.service.AppendRunInput(context.Background(), run.ID, "訊息", ""); err != nil {
			t.Fatalf("AppendRunInput %d: %v", index, err)
		}
	}
	if _, err := fixture.service.AppendRunInput(context.Background(), run.ID, "超過", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("over-limit error = %v, want ErrConflict", err)
	}
}
