package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeEngine struct {
	mu       sync.Mutex
	sessions map[string]domain.Session
	counter  int
	run      func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error)
	// retractedMessageID 記下最後一次「重新提問」撤回的起點，供測試檢查。
	retractedMessageID string
}

func (e *fakeEngine) SetPermanentToolApproval(_ context.Context, sessionID string, enabled bool) (domain.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, exists := e.sessions[sessionID]
	if !exists {
		return domain.Session{}, domain.ErrNotFound
	}
	session.PermanentToolApproval = enabled
	e.sessions[sessionID] = session
	return session, nil
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{sessions: map[string]domain.Session{}}
}

func (e *fakeEngine) Descriptor() domain.AgentDescriptor {
	return domain.AgentDescriptor{ID: "agent_test", Name: "test"}
}

func (e *fakeEngine) CreateSession(_ context.Context, input domain.CreateSessionInput) (domain.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counter++
	session := domain.Session{
		ID:                fmt.Sprintf("session_%d", e.counter),
		AgentID:           "agent_test",
		WorkspaceID:       input.WorkspaceID,
		ProjectID:         input.ProjectID,
		ProviderID:        input.ProviderID,
		Model:             input.Model,
		ThinkingMode:      input.ThinkingMode,
		LockPlans:         input.LockPlans,
		PermissionProfile: input.PermissionProfile,
	}
	e.sessions[session.ID] = session
	return session, nil
}

func (e *fakeEngine) ListSessions(context.Context) ([]domain.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	values := make([]domain.Session, 0, len(e.sessions))
	for _, session := range e.sessions {
		values = append(values, session)
	}
	return values, nil
}

func (e *fakeEngine) GetSession(_ context.Context, sessionID string) (domain.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, exists := e.sessions[sessionID]
	if !exists {
		return domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
	}
	return session, nil
}

func (e *fakeEngine) UpdateSession(_ context.Context, sessionID string, input domain.UpdateSessionInput) (domain.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.sessions[sessionID]
	if input.PermissionProfile != nil {
		session.PermissionProfile = *input.PermissionProfile
	}
	if input.Position != nil {
		session.Position = *input.Position
	}
	if input.ProviderID != nil {
		session.ProviderID = *input.ProviderID
	}
	if input.Model != nil {
		session.Model = *input.Model
	}
	if input.ThinkingMode != nil {
		session.ThinkingMode = *input.ThinkingMode
	}
	if input.LockPlans != nil {
		session.LockPlans = *input.LockPlans
	}
	e.sessions[sessionID] = session
	return session, nil
}

func (e *fakeEngine) DeleteSession(context.Context, string) error { return nil }

func (e *fakeEngine) ListMessages(context.Context, string) ([]domain.Message, error) { return nil, nil }

func (e *fakeEngine) ListEntries(context.Context, string) ([]domain.SessionEntry, error) {
	return nil, nil
}

func (e *fakeEngine) RetractMessages(_ context.Context, _, messageID string) ([]domain.Message, error) {
	e.retractedMessageID = messageID
	return nil, nil
}

func (e *fakeEngine) Run(ctx context.Context, input domain.RunInput, sink ports.AgentEventSink) (domain.RunResult, error) {
	if e.run != nil {
		return e.run(ctx, input, sink)
	}
	return domain.RunResult{}, nil
}

type fakeRuns struct{}

func (fakeRuns) Save(context.Context, domain.Run) error { return nil }
func (fakeRuns) Get(_ context.Context, id string) (domain.Run, error) {
	return domain.Run{}, fmt.Errorf("%w: run %q", domain.ErrNotFound, id)
}
func (fakeRuns) List(context.Context, string) ([]domain.Run, error) { return nil, nil }
func (fakeRuns) FindByIdempotencyKey(context.Context, string, string) (domain.Run, error) {
	return domain.Run{}, domain.ErrNotFound
}

type fakeEvents struct{}

func (fakeEvents) Append(context.Context, domain.Event) error { return nil }
func (fakeEvents) List(context.Context, string, int64) ([]domain.Event, error) {
	return nil, nil
}

type fakeProjects struct{}

