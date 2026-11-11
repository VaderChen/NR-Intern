package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"log/slog"
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
	// inputs 承接執行中追加的使用者訊息；Run 進入終止狀態時關閉，
	// 尚未消化的訊息放進終止事件的 returned_inputs 退回。
	inputs         *domain.RunInputQueue
	modelSelection *domain.RunModelSelection
	providerID     string
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
	eventMu        [64]sync.Mutex
	eventSequences sync.Map // Run ID -> int64；檔案 I/O 只持有對應 Run 的分片鎖。
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
	}
	service.notificationsEnabled.Store(dependencies.NotificationsEnabled)
	service.memoryIsolatedProjects.Store(dependencies.MemoryIsolatedProjects)
	if err := service.reconcileTerminalEvents(context.Background()); err != nil {
		stop()
		return nil, err
	}
	service.reconcilePlanLoops(context.Background())
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
