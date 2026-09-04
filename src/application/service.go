package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/internal/textutil"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type EventSink func(domain.Event) error

type activeRun struct {
	sessionID       string
	cancel          context.CancelFunc
	cancelRequested bool
}

type Service struct {
	registry             *Registry
	runs                 ports.RunRepository
	events               ports.RunEventRepository
	projects             ports.ProjectRepository
	ephemeralProjects    ports.EphemeralProjectWorkspace
	workspaces           ports.WorkspaceRepository
	providers            ports.ProviderCatalog
	approvals            ports.ApprovalCoordinator
	questions            ports.QuestionCoordinator
	memories             ports.MemoryRepository
	plans                ports.PlanRepository
	attachments          ports.AttachmentRepository
	schedules            ports.ScheduleRepository
	notifications        ports.NotificationRepository
	notificationsEnabled atomic.Bool
	// memoryIsolatedProjects 只擋新建。既有隔離專案仍照常運作，
	// 否則一次誤關就會讓使用者現有的專案突然開不起來。
	memoryIsolatedProjects atomic.Bool
	modelPrices            map[string]map[string]domain.ModelPrice
	// permissions 是後端唯一的 permission profile 依據。
	// 呼叫端要求的 profile 必須先經過這裡解析，才不會讓 API request 自行決定提權。
	permissions domain.PermissionPolicy
	logger      *slog.Logger
	now         func() time.Time
	rootCtx     context.Context
	stop        context.CancelFunc

	startMu        sync.Mutex
	mu             sync.Mutex
	active         map[string]activeRun
	sessionGates   map[string]chan struct{}
	pausedSessions map[string]string
	sessionSignals map[string]chan struct{}
	pausedRuns     map[string]bool
	runSignals     map[string]chan struct{}
	watchers       map[string]map[uint64]chan struct{}
	nextWatcher    uint64
	eventMu        sync.Mutex
	eventSequences map[string]int64
	closed         bool
	wg             sync.WaitGroup
}

// Dependencies 取代一長串位置參數：這些欄位多半是介面，順序寫錯不會被型別系統擋下。
type Dependencies struct {
	Registry *Registry
	Runs     ports.RunRepository
	Events   ports.RunEventRepository
	Projects ports.ProjectRepository
	// EphemeralProjects 可以是 nil；只有建立或執行記憶體隔離專案時才要求此能力。
	EphemeralProjects ports.EphemeralProjectWorkspace
	Workspaces        ports.WorkspaceRepository
	Providers         ports.ProviderCatalog
	Approvals         ports.ApprovalCoordinator
	Questions         ports.QuestionCoordinator
	// Memories 可以是 nil：後端停用長期記憶時，記憶 API 會回報 conflict 而不是假裝成功。
	Memories ports.MemoryRepository
	// Plans 是 Session 計畫的持久化來源，也是 Agent 與使用者介面共用的唯一真實狀態。
	Plans ports.PlanRepository
	// Attachments 可以是 nil；只有建立含附件的 Run 時才要求此能力。
	Attachments ports.AttachmentRepository
	// Schedules 可以是 nil：沒有排程儲存時，排程 API 會回報 conflict，
	// 也不會啟動背景排程執行器。
	Schedules ports.ScheduleRepository
	// Notifications 可以是 nil，讓精簡測試與嵌入式使用者不必啟用通知儲存。
	Notifications          ports.NotificationRepository
	NotificationsEnabled   bool
	MemoryIsolatedProjects bool
	ModelPrices            map[string]map[string]domain.ModelPrice
	Permissions            domain.PermissionPolicy
	Logger                 *slog.Logger
}

func NewService(dependencies Dependencies) (*Service, error) {
	registry, runs, events := dependencies.Registry, dependencies.Runs, dependencies.Events
	projects, workspaces, providers := dependencies.Projects, dependencies.Workspaces, dependencies.Providers
	if registry == nil || runs == nil || events == nil || projects == nil || workspaces == nil || providers == nil || dependencies.Plans == nil {
		return nil, fmt.Errorf("%w: registry, run, event, project, workspace, provider and plan dependencies are required", domain.ErrInvalidInput)
	}
	rootCtx, stop := context.WithCancel(context.Background())
	service := &Service{
		registry:          registry,
		runs:              runs,
		events:            events,
		projects:          projects,
		ephemeralProjects: dependencies.EphemeralProjects,
		workspaces:        workspaces,
		providers:         providers,
		approvals:         dependencies.Approvals,
		questions:         dependencies.Questions,
		memories:          dependencies.Memories,
		plans:             dependencies.Plans,
		attachments:       dependencies.Attachments,
		schedules:         dependencies.Schedules,
		notifications:     dependencies.Notifications,
		modelPrices:       cloneModelPrices(dependencies.ModelPrices),
		permissions:       dependencies.Permissions.Normalize(),
		logger:            logging.Or(dependencies.Logger),
		now:               time.Now,
		rootCtx:           rootCtx,
		stop:              stop,
		active:            map[string]activeRun{},
		sessionGates:      map[string]chan struct{}{},
		pausedSessions:    map[string]string{},
		sessionSignals:    map[string]chan struct{}{},
		pausedRuns:        map[string]bool{},
		runSignals:        map[string]chan struct{}{},
		watchers:          map[string]map[uint64]chan struct{}{},
		eventSequences:    map[string]int64{},
	}
	service.notificationsEnabled.Store(dependencies.NotificationsEnabled)
	service.memoryIsolatedProjects.Store(dependencies.MemoryIsolatedProjects)
	if err := service.reconcileTerminalEvents(context.Background()); err != nil {
		stop()
		return nil, err
	}
	service.startScheduleRunner()
	return service, nil
}

func (s *Service) ListAgents() []domain.AgentDescriptor {
	return s.registry.List()
}

func (s *Service) GetAgent(id string) (domain.AgentDescriptor, error) {
	engine, err := s.registry.Get(id)
	if err != nil {
		return domain.AgentDescriptor{}, err
	}
	return engine.Descriptor(), nil
}

func (s *Service) CreateSession(ctx context.Context, agentID string, input domain.CreateSessionInput) (domain.Session, error) {
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return domain.Session{}, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	thinkingMode, err := domain.NormalizeThinkingMode(input.ThinkingMode)
	if err != nil {
		return domain.Session{}, err
	}
	input.ThinkingMode = thinkingMode
	input.Metadata = valueutil.CloneMap(input.Metadata)
	delete(input.Metadata, "workspace_root")
	delete(input.Metadata, "sandbox_roots")
	if err := s.validateSessionPlacement(ctx, input.WorkspaceID, input.ProjectID); err != nil {
		return domain.Session{}, err
	}
	workspace, err := s.workspaces.Get(ctx, input.WorkspaceID)
	if err != nil {
		return domain.Session{}, err
	}
	defaultProviderID := strings.TrimSpace(workspace.DefaultProviderID)
	// Session 建立後便保存實際 Provider 與模型，避免 Workspace 日後調整預設值時，
	// 舊對話在沒有明確操作的情況下被悄悄切換推理環境。
	if input.ProviderID == "" {
		input.ProviderID = defaultProviderID
	}
	if strings.TrimSpace(input.Model) == "" {
		if input.ProviderID == defaultProviderID {
			input.Model = strings.TrimSpace(workspace.Model)
		}
		if input.Model == "" {
			input.Model = s.defaultModelForProvider(input.ProviderID)
		}
	}
	if err := s.validateSessionProvider(input.ProviderID); err != nil {
		return domain.Session{}, err
	}
	profile, err := s.permissions.Resolve(input.PermissionProfile)
	if err != nil {
		return domain.Session{}, err
	}
	input.PermissionProfile = profile
	return engine.CreateSession(ctx, input)
}

