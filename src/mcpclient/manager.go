package mcpclient

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

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
	available  []ToolInfo
	toolsDirty bool
	status     string
	lastError  string
	updatedAt  time.Time
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
			config:     config,
			tools:      map[string]resolvedTool{},
			toolsDirty: config.Enabled,
			status:     map[bool]string{true: "disconnected", false: "disabled"}[config.Enabled],
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

func (m *Manager) Definitions(ctx context.Context, _ domain.Session) ([]domain.ToolDefinition, error) {
	definitions := []domain.ToolDefinition{}
	for _, state := range m.states() {
		state.mu.RLock()
		enabled := state.config.Enabled
		connected := state.session != nil
		toolsDirty := state.toolsDirty
		state.mu.RUnlock()
		if !enabled {
			continue
		}
		// session 存在不代表工具目錄已同步：背景 Warm、工具變更通知或
		// 上一次 tools/list 暫時失敗，都會留下待刷新狀態。每次建立模型
		// 工具目錄前重試，避免把空的或過期的目錄交給模型。
		if !connected || toolsDirty {
			if err := m.ensureConnected(ctx, state, false); err != nil {
				m.logger.Warn("MCP tool catalog unavailable", "server_id", stateID(state), "error", err)
				continue
			}
		}
		state.mu.RLock()
		for _, tool := range state.tools {
			definitions = append(definitions, cloneDefinition(tool.definition))
		}
		state.mu.RUnlock()
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func (m *Manager) Execute(ctx context.Context, _ domain.Session, call domain.ToolCall, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	state, tool := m.route(call.Name)
	if state == nil {
		return failed(call, "MCP 工具不存在或尚未連線"), nil
	}
	if err := m.ensureConnected(ctx, state, false); err != nil {
		return failed(call, err.Error()), nil
	}
	state.mu.RLock()
	tool = state.tools[call.Name]
	session := state.session
	config := state.config
	state.mu.RUnlock()
	if session == nil || tool.remoteName == "" {
		return failed(call, "MCP 工具已不可用，請重新整理 MCP 連線"), nil
	}

	// CallTimeoutSeconds 是「沒有任何回應或進度更新」的容忍時間，不是硬性的
	// 總時長上限：長時間執行的 MCP 工作只要持續回報進度就能繼續，整體時間仍由
	// Run 的 wall-clock 預算控制。沒有進度回報的工具行為與原本一致。
	inactivity := time.Duration(config.CallTimeoutSeconds) * time.Second
	// 不直接把 timer callback 綁在 SDK 呼叫上：部分 MCP transport 在取消後
	// 仍可能卡在讀取或關閉流程，Manager 必須自己保證 Execute 能返回。
	ceiling := time.Duration(mcpMaxCallMultiplier) * inactivity
	if ceiling > mcpMaxCallDuration {
		ceiling = mcpMaxCallDuration
	}
	progressResets := make(chan struct{}, 1)
	progressCount := &atomic.Int64{}
	m.progressMu.Lock()
	m.progress[call.ID] = progressListener{sink: sink, extend: func() {
		progressCount.Add(1)
		select {
		case progressResets <- struct{}{}:
		default:
		}
	}}
	m.progressMu.Unlock()
	defer func() {
		m.progressMu.Lock()
		delete(m.progress, call.ID)
		m.progressMu.Unlock()
	}()
	startedAt := time.Now()
	stopHeartbeat := m.startWaitHeartbeat(ctx, call, config, sink, startedAt, progressCount)
	defer stopHeartbeat()
	for attempt := 1; attempt <= MCPCallRetryAttempts; attempt++ {
		params, paramsErr := m.callToolParams(call, tool)
		if paramsErr != nil {
			return failed(call, fmt.Sprintf("MCP %s 輸入無效：%v", config.DisplayName, paramsErr)), nil
		}
		result, err, outcome := callToolWithTimeout(ctx, inactivity, ceiling, progressResets, func(callCtx context.Context) (*mcp.CallToolResult, error) {
			return session.CallTool(callCtx, params)
		})
		if err == nil {
			state.mu.Lock()
			state.lastUsedAt = time.Now()
			state.mu.Unlock()
			return m.executionFromResult(call, config, result), nil
		}
		// 伺服器明確拒收（session 已失效、連線根本沒建立）代表工具沒有被執行過，
		// 重連後重送不會產生第二次副作用；這種情況對所有工具都重試一次。
		// 其他錯誤有可能是「已執行、但回應遺失」，維持只有 idempotent 工具才重試。
		if outcome == mcpCallStalled {
			message := fmt.Sprintf("MCP %s 呼叫已中止：%d 秒內沒有回應或進度更新（已等待 %s）",
				config.DisplayName, config.CallTimeoutSeconds, time.Since(startedAt).Round(time.Second))
			m.disconnectWithError(state, errors.New(message))
			return failed(call, message), nil
		}
		if outcome == mcpCallExceeded {
			message := fmt.Sprintf("MCP %s 呼叫已中止：單次呼叫超過上限 %s，期間收到 %d 次進度更新。請縮小工作範圍，或調整這個 Server 的呼叫逾時設定",
				config.DisplayName, ceiling.Round(time.Second), progressCount.Load())
			m.disconnectWithError(state, errors.New(message))
			return failed(call, message), nil
		}
		if ctx.Err() != nil {
			// Run 被取消時不拆掉健康的 MCP session，避免下一個 Run 又要重新
			// initialize；這次呼叫的 context 已由 callToolWithTimeout 收束。
			return failed(call, fmt.Sprintf("MCP %s 呼叫已取消", config.DisplayName)), nil
		}
		rejected := deliveryRejectedMCPError(err)
		if attempt == MCPCallRetryAttempts || (!rejected && (!tool.idempotent || !retryableMCPCallError(ctx, err))) {
			m.disconnectWithError(state, err)
			return failed(call, fmt.Sprintf("MCP %s 呼叫失敗：%v", config.DisplayName, err)), nil
		}

		m.logger.Warn("reconnecting and retrying MCP tool call",
			"server_id", config.ID,
			"tool_name", call.Name,
			"attempt", attempt+1,
			"max_attempts", MCPCallRetryAttempts,
			"server_rejected_delivery", rejected,
			"idempotent", tool.idempotent,
			"error", err,
		)
		m.disconnectWithError(state, err)
		if reconnectErr := m.ensureConnected(ctx, state, true); reconnectErr != nil {
			return failed(call, fmt.Sprintf("MCP %s 連線中斷且重新連線失敗：%v", config.DisplayName, reconnectErr)), nil
		}
		state.mu.RLock()
		tool = state.tools[call.Name]
		session = state.session
		state.mu.RUnlock()
		if session == nil || tool.remoteName == "" {
			return failed(call, "MCP 工具在重新連線後已不存在，請重新整理工具清單"), nil
		}
	}
	return failed(call, fmt.Sprintf("MCP %s 呼叫失敗", config.DisplayName)), nil
}

const (
	mcpInputResponsesArgument = "_mcp_input_responses"
	mcpRequestStateArgument   = "_mcp_request_state"
)

// callToolParams 將 MCP 多輪輸入的控制欄位與實際工具參數分開。一般工具
// 不會看到底線開頭的控制欄位；模型只需在收到 input_required 結果後，沿用
// 同一工具補上這兩個欄位即可繼續完成 MCP 呼叫。
// startWaitHeartbeat 在等待 MCP 回應期間定期回報已等待時間與收到的進度次數。
//
// 遠端工具沒有回應時，畫面上原本只有一個沒有說明的轉圈圈；把等待對象與秒數寫進
// Run 的事件流之後，卡住時可以直接看出是哪一個 MCP、等了多久、有沒有進度。
func (m *Manager) startWaitHeartbeat(
	ctx context.Context,
	call domain.ToolCall,
	config ServerConfig,
	sink ports.ToolUpdateSink,
	startedAt time.Time,
	progressCount *atomic.Int64,
) func() {
	if sink == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(mcpWaitHeartbeatIntervalForTest)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(startedAt).Round(time.Second)
				_ = sink(domain.ToolExecution{
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    fmt.Sprintf("等待 MCP %s 回應中（已 %s）", config.DisplayName, elapsed),
					Details: map[string]any{
						"phase":            "mcp_waiting",
						"mcp_server_id":    config.ID,
						"elapsed_seconds":  int(elapsed.Seconds()),
						"progress_updates": progressCount.Load(),
					},
				})
			}
		}
	}()
	return func() { close(done) }
}

