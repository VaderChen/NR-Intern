package domain

import "time"

const EventSchemaVersion = "1.0"

type AgentDescriptor struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type Session struct {
	ID                string `json:"id"`
	AgentID           string `json:"agent_id"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	ProjectID         string `json:"project_id,omitempty"`
	Title             string `json:"title"`
	ProviderID        string `json:"provider_id,omitempty"`
	Model             string `json:"model,omitempty"`
	PermissionProfile string `json:"permission_profile,omitempty"`
	// PermanentToolApproval 只允許由目前 Session 的人工核准流程設定；一般
	// Session PATCH 不得直接開啟，避免 Client 繞過高風險工具審核。
	PermanentToolApproval bool           `json:"permanent_tool_approval,omitempty"`
	Pinned                bool           `json:"pinned,omitempty"`
	PinnedAt              *time.Time     `json:"pinned_at,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type Message struct {
	ID                string         `json:"id"`
	SessionID         string         `json:"session_id"`
	Role              string         `json:"role"`
	Content           string         `json:"content,omitempty"`
	Reasoning         string         `json:"reasoning,omitempty"`
	ToolCalls         []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID        string         `json:"tool_call_id,omitempty"`
	ToolName          string         `json:"tool_name,omitempty"`
	IsError           bool           `json:"is_error,omitempty"`
	ProviderID        string         `json:"provider_id,omitempty"`
	ProviderRequestID string         `json:"provider_request_id,omitempty"`
	Model             string         `json:"model,omitempty"`
	StopReason        string         `json:"stop_reason,omitempty"`
	Usage             *Usage         `json:"usage,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

// Attachment 是使用者透過 Browser／Desktop 對話輸入區上傳的檔案。
// Path 只由後端產生，固定落在 Session 私有工作目錄；Client 在建立 Run 時
// 只能提交 Attachment ID，不能自行指定任意主機路徑。
type Attachment struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	MediaType string    `json:"media_type,omitempty"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateSessionInput struct {
	Title             string         `json:"title,omitempty"`
	WorkspaceID       string         `json:"workspace_id"`
	ProjectID         string         `json:"project_id,omitempty"`
	ProviderID        string         `json:"provider_id,omitempty"`
	Model             string         `json:"model,omitempty"`
	PermissionProfile string         `json:"permission_profile,omitempty"`
	Pinned            bool           `json:"pinned,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type UpdateSessionInput struct {
	Title             *string `json:"title,omitempty"`
	WorkspaceID       *string `json:"workspace_id,omitempty"`
	ProjectID         *string `json:"project_id,omitempty"`
	ProviderID        *string `json:"provider_id,omitempty"`
	Model             *string `json:"model,omitempty"`
	PermissionProfile *string `json:"permission_profile,omitempty"`
	MemoryScope       *string `json:"memory_scope,omitempty"`
	Pinned            *bool   `json:"pinned,omitempty"`
}

type Project struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	SandboxRoots []string  `json:"sandbox_roots,omitempty"`
	Position     int       `json:"position"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateProjectInput struct {
	Name         string   `json:"name"`
	WorkspaceID  string   `json:"workspace_id"`
	Description  string   `json:"description,omitempty"`
	SandboxRoots []string `json:"sandbox_roots,omitempty"`
}

type UpdateProjectInput struct {
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	SandboxRoots *[]string `json:"sandbox_roots,omitempty"`
	Position     *int      `json:"position,omitempty"`
}

type Workspace struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description,omitempty"`
	ProviderIDs       []string  `json:"provider_ids"`
	DefaultProviderID string    `json:"default_provider_id"`
	Model             string    `json:"model,omitempty"`
	Position          int       `json:"position"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateWorkspaceInput struct {
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	ProviderIDs       []string `json:"provider_ids"`
	DefaultProviderID string   `json:"default_provider_id"`
	Model             string   `json:"model,omitempty"`
}

type UpdateWorkspaceInput struct {
	Name              *string   `json:"name,omitempty"`
	Description       *string   `json:"description,omitempty"`
	ProviderIDs       *[]string `json:"provider_ids,omitempty"`
	DefaultProviderID *string   `json:"default_provider_id,omitempty"`
	Model             *string   `json:"model,omitempty"`
	Position          *int      `json:"position,omitempty"`
}

type ProviderDescriptor struct {
	ID           string `json:"id"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint"`
	DefaultModel string `json:"default_model"`
	Streaming    bool   `json:"streaming"`
	HasAPIKey    bool   `json:"has_api_key"`
	// ContextWindow 與 MaxOutputTokens 是該 Provider 預設模型的宣告限制；0 代表未宣告。
	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

type RunInput struct {
	RunID          string         `json:"-"`
	SessionID      string         `json:"session_id"`
	UserInput      string         `json:"input"`
	AttachmentIDs  []string       `json:"attachment_ids,omitempty"`
	ProviderID     string         `json:"provider_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	IdempotencyKey string         `json:"-"`
}

type RunStatus string

const (
	RunStatusQueued          RunStatus = "queued"
	RunStatusRunning         RunStatus = "running"
	RunStatusWaitingApproval RunStatus = "waiting_approval"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusCanceled        RunStatus = "canceled"
)

// RunBudget 是單次 Harness 工作的安全上限。MaxTurns、MaxWallClock 與
// MaxToolCalls 用來避免工作長時間佔用資源，和模型 Context 容量及其自動壓縮無關。
// MaxTokens 與 MaxToolCalls 為 0 時表示不限制。
type RunBudget struct {
	MaxTurns     int           `json:"max_turns,omitempty"`
	MaxWallClock time.Duration `json:"-"`
	MaxTokens    int           `json:"max_tokens,omitempty"`
	MaxToolCalls int           `json:"max_tool_calls,omitempty"`
}

const (
	RunBudgetResourceTurns     = "max_turns"
	RunBudgetResourceWallClock = "max_wall_clock"
	RunBudgetResourceTokens    = "max_tokens"
	RunBudgetResourceToolCalls = "max_tool_calls"
)

// RunBudgetUsage 是終止當下已消耗的資源快照。WallClockMilliseconds 不直接使用
// time.Duration，避免 HTTP JSON 暴露難以理解的奈秒數值。
type RunBudgetUsage struct {
	Turns                 int   `json:"turns"`
	WallClockMilliseconds int64 `json:"wall_clock_milliseconds"`
	Tokens                int   `json:"tokens"`
	ToolCalls             int   `json:"tool_calls"`
}

type RunBudgetExceeded struct {
	Resource string         `json:"resource"`
	Limit    int64          `json:"limit"`
	Observed int64          `json:"observed"`
	Usage    RunBudgetUsage `json:"usage"`
}

type ToolApprovalDecisionType string

const (
	ToolApprovalApprove ToolApprovalDecisionType = "approve"
	ToolApprovalDeny    ToolApprovalDecisionType = "deny"
)

// ToolApprovalRequest 只保存讓人類判斷目前工具所需的資料。它會寫入 Run 與事件，
// 所以不得夾帶 Provider 憑證或其他後端內部狀態。
type ToolApprovalRequest struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	SessionID   string         `json:"session_id"`
	ToolCallID  string         `json:"tool_call_id"`
	ToolName    string         `json:"tool_name"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	RequestedAt time.Time      `json:"requested_at"`
}

type ToolApprovalDecisionInput struct {
	ApprovalID string                   `json:"approval_id"`
	Decision   ToolApprovalDecisionType `json:"decision"`
	Reason     string                   `json:"reason,omitempty"`
	Permanent  bool                     `json:"permanent,omitempty"`
}

type ToolApprovalDecision struct {
	ApprovalID string                   `json:"approval_id"`
	RunID      string                   `json:"run_id"`
	Decision   ToolApprovalDecisionType `json:"decision"`
	Reason     string                   `json:"reason,omitempty"`
	Permanent  bool                     `json:"permanent,omitempty"`
	DecidedAt  time.Time                `json:"decided_at"`
}

type RunResult struct {
	Message        Message            `json:"message"`
	BudgetExceeded *RunBudgetExceeded `json:"budget_exceeded,omitempty"`
	// Completion 說明這次的完成宣告是否經過追問、以及是否仍有未解決的工具失敗。
	// 沒有它時，「工具全部成功後完成」與「工具失敗後照樣宣稱完成」在 API 上無法區分。
	Completion *RunCompletion `json:"completion,omitempty"`
}

type RunError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type Run struct {
	ID              string               `json:"id"`
	AgentID         string               `json:"agent_id"`
	SessionID       string               `json:"session_id"`
	Status          RunStatus            `json:"status"`
	Input           string               `json:"input"`
	AttachmentIDs   []string             `json:"attachment_ids,omitempty"`
	ProviderID      string               `json:"provider_id,omitempty"`
	Model           string               `json:"model,omitempty"`
	IdempotencyKey  string               `json:"idempotency_key,omitempty"`
	Result          *RunResult           `json:"result,omitempty"`
	Error           *RunError            `json:"error,omitempty"`
	PendingApproval *ToolApprovalRequest `json:"pending_approval,omitempty"`
	Metadata        map[string]any       `json:"metadata,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	CompletedAt     *time.Time           `json:"completed_at,omitempty"`
}

type EngineEvent struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

type Event struct {
	SchemaVersion string         `json:"schema_version"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	AgentID       string         `json:"agent_id"`
	SessionID     string         `json:"session_id"`
	RunID         string         `json:"run_id"`
	Sequence      int64          `json:"sequence"`
	CreatedAt     time.Time      `json:"created_at"`
	Payload       map[string]any `json:"payload,omitempty"`
}

type ServiceStatus struct {
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	InstanceID string    `json:"instance_id"`
	StartedAt  time.Time `json:"started_at"`
	Ready      bool      `json:"ready"`
}