func (s *Service) ListSessions(ctx context.Context, agentID string) ([]domain.Session, error) {
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return nil, err
	}
	values, err := engine.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	return s.decorateSessionsUsage(ctx, values)
}

// ReorderSessions 只接受同一 Workspace、同一 Project 內完整且不重複的未釘選
// Session ID。ProjectID 是空字串時代表未分類；排序不具備搬移語意。
func (s *Service) ReorderSessions(ctx context.Context, agentID string, input domain.ReorderSessionsInput) ([]domain.Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	agentID = strings.TrimSpace(agentID)
	engine, err := s.registry.Get(agentID)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if err := s.validateSessionPlacement(ctx, input.WorkspaceID, input.ProjectID); err != nil {
		return nil, err
	}
	values, err := engine.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make(map[string]domain.Session)
	for _, session := range values {
		if session.WorkspaceID == input.WorkspaceID && session.ProjectID == input.ProjectID && !session.Pinned {
			candidates[session.ID] = session
		}
	}
	if len(input.SessionIDs) == 0 || len(input.SessionIDs) != len(candidates) {
		return nil, fmt.Errorf("%w: session order must contain every unpinned session in the same project", domain.ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(input.SessionIDs))
	for _, rawID := range input.SessionIDs {
		sessionID := strings.TrimSpace(rawID)
		if sessionID == "" {
			return nil, fmt.Errorf("%w: session id cannot be empty", domain.ErrInvalidInput)
		}
		if _, duplicate := seen[sessionID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate session id %q", domain.ErrConflict, sessionID)
		}
		if _, exists := candidates[sessionID]; !exists {
			return nil, fmt.Errorf("%w: session %q does not belong to the requested project", domain.ErrInvalidInput, sessionID)
		}
		seen[sessionID] = struct{}{}
	}

	updated := make([]domain.Session, 0, len(input.SessionIDs))
	changed := make([]domain.Session, 0, len(input.SessionIDs))
	for position, sessionID := range input.SessionIDs {
		current := candidates[sessionID]
		if current.Position == position {
			updated = append(updated, current)
			continue
		}
		value, updateErr := engine.UpdateSession(ctx, sessionID, domain.UpdateSessionInput{Position: &position})
		if updateErr != nil {
			for _, previous := range changed {
				originalPosition := previous.Position
				_, _ = engine.UpdateSession(context.WithoutCancel(ctx), previous.ID, domain.UpdateSessionInput{Position: &originalPosition})
			}
			return nil, updateErr
		}
		changed = append(changed, current)
		updated = append(updated, value)
	}
	return s.decorateSessionsUsage(ctx, updated)
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	return s.decorateSessionUsage(ctx, session)
}

func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.hasActiveSession(sessionID) {
		return fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	if err := engine.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	return s.plans.DeleteSession(ctx, sessionID)
}

func (s *Service) UpdateSession(ctx context.Context, sessionID string, input domain.UpdateSessionInput) (domain.Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, current, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if s.hasActiveSession(sessionID) {
		return domain.Session{}, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	workspaceID := current.WorkspaceID
	projectID := current.ProjectID
	if input.WorkspaceID != nil {
		workspaceID = strings.TrimSpace(*input.WorkspaceID)
		input.WorkspaceID = &workspaceID
	}
	if input.ProjectID != nil {
		projectID = strings.TrimSpace(*input.ProjectID)
		input.ProjectID = &projectID
	}
	if input.WorkspaceID != nil || input.ProjectID != nil || input.ProviderID != nil {
		if err := s.validateSessionPlacement(ctx, workspaceID, projectID); err != nil {
			return domain.Session{}, err
		}
	}
	providerID := current.ProviderID
	if input.ProviderID != nil {
		providerID = strings.TrimSpace(*input.ProviderID)
		input.ProviderID = &providerID
	}
	if input.WorkspaceID != nil || input.ProviderID != nil {
		if err := s.validateSessionProvider(providerID); err != nil {
			return domain.Session{}, err
		}
	}
	if input.PermissionProfile != nil {
		profile, resolveErr := s.permissions.Resolve(*input.PermissionProfile)
		if resolveErr != nil {
			return domain.Session{}, resolveErr
		}
		input.PermissionProfile = &profile
	}
	if input.ThinkingMode != nil {
		thinkingMode, normalizeErr := domain.NormalizeThinkingMode(*input.ThinkingMode)
		if normalizeErr != nil {
			return domain.Session{}, normalizeErr
		}
		input.ThinkingMode = &thinkingMode
	}
	updated, err := engine.UpdateSession(ctx, sessionID, input)
	if err != nil {
		return domain.Session{}, err
	}
	if input.LockPlans != nil {
		if _, err := s.plans.Reconcile(ctx, updated.ID, updated.LockPlans); err != nil {
			return domain.Session{}, err
		}
	}
	return s.decorateSessionUsage(ctx, updated)
}

func (s *Service) CreateProject(ctx context.Context, input domain.CreateProjectInput) (domain.Project, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if _, err := s.workspaces.Get(ctx, input.WorkspaceID); err != nil {
		return domain.Project{}, err
	}
	project, err := s.projects.Create(ctx, input)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.Ephemeral {
		return project, nil
	}
	if !s.memoryIsolatedProjects.Load() {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("%w: 記憶體隔離專案已在實驗性功能中關閉", domain.ErrConflict)
	}
	if s.ephemeralProjects == nil {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("%w: memory-isolated project support is unavailable", domain.ErrConflict)
	}
	if _, err := s.ephemeralProjects.Prepare(ctx, project.ID, project.RAMDiskSizeMB); err != nil {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("prepare memory-isolated project: %w", err)
	}
	return project, nil
}

// sessionInstructions 依 Workspace → Project 的順序收集職務說明。
//
// 這是「寫一次、每次對話都適用」的常駐指示：使用者不必在每個新對話重述工作規則，
// 排程建立的對話也會自動沿用所屬 Workspace 的說明。
func (s *Service) sessionInstructions(ctx context.Context, session domain.Session) ([]any, error) {
	entries := []any{}
	if workspaceID := strings.TrimSpace(session.WorkspaceID); workspaceID != "" {
		workspace, err := s.workspaces.Get(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		if text := strings.TrimSpace(workspace.Instructions); text != "" {
			entries = append(entries, map[string]any{"scope": "workspace", "name": workspace.Name, "text": text})
		}
	}
	if projectID := strings.TrimSpace(session.ProjectID); projectID != "" {
		project, err := s.projects.Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if text := strings.TrimSpace(project.Instructions); text != "" {
			entries = append(entries, map[string]any{"scope": "project", "name": project.Name, "text": text})
		}
		// 記憶體隔離專案的工作區關閉即消失。不講明的話，模型會照常寫檔、回報完成，
		// 使用者事後才發現硬碟上什麼都沒有——那不是模型說謊，是它根本不知道環境是揮發的。
		if project.Ephemeral {
			entries = append(entries, map[string]any{
				"scope": "project",
				"name":  project.Name,
				"text": "這個專案的工作目錄建立在揮發性 RAM Disk 上，程式關閉或重啟後全部消失。" +
					"需要長期保留的產出，必須在同一次對話裡告訴使用者它不會被保留，並說明可以複製到哪裡；" +
					"不要假設下次對話還讀得到這次寫的檔案。",
			})
		}
	}
	return entries, nil
}

func (s *Service) ListProjects(ctx context.Context) ([]domain.Project, error) {
	return s.projects.List(ctx)
}

func (s *Service) GetProject(ctx context.Context, projectID string) (domain.Project, error) {
	return s.projects.Get(ctx, strings.TrimSpace(projectID))
}

func (s *Service) UpdateProject(ctx context.Context, projectID string, input domain.UpdateProjectInput) (domain.Project, error) {
	return s.projects.Update(ctx, strings.TrimSpace(projectID), input)
}

// DeleteProject 只刪除空專案；Session 必須先移至其他專案或未分類，避免隱含級聯刪除。
// DeleteProject 刪除專案。force 為真時連同專案底下的對話一起刪除。
//
// 預設拒絕是對的：專案裡的對話是使用者的工作紀錄，不該因為刪一個分類就一起消失。
// 但拒絕之後使用者唯一的出路是手動一則一則刪，對話多的時候等於刪不掉——
// force 讓他在知道後果的前提下一次完成，而不是提高門檻逼他放棄。
func (s *Service) DeleteProject(ctx context.Context, projectID string, force bool) error {
	projectID = strings.TrimSpace(projectID)
	project, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	type ownedSession struct {
		engine ports.AgentEngine
		id     string
	}
	owned := []ownedSession{}
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if session.ProjectID != projectID {
				continue
			}
			if !force {
				return fmt.Errorf("%w: project still contains sessions", domain.ErrConflict)
			}
			owned = append(owned, ownedSession{engine: engine, id: session.ID})
		}
	}
	// 對話先刪、專案後刪：反過來的話中途失敗會留下一批指向不存在專案的孤兒對話，
	// 它們在側邊欄不屬於任何分組，使用者也找不到入口處理。
	for _, session := range owned {
		if err := session.engine.DeleteSession(ctx, session.id); err != nil {
			return fmt.Errorf("刪除專案底下的對話 %s: %w", session.id, err)
		}
	}
	if project.Ephemeral && s.ephemeralProjects != nil {
		if err := s.ephemeralProjects.Release(ctx, projectID); err != nil {
			return fmt.Errorf("release memory-isolated project: %w", err)
		}
	}
	return s.projects.Delete(ctx, projectID)
}

func (s *Service) CreateWorkspace(ctx context.Context, input domain.CreateWorkspaceInput) (domain.Workspace, error) {
	input.ProviderIDs = normalizeStrings(input.ProviderIDs)
	input.DefaultProviderID = strings.TrimSpace(input.DefaultProviderID)
	if len(input.ProviderIDs) == 0 || input.DefaultProviderID == "" {
		return domain.Workspace{}, fmt.Errorf("%w: provider_ids and default_provider_id are required", domain.ErrInvalidInput)
	}
	for _, providerID := range input.ProviderIDs {
		if err := s.validateProvider(providerID); err != nil {
			return domain.Workspace{}, err
		}
	}
	if !contains(input.ProviderIDs, input.DefaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: default provider must belong to provider_ids", domain.ErrInvalidInput)
	}
	return s.workspaces.Create(ctx, input)
}

func (s *Service) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	return s.workspaces.List(ctx)
}

