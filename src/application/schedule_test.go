package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newScheduleTestService(t *testing.T) (*Service, *fakeEngine, ports.ScheduleRepository) {
	t.Helper()
	engine := newFakeEngine()
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	dataDir := t.TempDir()
	plans, err := filestore.NewPlanRepository(dataDir)
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	schedules, err := filestore.NewScheduleRepository(dataDir)
	if err != nil {
		t.Fatalf("NewScheduleRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry:    registry,
		Runs:        fakeRuns{},
		Events:      fakeEvents{},
		Projects:    fakeProjects{},
		Workspaces:  fakeWorkspaces{},
		Providers:   fakeProviders{},
		Plans:       plans,
		Schedules:   schedules,
		Permissions: lockedPolicy(),
		Logger:      logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return service, engine, schedules
}

func TestCreateScheduleRequiresKnownWorkspace(t *testing.T) {
	service, _, _ := newScheduleTestService(t)
	_, err := service.CreateSchedule(context.Background(), domain.CreateScheduleInput{
		WorkspaceID: "workspace_missing",
		Name:        "每日巡檢",
		Prompt:      "檢查昨天的執行結果",
		Recurrence:  domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "09:00"},
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

// 排程到期時要建立新的 Session，而不是沿用任何既有對話。
func TestRunDueSchedulesStartsRunInNewSession(t *testing.T) {
	service, engine, schedules := newScheduleTestService(t)
	sandboxRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	observed := make(chan domain.RunInput, 1)
	engine.run = func(_ context.Context, input domain.RunInput, _ ports.AgentEventSink) (domain.RunResult, error) {
		select {
		case observed <- input:
		default:
		}
		return domain.RunResult{}, nil
	}
	schedule, err := service.CreateSchedule(context.Background(), domain.CreateScheduleInput{
		WorkspaceID:  "workspace_1",
		Name:         "每小時巡檢",
		Prompt:       "檢查昨天的執行結果",
		Recurrence:   domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 60},
		SandboxRoots: []string{sandboxRoot},
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if schedule.NextRunAt == nil {
		t.Fatal("enabled schedule must carry next_run_at")
	}
	due := time.Now().UTC().Add(-time.Minute)
	if _, err := schedules.Reschedule(context.Background(), schedule.ID, &due); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	service.runDueSchedules(context.Background())

	var input domain.RunInput
	select {
	case input = <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled run did not start")
	}
	if input.UserInput != "檢查昨天的執行結果" {
		t.Fatalf("run input = %q", input.UserInput)
	}
	roots, _ := input.Metadata["sandbox_roots"].([]string)
	if len(roots) == 0 || roots[0] != sandboxRoot {
		t.Fatalf("sandbox roots = %v, want first entry %q", input.Metadata["sandbox_roots"], sandboxRoot)
	}
	session, err := engine.GetSession(context.Background(), input.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.ProjectID != "" {
		t.Fatalf("scheduled session must stay outside projects, got %q", session.ProjectID)
	}

	stored, err := schedules.Get(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.LastRunID == "" || stored.LastSessionID != input.SessionID {
		t.Fatalf("last run not recorded: %+v", stored)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("next_run_at was not advanced: %+v", stored.NextRunAt)
	}
}

func TestRunDueSchedulesSkipsDisabledSchedule(t *testing.T) {
	service, engine, schedules := newScheduleTestService(t)
	started := make(chan struct{}, 1)
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		started <- struct{}{}
		return domain.RunResult{}, nil
	}
	disabled := false
	schedule, err := service.CreateSchedule(context.Background(), domain.CreateScheduleInput{
		WorkspaceID: "workspace_1",
		Name:        "停用的排程",
		Prompt:      "不應該執行",
		Enabled:     &disabled,
		Recurrence:  domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 5},
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if schedule.NextRunAt != nil {
		t.Fatalf("disabled schedule must not carry next_run_at: %v", schedule.NextRunAt)
	}
	due := time.Now().UTC().Add(-time.Minute)
	if _, err := schedules.Reschedule(context.Background(), schedule.ID, &due); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	service.runDueSchedules(context.Background())

	select {
	case <-started:
		t.Fatal("disabled schedule must not start a run")
	case <-time.After(200 * time.Millisecond):
	}
}

// 停機期間錯過太久的時間點只會往後對齊，不會在啟動時湧出一批補償 Run。
func TestRebaselineSchedulesSkipsLongMissedOccurrences(t *testing.T) {
	service, engine, schedules := newScheduleTestService(t)
	started := make(chan struct{}, 1)
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		started <- struct{}{}
		return domain.RunResult{}, nil
	}
	schedule, err := service.CreateSchedule(context.Background(), domain.CreateScheduleInput{
		WorkspaceID: "workspace_1",
		Name:        "每日巡檢",
		Prompt:      "檢查昨天的執行結果",
		Recurrence:  domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "09:00"},
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	missed := time.Now().UTC().Add(-72 * time.Hour)
	if _, err := schedules.Reschedule(context.Background(), schedule.ID, &missed); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	service.rebaselineSchedules(context.Background())

	stored, err := schedules.Get(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("next_run_at was not rebaselined: %v", stored.NextRunAt)
	}
	if stored.LastRunID != "" {
		t.Fatalf("rebaseline must not start a run: %+v", stored)
	}
	select {
	case <-started:
		t.Fatal("rebaseline must not start a run")
	case <-time.After(200 * time.Millisecond):
	}
}

// 手動執行只補上最近一次結果，不會把週期性的時間軸往前推。
func TestRunScheduleKeepsNextRunAt(t *testing.T) {
	service, engine, schedules := newScheduleTestService(t)
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		return domain.RunResult{}, nil
	}
	schedule, err := service.CreateSchedule(context.Background(), domain.CreateScheduleInput{
		WorkspaceID: "workspace_1",
		Name:        "每日巡檢",
		Prompt:      "檢查昨天的執行結果",
		Recurrence:  domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyDaily, TimeOfDay: "09:00"},
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if _, err := service.RunSchedule(context.Background(), schedule.ID); err != nil {
		t.Fatalf("RunSchedule: %v", err)
	}
	stored, err := schedules.Get(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.Equal(*schedule.NextRunAt) {
		t.Fatalf("next_run_at changed: %v -> %v", schedule.NextRunAt, stored.NextRunAt)
	}
	if stored.LastRunID == "" || stored.LastStatus != domain.ScheduleStatusTriggered {
		t.Fatalf("manual run not recorded: %+v", stored)
	}
}

func TestScheduleAPIsRequireScheduleStore(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	if _, err := service.ListSchedules(context.Background()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}
