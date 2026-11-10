package mcpclient

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxPublicToolNameLength = 64
	// MCPCallRetryAttempts 只套用在工具宣告為 idempotent 的連線層錯誤。
	// 未宣告 idempotent 的工具不自動重試，避免網路在 Server 已完成副作用後
	// 中斷回應，重送造成重複寫入。
	MCPCallRetryAttempts  = 2
	maxServerInstructions = 8 * 1024
	// mcpKeepAliveInterval 讓 SDK 定期 ping。閒置的 HTTP session 常被伺服器或
	// 中間的反向代理回收，沒有 keepalive 時只有下一次工具呼叫才會發現。
	mcpKeepAliveInterval = 30 * time.Second
	// mcpKeepAliveFailures 容忍一次暫時性失敗，避免網路抖動就拆掉可用連線。
	mcpKeepAliveFailures = 2
	// mcpIdleProbeInterval 之後的第一次使用會先 ping 確認連線仍活著。
	// ping 沒有副作用，因此這個檢查對所有工具都安全。
	mcpIdleProbeInterval = 20 * time.Second
	mcpProbeTimeout      = 5 * time.Second
	// mcpMaxCallMultiplier 是單次呼叫的絕對上限倍數。進度通知可以延長閒置視窗，
	// 但不能讓一次呼叫無限期執行：Server 一直送進度卻永遠不回結果時仍要收斂，
	// 否則畫面上就是一個永遠轉不完的圈。
	mcpMaxCallMultiplier = 4
	mcpMaxCallDuration   = 24 * time.Hour
	// mcpWaitHeartbeatInterval 決定等待中的狀態回報頻率。沒有這個訊息時，使用者
	// 無法分辨是模型在想、MCP 沒回應，還是卡在別的地方。
	mcpWaitHeartbeatInterval = 15 * time.Second
)

const (
	AuthModeNone    = "none"
	AuthModeBearer  = "bearer"
	AuthModeBasic   = "basic"
	AuthModeHeaders = "headers"
)

type ToolInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

type ServerStatus struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	Transport   string `json:"transport"`
	Status      string `json:"status"`
	// AvailableTools 是 Server 提供的完整工具清單；Tools 只含本次公開給模型的工具。
	AvailableTools []ToolInfo `json:"available_tools,omitempty"`
	AuthMode       string     `json:"auth_mode"`
	Error          string     `json:"error,omitempty"`
	ToolCount      int        `json:"tool_count"`
	Tools          []ToolInfo `json:"tools,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at,omitempty"`
}

type resolvedTool struct {
	definition domain.ToolDefinition
	remoteName string
	idempotent bool
}

type serverState struct {
	mu        sync.RWMutex
	connectMu sync.Mutex
	config    ServerConfig
	session   *mcp.ClientSession
	// sessionDone 在連線結束時關閉，讓下一次呼叫在送出前就知道 session 已死。
	sessionDone chan struct{}
	lastUsedAt  time.Time
	tools       map[string]resolvedTool
	// available 是 Server 回報的完整工具清單（含未公開的），供管理介面挑選。
	available     []ToolInfo
	toolsDirty    bool
	status        string
	lastError     string
	updatedAt     time.Time
	contractScope string
}

type Manager struct {
	mu             sync.RWMutex
	servers        map[string]*serverState
	logger         *slog.Logger
	clientName     string
	clientVersion  string
	maxOutputBytes int
	progressMu     sync.RWMutex
	progress       map[string]progressListener
	continuationMu sync.Mutex
	continuations  map[string]*toolContinuation
}

// progressListener 把工具的進度事件同時交給 UI 與呼叫的閒置計時器：
// 只要 MCP Server 還在回報進度，這次呼叫就不算停滯。
type progressListener struct {
	sink   ports.ToolUpdateSink
	extend func()
}

func New(configs []ServerConfig, clientName, clientVersion string, maxOutputBytes int, logger *slog.Logger) (*Manager, error) {
	manager := &Manager{
		servers:        map[string]*serverState{},
		logger:         logging.Or(logger),
		clientName:     strings.TrimSpace(clientName),
		clientVersion:  strings.TrimSpace(clientVersion),
		maxOutputBytes: maxOutputBytes,
		progress:       map[string]progressListener{},
	}
	if manager.clientName == "" {
		manager.clientName = "nr-intern"
	}
	if manager.clientVersion == "" {
		manager.clientVersion = "dev"
	}
	if manager.maxOutputBytes <= 0 {
		manager.maxOutputBytes = 512 * 1024
	}
	if err := manager.Replace(configs); err != nil {
		return nil, err
	}
	return manager, nil
}

// Replace 原子替換 MCP Server 設定。未變更的連線會保留，變更或刪除的連線會關閉。
func (m *Manager) Replace(configs []ServerConfig) error {
	next := make(map[string]ServerConfig, len(configs))
	for _, value := range configs {
		normalized, err := value.Normalize()
		if err != nil {
			return err
		}
		if _, exists := next[normalized.ID]; exists {
			return fmt.Errorf("重複的 MCP ID %q", normalized.ID)
		}
		next[normalized.ID] = normalized
	}

	m.mu.Lock()
	old := m.servers
	updated := make(map[string]*serverState, len(next))
	for id, config := range next {
		if existing := old[id]; existing != nil {
			existing.mu.RLock()
			same := reflect.DeepEqual(existing.config, config)
			existing.mu.RUnlock()
			if same {
				updated[id] = existing
				delete(old, id)
				continue
			}
		}
		updated[id] = &serverState{
			config:        config,
			contractScope: domain.NewID("mcp_contract"),
			tools:         map[string]resolvedTool{},
			toolsDirty:    config.Enabled,
			status:        map[bool]string{true: "disconnected", false: "disabled"}[config.Enabled],
		}
	}
	m.servers = updated
	m.mu.Unlock()

	for _, state := range old {
		m.closeState(state)
	}
	return nil
}