func (s *Service) GetWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	return s.workspaces.Get(ctx, strings.TrimSpace(workspaceID))
}

func (s *Service) UpdateWorkspace(ctx context.Context, workspaceID string, input domain.UpdateWorkspaceInput) (domain.Workspace, error) {
	current, err := s.workspaces.Get(ctx, strings.TrimSpace(workspaceID))
	if err != nil {
		return domain.Workspace{}, err
	}
	providerIDs := current.ProviderIDs
	defaultProviderID := current.DefaultProviderID
	if input.ProviderIDs != nil {
		values := normalizeStrings(*input.ProviderIDs)
		input.ProviderIDs = &values
		providerIDs = values
	}
	for _, providerID := range providerIDs {
		if err := s.validateProvider(providerID); err != nil {
			return domain.Workspace{}, err
		}
	}
	if input.DefaultProviderID != nil {
		value := strings.TrimSpace(*input.DefaultProviderID)
		input.DefaultProviderID = &value
		defaultProviderID = value
	}
	if len(providerIDs) == 0 || !contains(providerIDs, defaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: default provider must belong to provider_ids", domain.ErrInvalidInput)
	}
	if err := s.validateProvider(defaultProviderID); err != nil {
		return domain.Workspace{}, err
	}
	return s.workspaces.Update(ctx, strings.TrimSpace(workspaceID), input)
}

func (s *Service) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if _, err := s.workspaces.Get(ctx, workspaceID); err != nil {
		return err
	}
	projects, err := s.projects.List(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.WorkspaceID == workspaceID {
			return fmt.Errorf("%w: workspace still contains projects", domain.ErrConflict)
		}
	}
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if session.WorkspaceID == workspaceID {
				return fmt.Errorf("%w: workspace still contains sessions", domain.ErrConflict)
			}
		}
	}
	values, err := s.workspaces.List(ctx)
	if err != nil {
		return err
	}
	if len(values) <= 1 {
		return fmt.Errorf("%w: cannot delete the last workspace", domain.ErrConflict)
	}
	return s.workspaces.Delete(ctx, workspaceID)
}

func (s *Service) ListProviders() []domain.ProviderDescriptor {
	return s.providers.ListProviders()
}

// ProviderCapabilities 回傳指定 Provider／Model 實際套用的 context 與輸出限制。
// 個別模型覆寫由 ProviderCatalog 統一解析，前端不必複製模型名稱比對規則。
func (s *Service) ProviderCapabilities(providerID, model string) (domain.ModelCapabilities, error) {
	providerID = strings.TrimSpace(providerID)
	if err := s.validateProvider(providerID); err != nil {
		return domain.ModelCapabilities{}, err
	}
	return s.providers.Capabilities(providerID, strings.TrimSpace(model)), nil
}

func (s *Service) validateSessionPlacement(ctx context.Context, workspaceID, projectID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("%w: workspace_id is required", domain.ErrInvalidInput)
	}
	if _, err := s.workspaces.Get(ctx, workspaceID); err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	project, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if project.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: project does not belong to workspace", domain.ErrInvalidInput)
	}
	return nil
}

func (s *Service) validateProvider(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	if !s.providers.HasProvider(providerID) {
		return fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	return nil
}

func (s *Service) defaultModelForProvider(providerID string) string {
	providerID = strings.TrimSpace(providerID)
	for _, provider := range s.providers.ListProviders() {
		if strings.TrimSpace(provider.ID) == providerID {
			return strings.TrimSpace(provider.DefaultModel)
		}
	}
	return ""
}

// validateSessionProvider 只檢查 Provider 是否為全域已啟用項目。Workspace 提供
// Session 的預設值，但不限制對話可使用的 Provider 範圍。
func (s *Service) validateSessionProvider(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	return s.validateProvider(providerID)
}

func (s *Service) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return engine.ListMessages(ctx, sessionID)
}

// RetractMessages 支援「重新提問」：把最後一則使用者訊息與其後的回答、工具過程
// 移出對話，讓同一個問題可以在乾淨的狀態下重跑。有 Run 在跑時不接受，否則會與
// 正在寫入 transcript 的流程互相踩踏。
func (s *Service) RetractMessages(ctx context.Context, sessionID, messageID string) ([]domain.Message, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if s.hasActiveSession(sessionID) {
		return nil, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	return engine.RetractMessages(ctx, sessionID, messageID)
}

func (s *Service) ListEntries(ctx context.Context, sessionID string) ([]domain.SessionEntry, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return engine.ListEntries(ctx, sessionID)
}

// ListEntriesPage 把分頁一路傳到儲存層：只讀這一頁需要的位元組。
func (s *Service) ListEntriesPage(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]domain.SessionEntry, bool, error) {
	engine, _, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	return engine.ListEntriesPage(ctx, sessionID, afterSequence, limit)
}

