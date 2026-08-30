package domain

import "time"

const (
	// APIVersion 是 HTTP API 契約版本。只要破壞既有欄位或狀態語意，就必須升版，
	// 讓桌面 UI 能在啟動時明確提示，而不是等到某個端點回 404 才讓使用者猜原因。
	APIVersion         = "1.0"
	EventSchemaVersion = "1.0"
)

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
	Position              int            `json:"position"`
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
	Position          *int    `json:"position,omitempty"`
}

type ReorderSessionsInput struct {
	WorkspaceID string   `json:"workspace_id"`
	ProjectID   string   `json:"project_id,omitempty"`
	SessionIDs  []string `json:"session_ids"`
}

type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Instructions 是 Project 的職務說明，接在所屬 Workspace 的說明之後注入。
	Instructions string    `json:"instructions,omitempty"`
	SandboxRoots []string  `json:"sandbox_roots,omitempty"`
	Position     int       `json:"position"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateProjectInput struct {
	Name         string   `json:"name"`
	WorkspaceID  string   `json:"workspace_id"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	SandboxRoots []string `json:"sandbox_roots,omitempty"`
}

type UpdateProjectInput struct {
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Instructions *string   `json:"instructions,omitempty"`
	SandboxRoots *[]string `json:"sandbox_roots,omitempty"`
	Position     *int      `json:"position,omitempty"`
}

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Instructions 是 Workspace 的職務說明：每次 Run 都會注入提示，
	// 讓使用者只寫一次就套用到底下所有對話，不必每次重述工作規則。
	Instructions      string    `json:"instructions,omitempty"`
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
	Instructions      string   `json:"instructions,omitempty"`
	ProviderIDs       []string `json:"provider_ids"`
	DefaultProviderID string   `json:"default_provider_id"`
	Model             string   `json:"model,omitempty"`
}

type UpdateWorkspaceInput struct {
	Name              *string   `json:"name,omitempty"`
	Description       *string   `json:"description,omitempty"`
	Instructions      *string   `json:"instructions,omitempty"`
	ProviderIDs       *[]string `json:"provider_ids,omitempty"`
	DefaultProviderID *string   `json:"default_provider_id,omitempty"`
	Model             *string   `json:"model,omitempty"`
	Position          *int      `json:"position,omitempty"`
}

type ProviderDescriptor struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint"`
	DefaultModel string `json:"default_model"`
	Streaming    bool   `json:"streaming"`
	HasAPIKey    bool   `json:"has_api_key"`
	// SupportsNativeToolCalls 表示 Provider 能可靠地以協定原生欄位回傳工具呼叫。
	// Harness 會優先採用原生模式；未宣告時仍可使用 instruction 相容模式。
	SupportsNativeToolCalls bool `json:"supports_native_tool_calls"`
	// ContextWindow 與 MaxOutputTokens 是該 Provider 預設模型的宣告限制；0 代表未宣告。
	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

type RunInput struct {
	RunID         string   `json:"-"`
	SessionID     string   `json:"session_id"`
	UserInput     string   `json:"input"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	ProviderID    string   `json:"provider_id,omitempty"`
	Model         string   `json:"model,omitempty"`
	// SandboxRoots 只由後端內部流程填入（目前是排程執行器），JSON 不解析。
	// 這些路徑在寫入來源實體時就已驗證，HTTP 呼叫端無法藉此擴大沙箱。
	SandboxRoots   []string       `json:"-"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	IdempotencyKey string         `json:"-"`
}

type RunStatus string

const (
	RunStatusQueued          RunStatus = "queued"
	RunStatusRunning         RunStatus = "running"
	RunStatusPaused          RunStatus = "paused"
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

// Notification 是顯示在通知中心的輕量事件摘要。它不保存完整對話內容，
// 只保留讓使用者知道下一步該做什麼所需的資訊。
type Notification struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Level     string         `json:"level"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Read      bool           `json:"read"`
	DedupeKey string         `json:"-"`
	RunID     string         `json:"run_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// SearchResult 是全域搜尋回傳的短摘要，不直接回傳整份訊息或檔案內容。
type SearchResult struct {
	Kind        string    `json:"kind"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Snippet     string    `json:"snippet"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type PermissionCenter struct {
	Policy              PermissionPolicy     `json:"policy"`
	Tools               []ToolPermissionInfo `json:"tools"`
	WaitingApprovalRuns int                  `json:"waiting_approval_runs"`
}

type ToolPermissionInfo struct {
	Name               string `json:"name"`
	Permission         string `json:"permission"`
	RequiresPermission bool   `json:"requires_permission"`
	ReadOnly           bool   `json:"read_only"`
	Available          bool   `json:"available"`
}

type RestoreResult struct {
	RestartRequired bool     `json:"restart_required"`
	Restored        []string `json:"restored"`
	Excluded        []string `json:"excluded"`
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
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	SessionID      string    `json:"session_id"`
	Status         RunStatus `json:"status"`
	Input          string    `json:"input"`
	AttachmentIDs  []string  `json:"attachment_ids,omitempty"`
	ProviderID     string    `json:"provider_id,omitempty"`
	Model          string    `json:"model,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	// IdempotencyFingerprint 用來拒絕同一 Idempotency-Key 搭配不同內容的重送，
	// 避免網路重試把另一個工作誤認成原本的 Run。
	IdempotencyFingerprint string               `json:"idempotency_fingerprint,omitempty"`
	Result                 *RunResult           `json:"result,omitempty"`
	Error                  *RunError            `json:"error,omitempty"`
	PendingApproval        *ToolApprovalRequest `json:"pending_approval,omitempty"`
	Metadata               map[string]any       `json:"metadata,omitempty"`
	CreatedAt              time.Time            `json:"created_at"`
	StartedAt              *time.Time           `json:"started_at,omitempty"`
	CompletedAt            *time.Time           `json:"completed_at,omitempty"`
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
	Name               string    `json:"name"`
	Version            string    `json:"version"`
	APIVersion         string    `json:"api_version"`
	EventSchemaVersion string    `json:"event_schema_version"`
	Capabilities       []string  `json:"capabilities,omitempty"`
	InstanceID         string    `json:"instance_id"`
	StartedAt          time.Time `json:"started_at"`
	Ready              bool      `json:"ready"`
}