func (fakeProjects) Create(context.Context, domain.CreateProjectInput) (domain.Project, error) {
	return domain.Project{}, nil
}
func (fakeProjects) List(context.Context) ([]domain.Project, error) { return nil, nil }
func (fakeProjects) Get(_ context.Context, id string) (domain.Project, error) {
	return domain.Project{}, fmt.Errorf("%w: project %q", domain.ErrNotFound, id)
}
func (fakeProjects) Update(context.Context, string, domain.UpdateProjectInput) (domain.Project, error) {
	return domain.Project{}, nil
}
func (fakeProjects) Delete(context.Context, string) error { return nil }

type fakeWorkspaces struct{}

func (fakeWorkspaces) Create(context.Context, domain.CreateWorkspaceInput) (domain.Workspace, error) {
	return domain.Workspace{}, nil
}
func (fakeWorkspaces) List(context.Context) ([]domain.Workspace, error) {
	return []domain.Workspace{{ID: "workspace_1", ProviderIDs: []string{"openai-compatible"}, DefaultProviderID: "openai-compatible", Model: "workspace-model"}}, nil
}
func (fakeWorkspaces) Get(_ context.Context, id string) (domain.Workspace, error) {
	if id != "workspace_1" {
		return domain.Workspace{}, fmt.Errorf("%w: workspace %q", domain.ErrNotFound, id)
	}
	return domain.Workspace{ID: "workspace_1", ProviderIDs: []string{"openai-compatible"}, DefaultProviderID: "openai-compatible", Model: "workspace-model"}, nil
}
func (fakeWorkspaces) Update(context.Context, string, domain.UpdateWorkspaceInput) (domain.Workspace, error) {
	return domain.Workspace{}, nil
}
func (fakeWorkspaces) Delete(context.Context, string) error { return nil }

type fakeProviders struct{}

func (fakeProviders) Stream(context.Context, domain.ModelRequest, ports.ModelEventSink) (domain.ModelResponse, error) {
	return domain.ModelResponse{}, nil
}
func (fakeProviders) Capabilities(string, string) domain.ModelCapabilities {
	return domain.ModelCapabilities{}
}
func (fakeProviders) DefaultProviderID() string { return "openai-compatible" }
func (fakeProviders) HasProvider(id string) bool {
	return id == "openai-compatible" || id == "secondary"
}
func (fakeProviders) ListProviders() []domain.ProviderDescriptor {
	return []domain.ProviderDescriptor{
		{ID: "openai-compatible", DefaultModel: "provider-model"},
		{ID: "secondary", DefaultModel: "secondary-model"},
	}
}

func newTestService(t *testing.T, policy domain.PermissionPolicy) (*Service, *fakeEngine) {
	t.Helper()
	engine := newFakeEngine()
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	plans, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry:    registry,
		Runs:        fakeRuns{},
		Events:      fakeEvents{},
		Projects:    fakeProjects{},
		Workspaces:  fakeWorkspaces{},
		Providers:   fakeProviders{},
		Plans:       plans,
		Permissions: policy,
		Logger:      logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return service, engine
}

func lockedPolicy() domain.PermissionPolicy {
	return domain.PermissionPolicy{DefaultProfile: "default", ElevatedProfiles: []string{"trusted"}}
}

func TestClassifyRunFailure(t *testing.T) {
	tests := []struct {
		name      string
		cause     error
		code      string
		retryable bool
	}{
		{name: "provider protocol", cause: fmt.Errorf("%w: missing tool_calls", domain.ErrProviderProtocol), code: "provider_protocol_incompatible"},
		{name: "invalid input", cause: fmt.Errorf("%w: malformed request", domain.ErrInvalidInput), code: "invalid_input"},
		{name: "transient agent failure", cause: errors.New("provider unavailable"), code: "agent_run_failed", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, retryable := classifyRunFailure(test.cause)
			if code != test.code || retryable != test.retryable {
				t.Fatalf("classifyRunFailure() = (%q, %t), want (%q, %t)", code, retryable, test.code, test.retryable)
			}
		})
	}
}

// TestCreateSessionRejectsClientRequestedElevation 是這次修正的核心回歸測試：
// 在此之前，permission_profile 直接從 request body 傳到 Session，
// 而高權限工具的閘門就是查這個欄位。
func TestCreateSessionRejectsClientRequestedElevation(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	_, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{
		WorkspaceID:       "workspace_1",
		PermissionProfile: "trusted",
	})

	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateSessionAssignsBackendDefaultProfile(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.PermissionProfile != "default" {
		t.Fatalf("profile = %q, want default", session.PermissionProfile)
	}
}