// CompactSession 手動壓縮 Session 的對話歷史。
//
// 有 Run 在跑時拒絕：壓縮會寫入 transcript，而 Run 正在同一份 transcript 上讀寫，
// 兩邊同時動會讓那一輪送給模型的歷史跟畫面上的對不起來。
func (s *Service) CompactSession(ctx context.Context, sessionID string) (domain.ContextCompactionResult, error) {
	engine, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.ContextCompactionResult{}, err
	}
	runs, err := s.runs.List(ctx, session.ID)
	if err != nil {
		return domain.ContextCompactionResult{}, err
	}
	for _, run := range runs {
		switch run.Status {
		case domain.RunStatusQueued, domain.RunStatusRunning, domain.RunStatusPaused, domain.RunStatusWaitingApproval:
			return domain.ContextCompactionResult{}, fmt.Errorf("%w: 這個 Session 還有進行中的工作，結束後才能手動壓縮", domain.ErrConflict)
		}
	}
	return engine.CompactSession(ctx, session.ID)
}

func (s *Service) ListRuns(ctx context.Context, sessionID string) ([]domain.Run, error) {
	return s.runs.List(ctx, strings.TrimSpace(sessionID))
}

func (s *Service) GetRun(ctx context.Context, runID string) (domain.Run, error) {
	return s.runs.Get(ctx, strings.TrimSpace(runID))
}

func (s *Service) decorateSessionsUsage(ctx context.Context, sessions []domain.Session) ([]domain.Session, error) {
	for index := range sessions {
		value, err := s.decorateSessionUsage(ctx, sessions[index])
		if err != nil {
			return nil, err
		}
		sessions[index] = value
	}
	return sessions, nil
}

func (s *Service) decorateSessionUsage(ctx context.Context, session domain.Session) (domain.Session, error) {
	runs, err := s.runs.List(ctx, session.ID)
	if err != nil {
		return domain.Session{}, err
	}
	session.Usage = summarizeSessionUsage(runs)
	return session, nil
}

func (s *Service) ListRunEvents(ctx context.Context, runID string, afterSequence int64) ([]domain.Event, error) {
	runID = strings.TrimSpace(runID)
	if _, err := s.runs.Get(ctx, runID); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, fmt.Errorf("%w: after_sequence cannot be negative", domain.ErrInvalidInput)
	}
	return s.events.List(ctx, runID, afterSequence)
}