func (m *Manager) Configs() []ServerConfig {
	states := m.states()
	values := make([]ServerConfig, 0, len(states))
	for _, state := range states {
		state.mu.RLock()
		values = append(values, cloneConfig(state.config))
		state.mu.RUnlock()
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}

// Warm 在背景建立已啟用的 MCP 連線，不阻塞 APP 顯示。
func (m *Manager) Warm(ctx context.Context) {
	for _, state := range m.states() {
		state.mu.RLock()
		enabled := state.config.Enabled
		state.mu.RUnlock()
		if !enabled {
			continue
		}
		go func(state *serverState) {
			if err := m.ensureConnected(ctx, state, true); err != nil {
				m.logger.Warn("MCP warm connection failed", "server_id", stateID(state), "error", err)
			}
		}(state)
	}
}

func (m *Manager) Refresh(ctx context.Context, id string) (ServerStatus, error) {
	state := m.state(id)
	if state == nil {
		return ServerStatus{}, fmt.Errorf("%w: MCP %q", domain.ErrNotFound, id)
	}
	m.closeState(state)
	state.mu.RLock()
	enabled := state.config.Enabled
	state.mu.RUnlock()
	if enabled {
		if err := m.ensureConnected(ctx, state, true); err != nil {
			return m.statusOf(state), err
		}
	}
	return m.statusOf(state), nil
}

func (m *Manager) Statuses() []ServerStatus {
	states := m.states()
	values := make([]ServerStatus, 0, len(states))
	for _, state := range states {
		values = append(values, m.statusOf(state))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}

func (m *Manager) Close() error {
	m.continuationMu.Lock()
	m.continuations = nil
	m.continuationMu.Unlock()
	var first error
	for _, state := range m.states() {
		if err := m.closeState(state); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) route(publicName string) (*serverState, resolvedTool) {
	for _, state := range m.states() {
		state.mu.RLock()
		tool, exists := state.tools[publicName]
		state.mu.RUnlock()
		if exists {
			return state, tool
		}
	}
	return nil, resolvedTool{}
}

func (m *Manager) states() []*serverState {
	m.mu.RLock()
	values := make([]*serverState, 0, len(m.servers))
	for _, state := range m.servers {
		values = append(values, state)
	}
	m.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return stateID(values[i]) < stateID(values[j]) })
	return values
}

func (m *Manager) state(id string) *serverState {
	m.mu.RLock()
	state := m.servers[strings.TrimSpace(id)]
	m.mu.RUnlock()
	return state
}

func (m *Manager) statusOf(state *serverState) ServerStatus {
	state.mu.RLock()
	defer state.mu.RUnlock()
	value := ServerStatus{ID: state.config.ID, DisplayName: state.config.DisplayName, Enabled: state.config.Enabled, Transport: state.config.Transport, Status: state.status, AuthMode: state.config.AuthMode(), Error: state.lastError, UpdatedAt: state.updatedAt}
	for _, tool := range state.tools {
		value.Tools = append(value.Tools, ToolInfo{Name: tool.definition.Name, DisplayName: tool.definition.Label})
	}
	sort.Slice(value.Tools, func(i, j int) bool { return value.Tools[i].Name < value.Tools[j].Name })
	value.ToolCount = len(value.Tools)
	value.AvailableTools = append([]ToolInfo(nil), state.available...)
	return value
}

func (m *Manager) closeState(state *serverState) error {
	state.connectMu.Lock()
	defer state.connectMu.Unlock()
	return m.closeStateLocked(state)
}

// 呼叫端已持有 connectMu，避免重連檢查再次鎖住同一把非重入鎖。
func (m *Manager) closeStateLocked(state *serverState) error {
	state.mu.Lock()
	session := state.session
	state.session = nil
	state.sessionDone = nil
	state.lastUsedAt = time.Time{}
	state.tools = map[string]resolvedTool{}
	state.toolsDirty = state.config.Enabled
	state.status = map[bool]string{true: "disconnected", false: "disabled"}[state.config.Enabled]
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
	if session != nil {
		m.closeSessionAsync(state, session)
	}
	return nil
}

func (m *Manager) disconnectWithError(state *serverState, err error) {
	state.connectMu.Lock()
	state.mu.Lock()
	session := state.session
	state.session = nil
	state.sessionDone = nil
	state.lastUsedAt = time.Time{}
	state.toolsDirty = state.config.Enabled
	state.lastError = err.Error()
	state.status = "error"
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
	state.connectMu.Unlock()
	m.closeSessionAsync(state, session)
}

// closeSessionAsync 不讓 MCP Server 的 DELETE／stdio 收尾阻塞重連與 UI。
// 有些遠端端點在工具逾時後也不回應 DELETE；此時保留背景清理即可，不能讓
// 已經失敗的 Run 再次卡在關閉舊 session。
func (m *Manager) closeSessionAsync(state *serverState, session *mcp.ClientSession) {
	if session == nil {
		return
	}
	serverID := stateID(state)
	go func() {
		if err := session.Close(); err != nil {
			m.logger.Debug("MCP session close failed", "server_id", serverID, "error", err)
		}
	}()
}

func (m *Manager) setStatus(state *serverState, status, detail string) {
	state.mu.Lock()
	state.status = status
	state.lastError = strings.TrimSpace(detail)
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
}

func stateID(state *serverState) string {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.config.ID
}

func cloneConfig(value ServerConfig) ServerConfig {
	value.Args = cloneStrings(value.Args)
	value.Environment = cleanMap(value.Environment)
	value.Headers = cleanMap(value.Headers)
	return value
}