func TestCreateSessionCopiesWorkspaceProviderAndModelDefaults(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ProviderID != "openai-compatible" || session.Model != "workspace-model" {
		t.Fatalf("provider/model = %q/%q, want openai-compatible/workspace-model", session.ProviderID, session.Model)
	}
}

func TestCreateSessionKeepsExplicitProviderAndModel(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{
		WorkspaceID: "workspace_1",
		ProviderID:  "openai-compatible",
		Model:       "explicit-model",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ProviderID != "openai-compatible" || session.Model != "explicit-model" {
		t.Fatalf("provider/model = %q/%q, want openai-compatible/explicit-model", session.ProviderID, session.Model)
	}
}

func TestCreateSessionAllowsEnabledProviderOutsideWorkspaceDefaults(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{
		WorkspaceID: "workspace_1",
		ProviderID:  "secondary",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ProviderID != "secondary" || session.Model != "secondary-model" {
		t.Fatalf("provider/model = %q/%q, want secondary/secondary-model", session.ProviderID, session.Model)
	}
}

func TestUpdateSessionAllowsEnabledProviderOutsideWorkspaceDefaults(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	providerID := "secondary"
	updated, err := service.UpdateSession(context.Background(), session.ID, domain.UpdateSessionInput{ProviderID: &providerID})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.ProviderID != "secondary" {
		t.Fatalf("provider = %q, want secondary", updated.ProviderID)
	}
}

func TestStartRunAllowsEnabledProviderOverrideOutsideWorkspaceDefaults(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	run, err := service.StartRun(context.Background(), domain.RunInput{
		SessionID:  session.ID,
		UserInput:  "使用另一個 Provider",
		ProviderID: "secondary",
		Model:      "secondary-model",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.ProviderID != "secondary" || run.Model != "secondary-model" {
		t.Fatalf("provider/model = %q/%q, want secondary/secondary-model", run.ProviderID, run.Model)
	}
}

func TestCreateSessionHonoursProfileWhenClientChoiceEnabled(t *testing.T) {
	policy := lockedPolicy()
	policy.AllowClientChoice = true
	service, _ := newTestService(t, policy)

	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{
		WorkspaceID:       "workspace_1",
		PermissionProfile: "trusted",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.PermissionProfile != "trusted" {
		t.Fatalf("profile = %q, want trusted", session.PermissionProfile)
	}
}

// TestUpdateSessionRejectsClientRequestedElevation 補上另一半：
// 建立時擋住但更新時放行，等於沒有擋。
func TestUpdateSessionRejectsClientRequestedElevation(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	profile := "trusted"
	if _, err := service.UpdateSession(context.Background(), session.ID, domain.UpdateSessionInput{PermissionProfile: &profile}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateSessionRejectsUnknownWorkspace(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())

	if _, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUserCanCreateAndReadSessionPlan(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	created, err := service.PutPlan(context.Background(), session.ID, domain.CreatePlanInput{
		Title: "使用者計畫",
		Steps: []domain.CreatePlanStepInput{{Title: "完成實作", Verification: "語法檢查通過"}},
	})
	if err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	if created.CreatedBy != domain.PlanCreatedByUser {
		t.Fatalf("created_by = %q", created.CreatedBy)
	}
	loaded, err := service.GetPlan(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if loaded == nil || loaded.ID != created.ID {
		t.Fatalf("loaded plan = %#v", loaded)
	}
}

func TestUserCanCreateAndReorderMultipleSessionPlans(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	session, err := service.CreateSession(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1", LockPlans: true})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first, err := service.CreatePlan(context.Background(), session.ID, domain.CreatePlanInput{
		Title: "第一份", Steps: []domain.CreatePlanStepInput{{Title: "第一步", Verification: "檢查一"}},
	})
	if err != nil {
		t.Fatalf("CreatePlan first: %v", err)
	}
	second, err := service.CreatePlan(context.Background(), session.ID, domain.CreatePlanInput{
		Title: "第二份", Steps: []domain.CreatePlanStepInput{{Title: "第二步", Verification: "檢查二"}},
	})
	if err != nil {
		t.Fatalf("CreatePlan second: %v", err)
	}
	if first.Status != domain.PlanStatusActive || second.Status != domain.PlanStatusQueued {
		t.Fatalf("statuses = %s, %s", first.Status, second.Status)
	}
	reordered, err := service.ReorderPlans(context.Background(), session.ID, domain.ReorderPlansInput{PlanIDs: []string{second.ID, first.ID}})
	if err != nil {
		t.Fatalf("ReorderPlans: %v", err)
	}
	if len(reordered) != 2 || reordered[0].ID != second.ID || reordered[0].Status != domain.PlanStatusActive || reordered[1].Status != domain.PlanStatusQueued {
		t.Fatalf("reordered plans = %#v", reordered)
	}
}

// TestApprovalWaitingReleasesPhysicalGateButKeepsSessionOrder 驗證等待人工決策時不持有
// session gate，同時仍以邏輯預約阻止後續 Run 插入尚未完成的 tool-call 協定區段。
func TestApprovalWaitingReleasesPhysicalGateButKeepsSessionOrder(t *testing.T) {
	coordinator := approval.NewCoordinator(nil)
	engine := newFakeEngine()
	engine.sessions["session_approval"] = domain.Session{ID: "session_approval", AgentID: "agent_test", WorkspaceID: "workspace_1"}
	started := make(chan string, 2)
	engine.run = func(ctx context.Context, input domain.RunInput, sink ports.AgentEventSink) (domain.RunResult, error) {
		started <- input.UserInput
		if input.UserInput != "needs approval" {
			return domain.RunResult{Message: domain.Message{ID: "msg_second", Role: "assistant", Content: "second done"}}, nil
		}
		request := domain.ToolApprovalRequest{
			ID:          "approval_service",
			RunID:       input.RunID,
			SessionID:   input.SessionID,
			ToolCallID:  "call_service",
			ToolName:    "shell_exec",
			RequestedAt: time.Now().UTC(),
		}
		if err := coordinator.Begin(request); err != nil {
			return domain.RunResult{}, err
		}
		if err := sink(domain.EngineEvent{Type: "run.approval_required", Payload: map[string]any{"approval": request}}); err != nil {
			return domain.RunResult{}, err
		}
		decision, err := coordinator.Wait(ctx, request.ID)
		if err != nil {
			return domain.RunResult{}, err
		}
		if err := sink(domain.EngineEvent{Type: "run.approval_resolved", Payload: map[string]any{"approval": request, "decision": decision}}); err != nil {
			return domain.RunResult{}, err
		}
		return domain.RunResult{Message: domain.Message{ID: "msg_first", Role: "assistant", Content: "first done"}}, nil
	}
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	runs, err := filestore.NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewRunRepository: %v", err)
	}
	events, err := filestore.NewRunEventRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewRunEventRepository: %v", err)
	}
	plans, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry: registry, Runs: runs, Events: events, Projects: fakeProjects{}, Workspaces: fakeWorkspaces{},
		Providers: fakeProviders{}, Approvals: coordinator, Plans: plans, Permissions: lockedPolicy(), Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	first, err := service.StartRun(context.Background(), domain.RunInput{SessionID: "session_approval", UserInput: "needs approval"})
	if err != nil {
		t.Fatalf("StartRun(first): %v", err)
	}
	if got := <-started; got != "needs approval" {
		t.Fatalf("first started = %q", got)
	}
	waitForRunStatus(t, service, first.ID, domain.RunStatusWaitingApproval)
	second, err := service.StartRun(context.Background(), domain.RunInput{SessionID: "session_approval", UserInput: "second"})
	if err != nil {
		t.Fatalf("StartRun(second): %v", err)
	}
	select {
	case got := <-started:
		t.Fatalf("queued run %q entered the paused session", got)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := service.DecideRun(context.Background(), first.ID, domain.ToolApprovalDecisionInput{
		ApprovalID: "approval_service",
		Decision:   domain.ToolApprovalApprove,
		Permanent:  true,
	}); err != nil {
		t.Fatalf("DecideRun: %v", err)
	}
	approvedSession, err := service.GetSession(context.Background(), "session_approval")
	if err != nil || !approvedSession.PermanentToolApproval {
		t.Fatalf("permanent session approval was not persisted: %+v, err = %v", approvedSession, err)
	}
	completed, err := service.WaitRun(context.Background(), first.ID, 0, nil)
	if err != nil || completed.Status != domain.RunStatusCompleted {
		t.Fatalf("first completion = %+v, err = %v", completed, err)
	}
	select {
	case got := <-started:
		if got != "second" {
			t.Fatalf("next run = %q, want second", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second run did not start after approval completed")
	}
	if _, err := service.WaitRun(context.Background(), second.ID, 0, nil); err != nil {
		t.Fatalf("WaitRun(second): %v", err)
	}
}

func waitForRunStatus(t *testing.T, service *Service, runID string, wanted domain.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		run, err := service.GetRun(context.Background(), runID)
		if err == nil && run.Status == wanted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	run, _ := service.GetRun(context.Background(), runID)
	t.Fatalf("run status = %q, want %q", run.Status, wanted)
}

// TestCancelRunPersistsTerminalStateBeforeEngineReturns 覆蓋不遵守 context
// 的引擎：停止 API 必須先讓 UI 看見終止，不能把第三方實作的收尾速度當成前提。
func TestCancelRunPersistsTerminalStateBeforeEngineReturns(t *testing.T) {
	engine := newFakeEngine()
	const sessionID = "session_cancel_immediate"
	engine.sessions[sessionID] = domain.Session{ID: sessionID, AgentID: "agent_test", WorkspaceID: "workspace_1"}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	engine.run = func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		close(started)
		<-release
		return domain.RunResult{}, nil
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
	stopEngine := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		stopEngine()
		_ = service.Close(context.Background())
	})

	run, err := service.StartRun(context.Background(), domain.RunInput{SessionID: sessionID, UserInput: "stop me"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	waitForRunStatus(t, service, run.ID, domain.RunStatusRunning)

	canceled, err := service.CancelRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if canceled.Status != domain.RunStatusCanceled {
		t.Fatalf("CancelRun status = %q, want canceled", canceled.Status)
	}
	persisted, err := service.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun after cancel: %v", err)
	}
	if persisted.Status != domain.RunStatusCanceled {
		t.Fatalf("persisted status = %q, want canceled", persisted.Status)
	}

	stopEngine()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, getErr := service.GetRun(context.Background(), run.ID)
		if getErr == nil && current.Status != domain.RunStatusCanceled {
			t.Fatalf("status after ignored cancellation returned = %q, want canceled", current.Status)
		}
		service.mu.Lock()
		active := len(service.active)
		service.mu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	values, err := events.List(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(values) == 0 || values[len(values)-1].Type != "run.canceled" {
		t.Fatalf("last event = %+v, want run.canceled", values)
	}
}

func TestRetryRunCreatesNewRunFromOriginalInput(t *testing.T) {
	engine := newFakeEngine()
	engine.sessions["session_retry"] = domain.Session{ID: "session_retry", AgentID: "agent_test", WorkspaceID: "workspace_1"}
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
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	completedAt := time.Now().UTC()
	original := domain.Run{
		ID: "run_original", AgentID: "agent_test", SessionID: "session_retry",
		Status: domain.RunStatusFailed, Input: "重新執行原始工作", ProviderID: "openai-compatible", Model: "model-a",
		Metadata:  map[string]any{"trace": "keep", "termination": "old"},
		Error:     &domain.RunError{Code: "server_restarted", Message: "restart", Retryable: true},
		CreatedAt: completedAt.Add(-time.Minute), CompletedAt: &completedAt,
	}
	if err := runs.Save(context.Background(), original); err != nil {
		t.Fatalf("Save(original): %v", err)
	}

	retried, err := service.RetryRun(context.Background(), original.ID, "retry-idempotency")
	if err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	if retried.ID == original.ID || retried.SessionID != original.SessionID || retried.Input != original.Input {
		t.Fatalf("retried = %+v", retried)
	}
	if retried.ProviderID != original.ProviderID || retried.Model != original.Model {
		t.Fatalf("provider/model were not retained: %+v", retried)
	}
	if retried.Metadata["retry_of"] != original.ID || retried.Metadata["trace"] != "keep" {
		t.Fatalf("retry metadata = %+v", retried.Metadata)
	}
	if _, exists := retried.Metadata["termination"]; exists {
		t.Fatalf("terminal metadata leaked into retry: %+v", retried.Metadata)
	}
}