// StartRun 建立 durable Run 後立即返回。實際工作使用 service-owned context，
// 不會因建立 Run 的 HTTP request 結束或 SSE client 斷線而取消。
func (s *Service) StartRun(ctx context.Context, input domain.RunInput) (domain.Run, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.UserInput = strings.TrimSpace(textutil.NormalizeFullwidthASCII(input.UserInput))
	input.AttachmentIDs = normalizedAttachmentIDs(input.AttachmentIDs)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.SessionID == "" || input.UserInput == "" {
		return domain.Run{}, fmt.Errorf("%w: session_id and input are required", domain.ErrInvalidInput)
	}
	if len(input.AttachmentIDs) > 16 {
		return domain.Run{}, fmt.Errorf("%w: no more than 16 attachments are allowed", domain.ErrInvalidInput)
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	engine, session, err := s.resolveSession(ctx, input.SessionID)
	if err != nil {
		return domain.Run{}, err
	}
	input.Metadata = valueutil.CloneMap(input.Metadata)
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	// sandbox_roots 是後端根據 Project 產生的保留欄位，不能由 Run 呼叫端自行擴權。
	delete(input.Metadata, "sandbox_roots")
	// attachments 同樣是後端解析 Attachment ID 後產生的可信 manifest；Client
	// 直接夾帶同名 metadata 不能注入任意主機路徑。
	delete(input.Metadata, "attachments")
	// ephemeral_project 決定這次 Run 要不要跳過人工核准，因此必須是後端自己判定的
	// 保留欄位。若讓 Client 夾帶，等於給了一個「宣告自己是記憶體專案就免審核」的後門。
	delete(input.Metadata, "ephemeral_project")
	// ThinkingMode 是明確的 Run override；為相容舊 Client，也接受 metadata 中的
	// thinking_mode。若兩者都沒有，才沿用 Session 設定；空值表示 Provider 預設。
	rawThinkingMode := strings.TrimSpace(input.ThinkingMode)
	if rawThinkingMode == "" {
		if value, exists := input.Metadata["thinking_mode"]; exists {
			legacy, ok := value.(string)
			if !ok && value != nil {
				return domain.Run{}, fmt.Errorf("%w: thinking_mode must be a string", domain.ErrInvalidInput)
			}
			rawThinkingMode = legacy
		} else {
			rawThinkingMode = session.ThinkingMode
		}
	}
	thinkingMode, err := domain.NormalizeThinkingMode(rawThinkingMode)
	if err != nil {
		return domain.Run{}, err
	}
	input.ThinkingMode = thinkingMode
	// 保存到 Run metadata，讓重試時不會因 Session 後來變更而改變原本的設定。
	input.Metadata["thinking_mode"] = thinkingMode
	sandboxRoots := []string{}
	ephemeralProjectID := ""
	// 後端內部流程（排程執行器）帶進來的沙箱優先，讓相對路徑以它為基準。
	for _, root := range input.SandboxRoots {
		if root = strings.TrimSpace(root); root != "" {
			sandboxRoots = appendUniqueString(sandboxRoots, root)
		}
	}
	if session.ProjectID != "" {
		project, projectErr := s.projects.Get(ctx, session.ProjectID)
		if projectErr != nil {
			return domain.Run{}, projectErr
		}
		if project.Ephemeral {
			if s.ephemeralProjects == nil {
				return domain.Run{}, fmt.Errorf("%w: memory-isolated project support is unavailable", domain.ErrConflict)
			}
			root, prepareErr := s.ephemeralProjects.Prepare(ctx, project.ID, project.RAMDiskSizeMB)
			if prepareErr != nil {
				return domain.Run{}, fmt.Errorf("prepare memory-isolated project: %w", prepareErr)
			}
			sandboxRoots = appendUniqueString(sandboxRoots, root)
			ephemeralProjectID = project.ID
		} else {
			for _, root := range project.SandboxRoots {
				sandboxRoots = appendUniqueString(sandboxRoots, root)
			}
		}
	}
	// Session 私有工作目錄由後端建立，用來容納對話附件與未綁定 Project 的工作。
	// Project 根目錄仍排第一，確保相對路徑維持以 Project 為基準。
	if workspaceRoot, _ := session.Metadata["workspace_root"].(string); ephemeralProjectID == "" && strings.TrimSpace(workspaceRoot) != "" {
		sandboxRoots = appendUniqueString(sandboxRoots, strings.TrimSpace(workspaceRoot))
	}
	if len(sandboxRoots) > 0 {
		input.Metadata["sandbox_roots"] = sandboxRoots
	}
	if ephemeralProjectID != "" {
		input.Metadata["ephemeral_project"] = true
	}
	// instructions 同樣是後端依 Workspace／Project 產生的保留欄位；
	// 呼叫端不能藉 metadata 自行往提示注入內容。
	delete(input.Metadata, "instructions")
	instructions, err := s.sessionInstructions(ctx, session)
	if err != nil {
		return domain.Run{}, err
	}
	if len(instructions) > 0 {
		input.Metadata["instructions"] = instructions
	}
	if len(input.AttachmentIDs) > 0 {
		if s.attachments == nil {
			return domain.Run{}, fmt.Errorf("%w: attachment storage is unavailable", domain.ErrConflict)
		}
		manifest := make([]domain.Attachment, 0, len(input.AttachmentIDs))
		for _, attachmentID := range input.AttachmentIDs {
			attachment, attachmentErr := s.attachments.Get(ctx, session.ID, attachmentID)
			if attachmentErr != nil {
				return domain.Run{}, attachmentErr
			}
			if ephemeralProjectID != "" {
				stagedPath, stageErr := s.ephemeralProjects.StageFile(ctx, ephemeralProjectID, attachment.Path, attachment.ID+"-"+attachment.Name)
				if stageErr != nil {
					return domain.Run{}, fmt.Errorf("stage attachment in memory-isolated project: %w", stageErr)
				}
				attachment.Path = stagedPath
			}
			manifest = append(manifest, attachment)
		}
		input.Metadata["attachments"] = manifest
	}
	input.ProviderID = valueutil.FirstNonEmpty(strings.TrimSpace(input.ProviderID), strings.TrimSpace(session.ProviderID))
	input.Model = valueutil.FirstNonEmpty(strings.TrimSpace(input.Model), strings.TrimSpace(session.Model))
	if session.WorkspaceID != "" && (input.ProviderID == "" || input.Model == "") {
		workspace, workspaceErr := s.workspaces.Get(ctx, session.WorkspaceID)
		if workspaceErr != nil {
			return domain.Run{}, workspaceErr
		}
		input.ProviderID = valueutil.FirstNonEmpty(input.ProviderID, workspace.DefaultProviderID)
		input.Model = valueutil.FirstNonEmpty(input.Model, workspace.Model)
	}
	if input.ProviderID == "" {
		input.ProviderID = s.providers.DefaultProviderID()
	}
	if err := s.validateSessionProvider(input.ProviderID); err != nil {
		return domain.Run{}, err
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return domain.Run{}, fmt.Errorf("%w: service is shutting down", domain.ErrConflict)
	}
	fingerprint, err := runInputFingerprint(input)
	if err != nil {
		return domain.Run{}, err
	}
	if input.IdempotencyKey != "" {
		existing, findErr := s.runs.FindByIdempotencyKey(ctx, input.SessionID, input.IdempotencyKey)
		if findErr == nil {
			if !sameIdempotentRun(existing, input, fingerprint) {
				return domain.Run{}, fmt.Errorf("%w: Idempotency-Key %q was already used with different run input", domain.ErrConflict, input.IdempotencyKey)
			}
			return existing, nil
		}
		if !errors.Is(findErr, domain.ErrNotFound) {
			return domain.Run{}, findErr
		}
	}

	now := s.now().UTC()
	run := domain.Run{
		ID:                     domain.NewID("run"),
		AgentID:                session.AgentID,
		SessionID:              session.ID,
		Status:                 domain.RunStatusQueued,
		Input:                  input.UserInput,
		AttachmentIDs:          append([]string(nil), input.AttachmentIDs...),
		ProviderID:             input.ProviderID,
		Model:                  input.Model,
		IdempotencyKey:         input.IdempotencyKey,
		IdempotencyFingerprint: fingerprint,
		Metadata:               valueutil.CloneMap(input.Metadata),
		CreatedAt:              now,
	}
	if err := s.runs.Save(ctx, run); err != nil {
		return domain.Run{}, err
	}
	input.RunID = run.ID
	runCtx, cancel := context.WithCancel(s.rootCtx)
	s.mu.Lock()
	s.active[run.ID] = activeRun{sessionID: session.ID, cancel: cancel}
	s.mu.Unlock()
	s.wg.Add(1)
	s.logger.Info("run queued", "run_id", run.ID, "session_id", session.ID, "agent_id", session.AgentID, "provider_id", run.ProviderID, "model", run.Model)
	go s.executeRun(runCtx, engine, session, input, run)
	return run, nil
}

// runInputFingerprint 只納入會決定實際工作的穩定欄位。後端產生的 instructions、
// sandbox manifest 與其他環境 metadata 不參與雜湊，避免相同 Client request 因為
// 後端重新載入設定而失去冪等性。
func runInputFingerprint(input domain.RunInput) (string, error) {
	payload := struct {
		SessionID     string   `json:"session_id"`
		Input         string   `json:"input"`
		AttachmentIDs []string `json:"attachment_ids,omitempty"`
		ProviderID    string   `json:"provider_id,omitempty"`
		Model         string   `json:"model,omitempty"`
		ThinkingMode  string   `json:"thinking_mode,omitempty"`
	}{
		SessionID:     input.SessionID,
		Input:         input.UserInput,
		AttachmentIDs: append([]string(nil), input.AttachmentIDs...),
		ProviderID:    input.ProviderID,
		Model:         input.Model,
		ThinkingMode:  input.ThinkingMode,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode idempotent run input: %w", err)
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

func sameIdempotentRun(run domain.Run, input domain.RunInput, fingerprint string) bool {
	if run.IdempotencyFingerprint != "" {
		return run.IdempotencyFingerprint == fingerprint
	}
	if run.SessionID != input.SessionID || run.Input != input.UserInput || run.ProviderID != input.ProviderID || run.Model != input.Model {
		return false
	}
	if thinkingMode, ok := run.Metadata["thinking_mode"].(string); ok && thinkingMode != input.ThinkingMode {
		return false
	}
	if len(run.AttachmentIDs) != len(input.AttachmentIDs) {
		return false
	}
	for index := range run.AttachmentIDs {
		if run.AttachmentIDs[index] != input.AttachmentIDs[index] {
			return false
		}
	}
	return true
}

// ExecuteRun 提供同步等待介面；底層仍是非同步 Run。ctx 取消只停止等待，
// 若要取消工作本身必須明確呼叫 CancelRun。
func (s *Service) ExecuteRun(ctx context.Context, input domain.RunInput, sink EventSink) (domain.Run, error) {
	run, err := s.StartRun(ctx, input)
	if err != nil {
		return domain.Run{}, err
	}
	return s.WaitRun(ctx, run.ID, 0, sink)
}

func (s *Service) WaitRun(ctx context.Context, runID string, afterSequence int64, sink EventSink) (domain.Run, error) {
	for {
		updates, unsubscribe, err := s.SubscribeRunEvents(ctx, runID)
		if err != nil {
			return domain.Run{}, err
		}
		events, err := s.ListRunEvents(ctx, runID, afterSequence)
		if err != nil {
			unsubscribe()
			return domain.Run{}, err
		}
		for _, event := range events {
			if sink != nil {
				if sinkErr := sink(event); sinkErr != nil {
					unsubscribe()
					current, _ := s.GetRun(context.WithoutCancel(ctx), runID)
					return current, sinkErr
				}
			}
			afterSequence = event.Sequence
		}
		run, err := s.GetRun(ctx, runID)
		if err != nil {
			unsubscribe()
			return domain.Run{}, err
		}
		if terminalRun(run.Status) {
			unsubscribe()
			return run, nil
		}
		select {
		case <-ctx.Done():
			unsubscribe()
			return run, ctx.Err()
		case <-updates:
			unsubscribe()
		}
	}
}

// SubscribeRunEvents 只提供喚醒通知；事件內容一律重新讀 durable log，
// 因此通知合併或 client 暫時離線都不會造成事件缺口。
func (s *Service) SubscribeRunEvents(ctx context.Context, runID string) (<-chan struct{}, func(), error) {
	runID = strings.TrimSpace(runID)
	if _, err := s.runs.Get(ctx, runID); err != nil {
		return nil, nil, err
	}
	channel := make(chan struct{}, 1)
	s.mu.Lock()
	s.nextWatcher++
	id := s.nextWatcher
	if s.watchers[runID] == nil {
		s.watchers[runID] = map[uint64]chan struct{}{}
	}
	s.watchers[runID][id] = channel
	s.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.watchers[runID], id)
			if len(s.watchers[runID]) == 0 {
				delete(s.watchers, runID)
			}
			s.mu.Unlock()
		})
	}
	return channel, unsubscribe, nil
}