func (m *Manager) callToolParams(call domain.ToolCall, tool resolvedTool) (*mcp.CallToolParams, error) {
	arguments := make(map[string]any, len(call.Arguments))
	for key, value := range call.Arguments {
		if key == mcpInputResponsesArgument || key == mcpRequestStateArgument {
			continue
		}
		arguments[key] = value
	}
	// 小型模型常把巢狀參數整個 JSON 字串化，遠端 Server 會直接以 schema 驗證失敗。
	// 依工具自己宣告的 schema 把該是 array／object 的字串解回結構；解不開就原樣送出。
	arguments = normalizeArgumentsForSchema(arguments, tool.definition.InputSchema)
	params := &mcp.CallToolParams{Name: tool.remoteName, Arguments: arguments}
	if value, exists := call.Arguments[mcpInputResponsesArgument]; exists {
		responses, err := decodeMCPInputResponses(value)
		if err != nil {
			return nil, err
		}
		params.InputResponses = responses
	}
	if value, exists := call.Arguments[mcpRequestStateArgument]; exists {
		requestState, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s 必須是字串", mcpRequestStateArgument)
		}
		params.RequestState = requestState
	}
	params.SetProgressToken(call.ID)
	return params, nil
}

func decodeMCPInputResponses(value any) (mcp.InputResponseMap, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s 無法編碼：%v", mcpInputResponsesArgument, err)
	}
	var rawValues map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &rawValues); err != nil || rawValues == nil {
		return nil, fmt.Errorf("%s 必須是 JSON object", mcpInputResponsesArgument)
	}
	responses := make(mcp.InputResponseMap, len(rawValues))
	for id, raw := range rawValues {
		var probe struct {
			Action json.RawMessage `json:"action"`
			Role   json.RawMessage `json:"role"`
			Roots  json.RawMessage `json:"roots"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("%s[%q] 必須是 JSON object：%v", mcpInputResponsesArgument, id, err)
		}
		switch {
		case len(probe.Action) > 0:
			var response mcp.ElicitResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode elicitation response %q: %v", id, err)
			}
			responses[id] = &response
		case len(probe.Role) > 0:
			var response mcp.CreateMessageResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode sampling response %q: %v", id, err)
			}
			responses[id] = &response
		case len(probe.Roots) > 0:
			var response mcp.ListRootsResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode roots response %q: %v", id, err)
			}
			responses[id] = &response
		default:
			return nil, fmt.Errorf("%s[%q] 缺少 action、role 或 roots", mcpInputResponsesArgument, id)
		}
	}
	return responses, nil
}

func retryableMCPCallError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"broken pipe", "connection reset", "connection refused", "connection aborted",
		"unexpected eof", "eof", "use of closed network connection", "transport is closing",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// deliveryRejectedMCPError 判斷「伺服器沒有收下這次呼叫」的錯誤。
//
// MCP session 過期或伺服器重啟後，Streamable HTTP 會以 session not found 拒絕
// 請求；連線根本沒建立起來也一樣。這兩種情況下遠端不可能執行過工具，因此重送
// 是安全的，不受 idempotent 宣告限制。
func deliveryRejectedMCPError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"session not found", "session expired", "unknown session", "invalid session",
		"missing session", "connection refused", "no such session",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isUnsupportedMCPMethod(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"method not found", "method not implemented", "unknown method", "not supported"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) Catalog(_ *domain.Session) []domain.ToolCatalogEntry {
	values := []domain.ToolCatalogEntry{}
	for _, state := range m.states() {
		state.mu.RLock()
		config := state.config
		status := state.status
		lastError := state.lastError
		for _, tool := range state.tools {
			entry := domain.ToolCatalogEntry{Definition: cloneDefinition(tool.definition), Allowed: config.Enabled, Available: config.Enabled && state.session != nil}
			if !config.Enabled {
				entry.UnavailableReason = "MCP Server 已停用"
			} else if !entry.Available {
				entry.UnavailableReason = strings.TrimSpace(lastError)
				if entry.UnavailableReason == "" {
					entry.UnavailableReason = "MCP Server 狀態：" + status
				}
			}
			values = append(values, entry)
		}
		state.mu.RUnlock()
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Definition.Name < values[j].Definition.Name })
	return values
}

func (m *Manager) Close() error {
	var first error
	for _, state := range m.states() {
		if err := m.closeState(state); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) ensureConnected(parent context.Context, state *serverState, forceList bool) error {
	state.connectMu.Lock()
	defer state.connectMu.Unlock()
	state.mu.RLock()
	config := state.config
	session := state.session
	toolsDirty := state.toolsDirty
	state.mu.RUnlock()
	if !config.Enabled {
		return nil
	}
	// 連線可能在閒置期間死掉（伺服器重啟、session 逾時、stdio 子程序結束）。
	// 在使用前就確認，讓呼叫端拿到的一定是活的 session，而不是等工具呼叫失敗
	// 才發現——後者對沒有宣告 idempotent 的工具等於直接失敗。
	if session != nil && !m.sessionUsable(parent, state, session) {
		m.closeState(state)
		session = nil
		toolsDirty = true
	}
	if session != nil && !forceList && !toolsDirty {
		return nil
	}
	if session == nil {
		m.setStatus(state, "connecting", "")
		ctx, cancel := context.WithTimeout(parent, time.Duration(config.StartupTimeoutSeconds)*time.Second)
		defer cancel()
		connected, err := m.connect(ctx, state, config)
		if err != nil {
			err = describeConnectError(config, err)
			m.setStatus(state, "error", err.Error())
			return err
		}
		done := make(chan struct{})
		state.mu.Lock()
		state.session = connected
		state.sessionDone = done
		state.lastUsedAt = time.Now()
		state.mu.Unlock()
		m.watchSession(state, connected, done)
	}
	catalogCtx, cancel := context.WithTimeout(parent, time.Duration(config.StartupTimeoutSeconds)*time.Second)
	defer cancel()
	return m.refreshTools(catalogCtx, state)
}

// sessionUsable 判斷既有連線是否還能直接使用。
//
// 先看連線是否已經結束（watchSession 會關閉 sessionDone），再對閒置一段時間的
// 連線送出 ping。ping 沒有副作用，所以這個檢查不會像重送工具呼叫那樣有重複執行
// 的風險。
func (m *Manager) sessionUsable(parent context.Context, state *serverState, session *mcp.ClientSession) bool {
	state.mu.RLock()
	done := state.sessionDone
	lastUsedAt := state.lastUsedAt
	state.mu.RUnlock()
	if done != nil {
		select {
		case <-done:
			return false
		default:
		}
	}
	if !lastUsedAt.IsZero() && time.Since(lastUsedAt) < mcpIdleProbeInterval {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, mcpProbeTimeout)
	defer cancel()
	if err := session.Ping(ctx, nil); err != nil {
		// 只有「連線確定已死」才重建。慢的伺服器 ping 逾時是常態，把它當成斷線會在
		// 每次閒置後多付一次 initialize 與 tools/list 的成本，反而讓呼叫更慢；真正
		// 失效的 session 會在送出工具呼叫時被伺服器拒收，那條路徑已經會重連並重送。
		if !deliveryRejectedMCPError(err) {
			m.logger.Debug("MCP session probe did not answer; keeping the session",
				"server_id", stateID(state), "error", err)
			return true
		}
		m.logger.Info("MCP session probe reported a dead session; reconnecting", "server_id", stateID(state), "error", err)
		return false
	}
	state.mu.Lock()
	state.lastUsedAt = time.Now()
	state.mu.Unlock()
	return true
}

// watchSession 在連線結束時立刻標記狀態，不必等到下一次工具呼叫才發現。
func (m *Manager) watchSession(state *serverState, session *mcp.ClientSession, done chan struct{}) {
	go func() {
		_ = session.Wait()
		close(done)
		state.mu.Lock()
		current := state.session
		if current == session {
			state.session = nil
			state.tools = map[string]resolvedTool{}
			state.toolsDirty = state.config.Enabled
			if state.status == "connected" {
				state.status = "disconnected"
			}
			state.updatedAt = time.Now().UTC()
		}
		state.mu.Unlock()
	}()
}

// describeConnectError 把常見的憑證錯誤講清楚。使用者最容易誤判的情況就是
// 「金鑰已儲存卻連不上」，錯誤訊息必須指出目前實際送出的驗證方式。
func describeConnectError(config ServerConfig, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unauthorized") || strings.Contains(message, "401"):
		return fmt.Errorf("MCP 伺服器拒絕憑證（HTTP 401 Unauthorized）；目前送出的驗證方式是 %s：%w", authModeLabel(config), err)
	case strings.Contains(message, "forbidden") || strings.Contains(message, "403"):
		return fmt.Errorf("MCP 伺服器拒絕存取（HTTP 403 Forbidden）；目前送出的驗證方式是 %s：%w", authModeLabel(config), err)
	case strings.Contains(message, "404") || strings.Contains(message, "not found") ||
		strings.Contains(message, "405") || strings.Contains(message, "method not allowed"):
		// 只填主機、少了端點路徑（例如 /mcp）是最常見的設定錯誤，錯誤訊息要指出來，
		// 否則很容易被誤判成憑證問題。
		return fmt.Errorf("MCP 端點沒有回應 MCP 協定（HTTP 404／405）；請確認 %s 是否需要加上端點路徑，例如 /mcp：%w", config.URL, err)
	}
	return err
}

// AuthMode 回傳這個設定實際會送出的驗證方式，供管理介面顯示。
func (c ServerConfig) AuthMode() string {
	switch {
	case c.Transport == TransportStdio:
		return AuthModeNone
	case strings.TrimSpace(c.APIKey) != "":
		return AuthModeBearer
	case strings.TrimSpace(c.Username) != "" || c.Password != "":
		return AuthModeBasic
	case len(c.Headers) > 0:
		return AuthModeHeaders
	default:
		return AuthModeNone
	}
}

func authModeLabel(config ServerConfig) string {
	switch config.AuthMode() {
	case AuthModeBearer:
		return "Bearer Token"
	case AuthModeBasic:
		return "Basic Auth（帳號 " + config.Username + "）"
	case AuthModeHeaders:
		return "自訂 HTTP Headers"
	default:
		return "不使用驗證"
	}
}

// connect 建立 MCP 連線。Streamable HTTP 的 subscriptions/listen 是選用能力，
// 實務上仍有 Server 宣告可連線但收到訂閱請求就關閉 session；工具目錄本來就會
// 在每次 Run 的待刷新狀態下明確呼叫 tools/list，因此不主動開啟這個訂閱，避免
// 非必要的背景串流讓整個 client 進入 closing。
func (m *Manager) connect(ctx context.Context, state *serverState, config ServerConfig) (*mcp.ClientSession, error) {
	transport, err := m.transport(config)
	if err != nil {
		return nil, err
	}
	watchTools := config.Transport != TransportStreamableHTTP
	connected, err := mcp.NewClient(
		&mcp.Implementation{Name: m.clientName, Title: "NR-Intern MCP Host", Version: m.clientVersion},
		m.clientOptions(state, watchTools),
	).Connect(ctx, transport, nil)
	return connected, err
}

func (m *Manager) clientOptions(state *serverState, watchTools bool) *mcp.ClientOptions {
	options := &mcp.ClientOptions{
		Logger:       m.logger.With("mcp_server_id", stateID(state)),
		Capabilities: &mcp.ClientCapabilities{},
		// Keep multi-round-trip manual: the next input comes from the Agent／user
		// loop, not from an implicit empty elicitation handler. The returned
		// input_required payload is exposed to the model and can be retried with
		// _mcp_input_responses and _mcp_request_state.
		MultiRoundTrip:              &mcp.MultiRoundTripOptions{Disabled: true},
		ProgressNotificationHandler: m.handleProgress,
		// 定期 ping 讓閒置連線不被伺服器或反向代理默默回收，連線真的斷掉時也
		// 會即時關閉，由 watchSession 標記狀態。
		KeepAlive:                 mcpKeepAliveInterval,
		KeepAliveFailureThreshold: mcpKeepAliveFailures,
	}
	if watchTools {
		options.ToolListChangedHandler = func(context.Context, *mcp.ToolListChangedRequest) {
			state.mu.Lock()
			state.toolsDirty = true
			state.updatedAt = time.Now().UTC()
			state.mu.Unlock()
		}
	}
	return options
}

func (m *Manager) refreshTools(ctx context.Context, state *serverState) error {
	state.mu.RLock()
	session := state.session
	config := state.config
	state.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("MCP %q 尚未連線", config.ID)
	}
	serverInstructions := ""
	if initialize := session.InitializeResult(); initialize != nil {
		serverInstructions = truncateUTF8(strings.TrimSpace(initialize.Instructions), maxServerInstructions)
		if initialize.Capabilities != nil && initialize.Capabilities.Tools == nil {
			// MCP Server 可以只提供 resources/prompts 而沒有 tools。這不是
			// 連線錯誤，工具目錄應保持為空並標記已同步。
			state.mu.Lock()
			state.tools = map[string]resolvedTool{}
			state.toolsDirty = false
			state.status = "connected"
			state.lastError = ""
			state.updatedAt = time.Now().UTC()
			state.mu.Unlock()
			return nil
		}
	}
	remoteTools := []*mcp.Tool{}
	params := &mcp.ListToolsParams{}
	for {
		result, err := session.ListTools(ctx, params)
		if err != nil {
			if isUnsupportedMCPMethod(err) {
				// Some older or narrowly-scoped servers omit tools/list even
				// though initialize succeeded. Treat the missing optional
				// capability as an empty catalog instead of losing the connection.
				remoteTools = nil
				break
			}
			// 保留 dirty 狀態，讓下一次 Run 能重新同步；同時清掉舊目錄，
			// 避免模型在清單已不可信時繼續呼叫過期工具。
			state.mu.Lock()
			state.tools = map[string]resolvedTool{}
			state.toolsDirty = state.config.Enabled
			state.mu.Unlock()
			m.setStatus(state, "error", err.Error())
			return err
		}
		remoteTools = append(remoteTools, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		params.Cursor = result.NextCursor
	}
	sort.SliceStable(remoteTools, func(i, j int) bool { return remoteTools[i].Name < remoteTools[j].Name })
	available := make([]ToolInfo, 0, len(remoteTools))
	for _, tool := range remoteTools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		title := strings.TrimSpace(tool.Title)
		if title == "" && tool.Annotations != nil {
			title = strings.TrimSpace(tool.Annotations.Title)
		}
		available = append(available, ToolInfo{Name: tool.Name, DisplayName: title})
	}
	resolved := make(map[string]resolvedTool, len(remoteTools))
	for _, tool := range remoteTools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		if !config.ToolEnabled(tool.Name) {
			continue
		}
		name := uniquePublicToolName(config.ID, tool.Name, resolved)
		label := strings.TrimSpace(tool.Title)
		if label == "" && tool.Annotations != nil {
			label = strings.TrimSpace(tool.Annotations.Title)
		}
		if label == "" {
			label = tool.Name
		}
		trusted := config.TrustsAnnotations()
		readOnly := trusted && tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		idempotent := trusted && tool.Annotations != nil && tool.Annotations.IdempotentHint
		resolved[name] = resolvedTool{remoteName: tool.Name, definition: domain.ToolDefinition{
			Name: name, Label: label, Category: "mcp", Description: strings.TrimSpace(tool.Description), InputSchema: schemaMap(tool.InputSchema),
			OutputSchema: optionalSchemaMap(tool.OutputSchema), ServerInstructions: serverInstructions,
			Capabilities: []string{"mcp", "mcp:" + config.ID}, ReadOnly: readOnly, RequiresPermission: true,
		}, idempotent: idempotent}
	}
	state.mu.Lock()
	state.tools = resolved
	state.available = available
	state.toolsDirty = false
	state.status = "connected"
	state.lastError = ""
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
	return nil
}

func (m *Manager) transport(config ServerConfig) (mcp.Transport, error) {
	switch config.Transport {
	case TransportStdio:
		command := exec.Command(config.Command, config.Args...)
		command.Dir = config.WorkDir
		command.Env = safeChildEnvironment(config.Environment)
		return &mcp.CommandTransport{Command: command, TerminateDuration: 4 * time.Second}, nil
	case TransportStreamableHTTP:
		client := newMCPHTTPClient(config)
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: 2, DisableStandaloneSSE: true}, nil
	case TransportSSE:
		client := newMCPHTTPClient(config)
		return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}, nil
	default:
		return nil, fmt.Errorf("不支援的 MCP transport %q", config.Transport)
	}
}

func (m *Manager) handleProgress(_ context.Context, request *mcp.ProgressNotificationClientRequest) {
	if request == nil || request.Params == nil {
		return
	}
	token := fmt.Sprint(request.Params.ProgressToken)
	m.progressMu.RLock()
	listener := m.progress[token]
	m.progressMu.RUnlock()
	if listener.extend != nil {
		listener.extend()
	}
	sink := listener.sink
	if sink == nil {
		return
	}
	_ = sink(domain.ToolExecution{Content: request.Params.Message, Details: map[string]any{
		"mcp_progress": request.Params.Progress,
		"mcp_total":    request.Params.Total,
	}})
}

type mcpCallResult struct {
	result *mcp.CallToolResult
	err    error
}

// mcpWaitHeartbeatIntervalForTest 讓測試縮短等待回報間隔；正式執行時等於
// mcpWaitHeartbeatInterval。
var mcpWaitHeartbeatIntervalForTest = mcpWaitHeartbeatInterval

type mcpCallOutcome int

const (
	mcpCallCompleted mcpCallOutcome = iota
	// mcpCallStalled 是「閒置視窗內完全沒有回應或進度」。
	mcpCallStalled
	// mcpCallExceeded 是「有進度但總時間超過單次呼叫上限」。
	mcpCallExceeded
	mcpCallCanceled
)

// callToolWithTimeout 將底層 MCP 呼叫與等待邏輯分開。SDK 通常會遵守 context，
// 但第三方 transport 或自訂 RoundTripper 可能在取消後仍不返回；用獨立 goroutine
// 與有緩衝的結果 channel，才能讓上層 Run 在逾時時立即收到失敗結果。
func callToolWithTimeout(
	parent context.Context,
	inactivity time.Duration,
	ceiling time.Duration,
	progressResets <-chan struct{},
	invoke func(context.Context) (*mcp.CallToolResult, error),
) (*mcp.CallToolResult, error, mcpCallOutcome) {
	callCtx, cancel := context.WithCancel(parent)
	defer cancel()

	resultCh := make(chan mcpCallResult, 1)
	go func() {
		result, err := invoke(callCtx)
		resultCh <- mcpCallResult{result: result, err: err}
	}()

	timer := time.NewTimer(inactivity)
	defer stopTimer(timer)
	// limit 是不受進度通知影響的絕對上限，確保單次呼叫一定會結束。
	limit := time.NewTimer(ceiling)
	defer stopTimer(limit)
	for {
		select {
		case response := <-resultCh:
			return response.result, response.err, mcpCallCompleted
		case <-timer.C:
			// 先取消底層呼叫，讓遵守 context 的實作儘快收束；不等待它，
			// 因為不遵守取消的實作正是這個保護邊界要處理的對象。
			cancel()
			return nil, context.DeadlineExceeded, mcpCallStalled
		case <-limit.C:
			cancel()
			return nil, context.DeadlineExceeded, mcpCallExceeded
		case <-parent.Done():
			cancel()
			return nil, parent.Err(), mcpCallCanceled
		case <-progressResets:
			stopTimer(timer)
			timer.Reset(inactivity)
		}
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (m *Manager) executionFromResult(call domain.ToolCall, config ServerConfig, result *mcp.CallToolResult) domain.ToolExecution {
	execution := domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Details: map[string]any{
		"mcp_server_id": config.ID, "mcp_transport": config.Transport,
	}}
	if result == nil {
		execution.Content = "MCP Server 未回傳結果"
		execution.IsError = true
		return execution
	}
	execution.IsError = result.IsError
	if result.StructuredContent != nil {
		execution.Details["structured_content"] = result.StructuredContent
	}
	if result.NeedsInput() {
		requests, marshalErr := json.Marshal(result.InputRequests)
		if marshalErr == nil {
			execution.Details["mcp_input_requests"] = result.InputRequests
		}
		if strings.TrimSpace(result.RequestState) != "" {
			execution.Details["mcp_request_state"] = result.RequestState
		}
		execution.Content = "MCP 工具需要額外輸入。請依下列請求補齊資料，並以同一工具重試；回傳參數請放入 " + mcpInputResponsesArgument + "，並原樣帶回 " + mcpRequestStateArgument + "。"
		if len(requests) > 0 {
			execution.Content += "\nMCP 輸入請求：" + string(requests)
		}
		execution.IsError = true
		execution.Details["mcp_input_required"] = true
		return execution
	}
	parts := make([]string, 0, len(result.Content))
	contentTypes := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch value := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, value.Text)
			contentTypes = append(contentTypes, "text")
		case *mcp.ResourceLink:
			parts = append(parts, fmt.Sprintf("MCP Resource：%s (%s)", value.Name, value.URI))
			contentTypes = append(contentTypes, "resource_link")
		case *mcp.EmbeddedResource:
			if value.Resource != nil && value.Resource.Text != "" {
				parts = append(parts, value.Resource.Text)
			} else if value.Resource != nil {
				parts = append(parts, fmt.Sprintf("MCP Embedded Resource：%s", value.Resource.URI))
			}
			contentTypes = append(contentTypes, "embedded_resource")
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[MCP 圖片：%s，%d bytes]", value.MIMEType, len(value.Data)))
			contentTypes = append(contentTypes, "image")
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[MCP 音訊：%s，%d bytes]", value.MIMEType, len(value.Data)))
			contentTypes = append(contentTypes, "audio")
		default:
			encoded, _ := json.Marshal(content)
			parts = append(parts, string(encoded))
			contentTypes = append(contentTypes, "unknown")
		}
	}
	if len(contentTypes) > 0 {
		execution.Details["mcp_content_types"] = contentTypes
	}
	execution.Content = strings.TrimSpace(strings.Join(parts, "\n\n"))
	if result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		// 宣告 output schema 的 MCP Server 會同時回傳 structuredContent 與一段內容
		// 相同的 text block（規格為了相容舊 Client 的要求）。兩份都塞進工具結果，
		// 等於這筆資料在 transcript 裡存兩次、之後每一輪都重讀兩次；只有在文字區塊
		// 沒有涵蓋同一份資料時才附加。
		if len(encoded) > 0 && string(encoded) != "null" && !jsonAlreadyPresent(parts, encoded) {
			structured := "MCP 結構化結果：" + string(encoded)
			if execution.Content == "" {
				execution.Content = structured
			} else {
				execution.Content += "\n\n" + structured
			}
		}
	}
	if execution.Content == "" {
		execution.Content = map[bool]string{true: "MCP 工具執行失敗，但未提供錯誤內容。", false: "MCP 工具已完成。"}[execution.IsError]
	}
	execution.Content = truncateUTF8WithNote(execution.Content, m.maxOutputBytes,
		"\n…（這次的 MCP 輸出過長已截斷。這是 Harness 的內部狀態，不要轉述給使用者，"+
			"也不要請使用者自己去查記錄；需要完整資料時縮小查詢範圍或加上篩選條件重新呼叫工具。）")
	return execution
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

func cloneDefinition(value domain.ToolDefinition) domain.ToolDefinition {
	value.InputSchema = schemaMap(value.InputSchema)
	value.OutputSchema = optionalSchemaMap(value.OutputSchema)
	value.Capabilities = cloneStrings(value.Capabilities)
	value.Platforms = cloneStrings(value.Platforms)
	return value
}

func optionalSchemaMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	result := schemaMap(value)
	if len(result) == 0 {
		return nil
	}
	return result
}

func schemaMap(value any) map[string]any {
	if direct, ok := value.(map[string]any); ok {
		encoded, _ := json.Marshal(direct)
		var result map[string]any
		_ = json.Unmarshal(encoded, &result)
		return result
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil || result == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return result
}

func uniquePublicToolName(serverID, remoteName string, existing map[string]resolvedTool) string {
	base := "mcp__" + sanitizeName(serverID) + "__" + sanitizeName(remoteName)
	name := limitName(base, remoteName)
	if _, exists := existing[name]; !exists {
		return name
	}
	sum := sha256.Sum256([]byte(serverID + "\x00" + remoteName))
	suffix := "__" + hex.EncodeToString(sum[:4])
	if len(base)+len(suffix) <= maxPublicToolNameLength {
		return base + suffix
	}
	return base[:maxPublicToolNameLength-len(suffix)] + suffix
}

func sanitizeName(value string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(value) {
		valid := unicode.IsLetter(r) && r <= unicode.MaxASCII || unicode.IsDigit(r) || r == '_' || r == '-'
		if valid {
			builder.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "tool"
	}
	return result
}

func limitName(base, hashInput string) string {
	if len(base) <= maxPublicToolNameLength {
		return base
	}
	sum := sha256.Sum256([]byte(hashInput))
	suffix := "__" + hex.EncodeToString(sum[:4])
	return base[:maxPublicToolNameLength-len(suffix)] + suffix
}

func safeChildEnvironment(extra map[string]string) []string {
	allowed := []string{"HOME", "LANG", "LC_ALL", "LOGNAME", "PATH", "SHELL", "TMPDIR", "USER"}
	values := make(map[string]string, len(allowed)+len(extra))
	for _, key := range allowed {
		if value, exists := os.LookupEnv(key); exists {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

type headerTransport struct {
	base     http.RoundTripper
	apiKey   string
	username string
	password string
	headers  map[string]string
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	// Apply credentials after custom headers so an explicitly configured key is
	// also authoritative for the initialize request and every later MCP call.
	if t.apiKey != "" {
		clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	} else if t.username != "" || t.password != "" {
		clone.SetBasicAuth(t.username, t.password)
	}
	return t.base.RoundTrip(clone)
}

func newMCPHTTPClient(config ServerConfig) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return &http.Client{
		Transport: &headerTransport{
			base:     base,
			apiKey:   config.APIKey,
			username: config.Username,
			password: config.Password,
			headers:  config.Headers,
		},
		CheckRedirect: preserveMCPPostRedirect,
	}
}

// preserveMCPPostRedirect prevents a 301/302 endpoint-slash redirect from
// changing MCP's JSON-RPC POST into GET and dropping the initialize body.
// Redirects to another host are rejected so credentials cannot leave the
// configured MCP endpoint.
func preserveMCPPostRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	if previous.URL == nil || request.URL == nil || !safeMCPRedirect(previous.URL, request.URL) {
		return fmt.Errorf("MCP HTTP redirect changed endpoint origin")
	}
	if previous.Method != http.MethodPost || request.Method != http.MethodGet {
		return nil
	}
	if previous.GetBody == nil {
		return fmt.Errorf("MCP HTTP redirect cannot replay request body")
	}
	body, err := previous.GetBody()
	if err != nil {
		return fmt.Errorf("MCP HTTP redirect replay body: %w", err)
	}
	request.Method = previous.Method
	request.Body = body
	request.GetBody = previous.GetBody
	request.ContentLength = previous.ContentLength
	request.Header = previous.Header.Clone()
	return nil
}

func safeMCPRedirect(from, to *url.URL) bool {
	if !strings.EqualFold(from.Host, to.Host) {
		return false
	}
	if strings.EqualFold(from.Scheme, to.Scheme) {
		return true
	}
	return strings.EqualFold(from.Scheme, "http") && strings.EqualFold(to.Scheme, "https")
}

// jsonAlreadyPresent 判斷某個文字區塊是否就是同一份 JSON 資料。
// 比對解碼後的值而不是字串，因為欄位順序與空白都可能不同。
func jsonAlreadyPresent(parts []string, encoded []byte) bool {
	var structured any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(part), &value); err != nil {
			continue
		}
		if reflect.DeepEqual(value, structured) {
			return true
		}
	}
	return false
}

func failed(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: message, IsError: true}
}

func truncateUTF8(value string, limit int) string {
	return truncateUTF8WithNote(value, limit, "\n…（MCP 輸出已截斷）")
}

// truncateUTF8WithNote 讓工具結果帶上「下一步該做什麼」，Server instructions
// 則維持單純的截斷說明——對 instructions 講「重新查詢」是沒有意義的。
func truncateUTF8WithNote(value string, limit int, note string) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	data := []byte(value[:limit])
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data) + note
}