func (s *Service) CancelRun(ctx context.Context, runID string) (domain.Run, error) {
	run, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	s.mu.Lock()
	active, ok := s.active[run.ID]
	s.mu.Unlock()
	if !ok {
		if terminalRun(run.Status) {
			return run, nil
		}
		return run, fmt.Errorf("%w: run is not active", domain.ErrConflict)
	}
	// 先記錄取消意圖，再中止 context；執行緒可能剛好位於完成轉換的邊界，
	// 這個旗標讓它不會在取消請求之後把舊快照寫回 completed。
	s.mu.Lock()
	if current, exists := s.active[run.ID]; exists {
		current.cancelRequested = true
		s.active[run.ID] = current
	}
	s.mu.Unlock()
	active.cancel()
	return s.persistImmediateCancellation(run)
}

// persistImmediateCancellation 讓控制 API 立即反映使用者的停止意圖，
// 不把完成時間綁在第三方 Provider 是否正確遵守 context 取消上。
func (s *Service) persistImmediateCancellation(run domain.Run) (domain.Run, error) {
	latest, err := s.runs.Get(context.Background(), run.ID)
	if err != nil {
		return run, err
	}
	if terminalRun(latest.Status) {
		return latest, nil
	}
	completedAt := s.now().UTC()
	latest.Status = domain.RunStatusCanceled
	latest.PendingApproval = nil
	latest.Error = &domain.RunError{Code: "run_canceled", Message: "run canceled", Retryable: true}
	latest.CompletedAt = &completedAt
	if err := s.runs.Save(context.Background(), latest); err != nil {
		return run, err
	}
	payload := map[string]any{"status": latest.Status, "error": latest.Error}
	if latest.Usage != nil {
		payload["usage"] = *latest.Usage
	}
	if err := s.appendControlEvent(latest, "run.canceled", payload); err != nil {
		// Run 狀態已經 durable；事件可由啟動時的 reconcile 補齊，不能讓
		// 使用者看到取消 API 失敗而重送，造成控制結果更加不確定。
		s.logger.Error("run cancellation event write failed", "run_id", latest.ID, "error", err)
		s.notifyRun(latest.ID)
	}
	s.notifyRunFinished(latest)
	return latest, nil
}

// AnswerQuestion 把使用者的抉擇送回正在等待的工具。
//
// 取消也走這裡：使用者沒有義務回答，取消是一種合法答案而不是錯誤。
func (s *Service) AnswerQuestion(_ context.Context, questionID string, answer domain.UserQuestionAnswer) error {
	if s.questions == nil {
		return fmt.Errorf("%w: question workflow is unavailable", domain.ErrConflict)
	}
	answer.QuestionID = strings.TrimSpace(questionID)
	return s.questions.Answer(answer)
}

func (s *Service) DecideRun(ctx context.Context, runID string, input domain.ToolApprovalDecisionInput) (domain.Run, error) {
	if s.approvals == nil {
		return domain.Run{}, fmt.Errorf("%w: approval workflow is unavailable", domain.ErrConflict)
	}
	run, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	input.ApprovalID = strings.TrimSpace(input.ApprovalID)
	if run.Status != domain.RunStatusWaitingApproval || run.PendingApproval == nil {
		return run, fmt.Errorf("%w: run is not waiting for approval", domain.ErrConflict)
	}
	if input.ApprovalID == "" || input.ApprovalID != run.PendingApproval.ID {
		return run, fmt.Errorf("%w: approval_id does not match the pending approval", domain.ErrConflict)
	}
	if input.Permanent && input.Decision != domain.ToolApprovalApprove {
		return run, fmt.Errorf("%w: permanent approval requires an approve decision", domain.ErrInvalidInput)
	}
	var permanentEngine ports.AgentEngine
	permanentChanged := false
	if input.Permanent {
		engine, session, resolveErr := s.resolveSession(ctx, run.SessionID)
		if resolveErr != nil {
			return run, resolveErr
		}
		permanentEngine = engine
		if !session.PermanentToolApproval {
			if _, updateErr := engine.SetPermanentToolApproval(ctx, run.SessionID, true); updateErr != nil {
				return run, updateErr
			}
			permanentChanged = true
		}
	}
	if err := s.approvals.Decide(run.ID, input); err != nil {
		// 決策未送達 waiter 時回復剛才的持久化變更，避免一次失敗的 API
		// 呼叫意外替 Session 開啟永久核准。
		if permanentChanged && permanentEngine != nil {
			_, _ = permanentEngine.SetPermanentToolApproval(context.WithoutCancel(ctx), run.SessionID, false)
		}
		return run, err
	}
	// 決策喚醒 Harness 後，狀態會由 durable run.approval_resolved 事件切回 running。
	// 這裡重新讀取可取得已經完成切換的狀態；若 goroutine 尚未排程則仍回 waiting。
	if current, getErr := s.runs.Get(ctx, run.ID); getErr == nil {
		return current, nil
	}
	return run, nil
}

// RetryRun 建立新的 durable Run，保留原 Run 作為不可變稽核紀錄。呼叫端應提供
// Idempotency-Key，讓 HTTP 回應遺失時重送不會再建立第三份工作。
func (s *Service) RetryRun(ctx context.Context, runID, idempotencyKey string) (domain.Run, error) {
	original, err := s.runs.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, err
	}
	if original.Status != domain.RunStatusFailed && original.Status != domain.RunStatusCanceled {
		return original, fmt.Errorf("%w: only failed or canceled runs can be retried", domain.ErrConflict)
	}
	if original.Error == nil || !original.Error.Retryable {
		return original, fmt.Errorf("%w: run is not retryable", domain.ErrConflict)
	}
	metadata := valueutil.CloneMap(original.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	delete(metadata, "termination")
	delete(metadata, "budget_exceeded")
	metadata["retry_of"] = original.ID
	return s.StartRun(ctx, domain.RunInput{
		SessionID:      original.SessionID,
		UserInput:      original.Input,
		AttachmentIDs:  append([]string(nil), original.AttachmentIDs...),
		ProviderID:     original.ProviderID,
		Model:          original.Model,
		Metadata:       metadata,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
	})
}

func normalizedAttachmentIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *Service) Close(ctx context.Context) error {
	s.startMu.Lock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.stop()
	}
	s.mu.Unlock()
	s.startMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func approvalRequestFromPayload(payload map[string]any) (domain.ToolApprovalRequest, bool) {
	if payload == nil {
		return domain.ToolApprovalRequest{}, false
	}
	switch value := payload["approval"].(type) {
	case domain.ToolApprovalRequest:
		return value, strings.TrimSpace(value.ID) != ""
	case *domain.ToolApprovalRequest:
		if value != nil {
			return *value, strings.TrimSpace(value.ID) != ""
		}
	}
	return domain.ToolApprovalRequest{}, false
}

func (s *Service) executeRun(ctx context.Context, engine ports.AgentEngine, session domain.Session, input domain.RunInput, run domain.Run) {
	sequence := int64(0)
	defer s.wg.Done()
	defer s.clearActive(run.ID)
	defer func() {
		if recovered := recover(); recovered != nil {
			// 這裡過去完全沒有紀錄：panic 被吞掉，只留下一筆沒有原因的 failed run。
			s.logger.Error("run panicked", "run_id", run.ID, "session_id", run.SessionID, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
			latest, err := s.runs.Get(context.Background(), run.ID)
			if err == nil && !terminalRun(latest.Status) {
				s.finishFailed(latest, "agent_panic", fmt.Errorf("unexpected panic: %v", recovered), false, &sequence)
			}
		}
	}()

	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	release, err := s.acquireSession(ctx, session.ID, run.ID)
	if err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	defer func() {
		if release != nil {
			release()
		}
		s.clearSessionPause(session.ID, run.ID)
	}()
	if err := ctx.Err(); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}

	startedAt := s.now().UTC()
	run.Status = domain.RunStatusRunning
	run.StartedAt = &startedAt
	s.mu.Lock()
	if current, canceled := s.active[run.ID]; canceled && current.cancelRequested {
		s.mu.Unlock()
		return
	}
	runSaveErr := s.runs.Save(context.Background(), run)
	s.mu.Unlock()
	if runSaveErr != nil {
		s.finishFailed(run, "run_state_write_failed", runSaveErr, true, &sequence)
		return
	}
	if err := s.appendEvent(run, &sequence, "run.started", map[string]any{"status": run.Status}); err != nil {
		s.finishFailed(run, "event_write_failed", err, true, &sequence)
		return
	}

	result, runErr := engine.Run(ctx, input, func(event domain.EngineEvent) error {
		if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
			return err
		}
		switch event.Type {
		case "run.approval_required":
			request, ok := approvalRequestFromPayload(event.Payload)
			if !ok {
				return fmt.Errorf("%w: approval event has no request", domain.ErrInvalidInput)
			}
			run.Status = domain.RunStatusWaitingApproval
			run.PendingApproval = &request
			if err := s.runs.Save(context.Background(), run); err != nil {
				return err
			}
			if err := s.appendEvent(run, &sequence, event.Type, event.Payload); err != nil {
				return err
			}
			s.notifyApproval(run, request)
			// 先建立邏輯預約再歸還 gate，已排隊的其他 Run 才不會插入尚未完成的
			// assistant/tool 協定區段。
			s.pauseSession(session.ID, run.ID)
			release()
			release = nil
			return nil
		case "run.approval_resolved":
			if release == nil {
				newRelease, err := s.acquireSession(ctx, session.ID, run.ID)
				if err != nil {
					return err
				}
				release = newRelease
			}
			s.clearSessionPause(session.ID, run.ID)
			run.Status = domain.RunStatusRunning
			run.PendingApproval = nil
			if err := s.runs.Save(context.Background(), run); err != nil {
				return err
			}
			return s.appendEvent(run, &sequence, event.Type, event.Payload)
		default:
			if err := s.appendEvent(run, &sequence, event.Type, event.Payload); err != nil {
				return err
			}
			return s.waitIfRunPaused(ctx, run.ID)
		}
	})
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, domain.ErrCanceled) {
		saveRunUsage(&run, &result, s.modelPrices)
		s.finishCanceled(run, valueutil.FirstError(runErr, ctx.Err()), &sequence)
		return
	}
	if runErr != nil {
		saveRunUsage(&run, &result, s.modelPrices)
		code, retryable := classifyRunFailure(runErr)
		s.finishFailed(run, code, runErr, retryable, &sequence)
		return
	}
	saveRunUsage(&run, &result, s.modelPrices)
	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}

	completedAt := s.now().UTC()
	run.Status = domain.RunStatusCompleted
	run.Result = &result
	run.PendingApproval = nil
	if result.BudgetExceeded != nil {
		run.Metadata = valueutil.CloneMap(run.Metadata)
		if run.Metadata == nil {
			run.Metadata = map[string]any{}
		}
		run.Metadata["termination"] = "budget_exceeded"
		run.Metadata["budget_exceeded"] = *result.BudgetExceeded
	}
	run.CompletedAt = &completedAt
	// PauseRun 與 terminal transition 共用這把鎖，避免完成寫入後又被舊的
	// running 快照覆蓋成 paused。若 pause 先取得鎖，則在安全邊界等待恢復。
	var saveErr error
	for {
		s.mu.Lock()
		if current, canceled := s.active[run.ID]; canceled && current.cancelRequested {
			s.mu.Unlock()
			return
		}
		if !s.pausedRuns[run.ID] {
			s.logger.Info("run completed", "run_id", run.ID, "session_id", run.SessionID, "duration_ms", completedAt.Sub(startedAt).Milliseconds(), "event_count", sequence)
			saveErr = s.runs.Save(context.Background(), run)
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
			s.finishCanceled(run, err, &sequence)
			return
		}
	}
	if saveErr != nil {
		s.finishFailed(run, "run_state_write_failed", saveErr, true, &sequence)
		return
	}
	if err := s.appendEvent(run, &sequence, "run.completed", map[string]any{"status": run.Status, "result": result}); err != nil {
		s.finishFailed(run, "event_write_failed", err, true, &sequence)
		return
	}
	s.notifyRunFinished(run)
}

func classifyRunFailure(cause error) (code string, retryable bool) {
	switch {
	case errors.Is(cause, domain.ErrProviderProtocol):
		return "provider_protocol_incompatible", false
	case errors.Is(cause, domain.ErrInvalidInput):
		return "invalid_input", false
	default:
		return "agent_run_failed", true
	}
}

func (s *Service) appendEvent(run domain.Run, sequence *int64, eventType string, payload map[string]any) error {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	return s.appendEventLocked(run, sequence, eventType, payload)
}

// appendControlEvent 供 HTTP 控制操作使用；它會和 Harness 的事件寫入共用同一把鎖，
// 避免 pause/resume 與模型事件同時寫入時產生重複 sequence。
func (s *Service) appendControlEvent(run domain.Run, eventType string, payload map[string]any) error {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	sequence := s.eventSequences[run.ID]
	if sequence == 0 {
		sequence = s.latestRunSequence(context.Background(), run.ID)
	}
	return s.appendEventLocked(run, &sequence, eventType, payload)
}

func (s *Service) appendEventLocked(run domain.Run, sequence *int64, eventType string, payload map[string]any) error {
	// 取消會先落盤再寫 terminal event；若底層 Provider 在取消後仍送出回呼，
	// 不得讓 late event 排到 run.canceled 之後，破壞事件串流的終止語意。
	if latest, err := s.runs.Get(context.Background(), run.ID); err == nil && terminalRun(latest.Status) && eventType != "run."+string(latest.Status) {
		return fmt.Errorf("%w: cannot append %s after run.%s", domain.ErrConflict, eventType, latest.Status)
	}
	if latest := s.eventSequences[run.ID]; latest > *sequence {
		*sequence = latest
	}
	next := *sequence + 1
	event := domain.Event{
		SchemaVersion: domain.EventSchemaVersion,
		ID:            domain.NewID("evt"),
		Type:          eventType,
		AgentID:       run.AgentID,
		SessionID:     run.SessionID,
		RunID:         run.ID,
		Sequence:      next,
		CreatedAt:     s.now().UTC(),
		Payload:       valueutil.CloneMap(payload),
	}
	if err := s.events.Append(context.Background(), event); err != nil {
		return err
	}
	*sequence = next
	s.eventSequences[run.ID] = next
	s.notifyRun(run.ID)
	return nil
}

func (s *Service) finishCanceled(run domain.Run, cause error, sequence *int64) {
	s.logger.Info("run canceled", "run_id", run.ID, "session_id", run.SessionID, "cause", cause)
	if latest, err := s.runs.Get(context.Background(), run.ID); err == nil {
		if latest.Status == domain.RunStatusCanceled {
			// CancelRun 可能已先把狀態寫成 canceled，而 Provider 稍後才
			// 回傳取消結果；只補上最後收到的用量，不重複寫 terminal event。
			if run.Usage != nil {
				latest.Usage = run.Usage
				_ = s.runs.Save(context.Background(), latest)
			}
			return
		}
		if terminalRun(latest.Status) {
			return
		}
		run = latest
	}
	completedAt := s.now().UTC()
	run.Status = domain.RunStatusCanceled
	run.PendingApproval = nil
	run.Error = &domain.RunError{Code: "run_canceled", Message: "run canceled", Retryable: true}
	if cause != nil {
		run.Error.Message = cause.Error()
	}
	run.CompletedAt = &completedAt
	s.mu.Lock()
	_ = s.runs.Save(context.Background(), run)
	s.mu.Unlock()
	payload := map[string]any{"status": run.Status, "error": run.Error}
	if run.Usage != nil {
		payload["usage"] = *run.Usage
	}
	_ = s.appendEvent(run, sequence, "run.canceled", payload)
	s.notifyRunFinished(run)
}

func (s *Service) finishFailed(run domain.Run, code string, cause error, retryable bool, sequence *int64) {
	s.logger.Error("run failed", "run_id", run.ID, "session_id", run.SessionID, "code", code, "retryable", retryable, "error", cause)
	completedAt := s.now().UTC()
	run.Status = domain.RunStatusFailed
	run.PendingApproval = nil
	run.Error = &domain.RunError{Code: code, Message: cause.Error(), Retryable: retryable}
	run.CompletedAt = &completedAt
	s.mu.Lock()
	_ = s.runs.Save(context.Background(), run)
	s.mu.Unlock()
	payload := map[string]any{"status": run.Status, "error": run.Error}
	if run.Usage != nil {
		payload["usage"] = *run.Usage
	}
	_ = s.appendEvent(run, sequence, "run.failed", payload)
	s.notifyRunFinished(run)
}

func (s *Service) resolveSession(ctx context.Context, sessionID string) (ports.AgentEngine, domain.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, domain.Session{}, fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	for _, engine := range s.registry.Engines() {
		session, err := engine.GetSession(ctx, sessionID)
		if err == nil {
			return engine, session, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, domain.Session{}, err
		}
	}
	return nil, domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
}

func (s *Service) acquireSession(ctx context.Context, sessionID, runID string) (func(), error) {
	for {
		s.mu.Lock()
		gate := s.sessionGates[sessionID]
		if gate == nil {
			gate = make(chan struct{}, 1)
			gate <- struct{}{}
			s.sessionGates[sessionID] = gate
		}
		signal := s.sessionSignals[sessionID]
		if signal == nil {
			signal = make(chan struct{})
			s.sessionSignals[sessionID] = signal
		}
		pausedBy := s.pausedSessions[sessionID]
		s.mu.Unlock()
		if pausedBy != "" && pausedBy != runID {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-signal:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gate:
		}
		// pause 可能在取得 token 前一刻建立，因此持有 token 後必須再確認一次。
		s.mu.Lock()
		pausedBy = s.pausedSessions[sessionID]
		signal = s.sessionSignals[sessionID]
		s.mu.Unlock()
		if pausedBy == "" || pausedBy == runID {
			return func() { gate <- struct{}{} }, nil
		}
		gate <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-signal:
		}
	}
}

func (s *Service) pauseSession(sessionID, runID string) {
	s.mu.Lock()
	s.pausedSessions[sessionID] = runID
	s.notifySessionStateLocked(sessionID)
	s.mu.Unlock()
}

func (s *Service) clearSessionPause(sessionID, runID string) {
	s.mu.Lock()
	if s.pausedSessions[sessionID] == runID {
		delete(s.pausedSessions, sessionID)
		s.notifySessionStateLocked(sessionID)
	}
	s.mu.Unlock()
}

func (s *Service) notifySessionStateLocked(sessionID string) {
	if signal := s.sessionSignals[sessionID]; signal != nil {
		close(signal)
	}
	s.sessionSignals[sessionID] = make(chan struct{})
}

func (s *Service) clearActive(runID string) {
	s.mu.Lock()
	if item, ok := s.active[runID]; ok {
		item.cancel()
		delete(s.active, runID)
	}
	s.clearRunPauseLocked(runID)
	s.mu.Unlock()
}

func (s *Service) clearRunPause(runID string) {
	s.mu.Lock()
	s.clearRunPauseLocked(runID)
	s.mu.Unlock()
}

func (s *Service) clearRunPauseLocked(runID string) {
	delete(s.pausedRuns, runID)
	if signal := s.runSignals[runID]; signal != nil {
		close(signal)
	}
	delete(s.runSignals, runID)
}

func (s *Service) waitIfRunPaused(ctx context.Context, runID string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		paused := s.pausedRuns[runID]
		signal := s.runSignals[runID]
		s.mu.Unlock()
		if !paused {
			return nil
		}
		if signal == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-signal:
		}
	}
}

func (s *Service) hasActiveSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.active {
		if run.sessionID == sessionID {
			return true
		}
	}
	return false
}

func (s *Service) notifyRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, channel := range s.watchers[runID] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}

// reconcileTerminalEvents 補齊伺服器重啟或先前事件寫入失敗留下的終止事件，
// 讓任何 terminal Run 的 durable event log 都能自行說明最終狀態。
func (s *Service) reconcileTerminalEvents(ctx context.Context) error {
	runs, err := s.runs.List(ctx, "")
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !terminalRun(run.Status) {
			continue
		}
		values, err := s.events.List(ctx, run.ID, 0)
		if err != nil {
			return err
		}
		if len(values) > 0 && terminalEvent(values[len(values)-1].Type) {
			continue
		}
		sequence := int64(0)
		if len(values) > 0 {
			sequence = values[len(values)-1].Sequence
		}
		payload := map[string]any{"status": run.Status}
		if run.Result != nil {
			payload["result"] = *run.Result
		}
		if run.Usage != nil {
			payload["usage"] = *run.Usage
		}
		if run.Error != nil {
			payload["error"] = run.Error
		}
		s.logger.Warn("reconciling missing terminal event after restart", "run_id", run.ID, "status", run.Status)
		if err := s.appendEvent(run, &sequence, "run."+string(run.Status), payload); err != nil {
			return fmt.Errorf("reconcile run %q terminal event: %w", run.ID, err)
		}
		if run.Error != nil && run.Error.Code == "server_restarted" {
			s.notifyRunFinished(run)
		}
	}
	return nil
}

func terminalEvent(eventType string) bool {
	return eventType == "run.completed" || eventType == "run.failed" || eventType == "run.canceled"
}

func terminalRun(status domain.RunStatus) bool {
	return status == domain.RunStatusCompleted || status == domain.RunStatusFailed || status == domain.RunStatusCanceled
}

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
