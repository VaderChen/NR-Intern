package mcpclient

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxPublicToolNameLength = 64

type ToolInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

type ServerStatus struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	Enabled     bool       `json:"enabled"`
	Transport   string     `json:"transport"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	ToolCount   int        `json:"tool_count"`
	Tools       []ToolInfo `json:"tools,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
}

type resolvedTool struct {
	definition domain.ToolDefinition
	remoteName string
}

type serverState struct {
	mu        sync.RWMutex
	connectMu sync.Mutex
	config    ServerConfig
	session   *mcp.ClientSession
	tools     map[string]resolvedTool
	status    string
	lastError string
	updatedAt time.Time
}

type Manager struct {
	mu             sync.RWMutex
	servers        map[string]*serverState
	logger         *slog.Logger
	clientName     string
	clientVersion  string
	maxOutputBytes int
	progressMu     sync.RWMutex
	progress       map[string]ports.ToolUpdateSink
}

func New(configs []ServerConfig, clientName, clientVersion string, maxOutputBytes int, logger *slog.Logger) (*Manager, error) {
	manager := &Manager{
		servers:        map[string]*serverState{},
		logger:         logging.Or(logger),
		clientName:     strings.TrimSpace(clientName),
		clientVersion:  strings.TrimSpace(clientVersion),
		maxOutputBytes: maxOutputBytes,
		progress:       map[string]ports.ToolUpdateSink{},
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
		updated[id] = &serverState{config: config, tools: map[string]resolvedTool{}, status: map[bool]string{true: "disconnected", false: "disabled"}[config.Enabled]}
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
		state.mu.RUnlock()
		if !enabled {
			continue
		}
		if !connected {
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

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.CallTimeoutSeconds)*time.Second)
	defer cancel()
	if sink != nil {
		m.progressMu.Lock()
		m.progress[call.ID] = sink
		m.progressMu.Unlock()
		defer func() {
			m.progressMu.Lock()
			delete(m.progress, call.ID)
			m.progressMu.Unlock()
		}()
	}
	params := &mcp.CallToolParams{Name: tool.remoteName, Arguments: call.Arguments}
	params.SetProgressToken(call.ID)
	result, err := session.CallTool(callCtx, params)
	if err != nil {
		m.disconnectWithError(state, err)
		return failed(call, fmt.Sprintf("MCP %s 呼叫失敗：%v", config.DisplayName, err)), nil
	}
	return m.executionFromResult(call, config, result), nil
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
	hasTools := len(state.tools) > 0
	state.mu.RUnlock()
	if !config.Enabled {
		return nil
	}
	if session != nil && (hasTools || !forceList) {
		return nil
	}
	if session == nil {
		m.setStatus(state, "connecting", "")
		ctx, cancel := context.WithTimeout(parent, time.Duration(config.StartupTimeoutSeconds)*time.Second)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: m.clientName, Title: "NR-Intern MCP Host", Version: m.clientVersion}, &mcp.ClientOptions{
			Logger:         m.logger.With("mcp_server_id", config.ID),
			Capabilities:   &mcp.ClientCapabilities{},
			MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
			ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
				go func() {
					refreshCtx, refreshCancel := context.WithTimeout(context.Background(), time.Duration(config.StartupTimeoutSeconds)*time.Second)
					defer refreshCancel()
					_ = m.refreshTools(refreshCtx, state)
				}()
			},
			ProgressNotificationHandler: m.handleProgress,
		})
		transport, err := m.transport(config)
		if err != nil {
			m.setStatus(state, "error", err.Error())
			return err
		}
		connected, err := client.Connect(ctx, transport, nil)
		if err != nil {
			m.setStatus(state, "error", err.Error())
			return err
		}
		state.mu.Lock()
		state.session = connected
		state.mu.Unlock()
	}
	return m.refreshTools(parent, state)
}

func (m *Manager) refreshTools(ctx context.Context, state *serverState) error {
	state.mu.RLock()
	session := state.session
	config := state.config
	state.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("MCP %q 尚未連線", config.ID)
	}
	remoteTools := []*mcp.Tool{}
	params := &mcp.ListToolsParams{}
	for {
		result, err := session.ListTools(ctx, params)
		if err != nil {
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
	resolved := make(map[string]resolvedTool, len(remoteTools))
	for _, tool := range remoteTools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
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
		readOnly := config.TrustAnnotations && tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		resolved[name] = resolvedTool{remoteName: tool.Name, definition: domain.ToolDefinition{
			Name: name, Label: label, Category: "mcp", Description: strings.TrimSpace(tool.Description), InputSchema: schemaMap(tool.InputSchema),
			Capabilities: []string{"mcp", "mcp:" + config.ID}, ReadOnly: readOnly, RequiresPermission: true,
		}}
	}
	state.mu.Lock()
	state.tools = resolved
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
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		client := &http.Client{Transport: &headerTransport{base: base, apiKey: config.APIKey, headers: config.Headers}}
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: 2, DisableStandaloneSSE: true}, nil
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
	sink := m.progress[token]
	m.progressMu.RUnlock()
	if sink == nil {
		return
	}
	_ = sink(domain.ToolExecution{Content: request.Params.Message, Details: map[string]any{
		"mcp_progress": request.Params.Progress,
		"mcp_total":    request.Params.Total,
	}})
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
		execution.Content = "MCP 工具需要額外的互動輸入，目前主系統尚未支援此流程。"
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
	if execution.Content == "" && result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		execution.Content = string(encoded)
	}
	if execution.Content == "" {
		execution.Content = map[bool]string{true: "MCP 工具執行失敗，但未提供錯誤內容。", false: "MCP 工具已完成。"}[execution.IsError]
	}
	execution.Content = truncateUTF8(execution.Content, m.maxOutputBytes)
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
	value := ServerStatus{ID: state.config.ID, DisplayName: state.config.DisplayName, Enabled: state.config.Enabled, Transport: state.config.Transport, Status: state.status, Error: state.lastError, UpdatedAt: state.updatedAt}
	for _, tool := range state.tools {
		value.Tools = append(value.Tools, ToolInfo{Name: tool.definition.Name, DisplayName: tool.definition.Label})
	}
	sort.Slice(value.Tools, func(i, j int) bool { return value.Tools[i].Name < value.Tools[j].Name })
	value.ToolCount = len(value.Tools)
	return value
}

func (m *Manager) closeState(state *serverState) error {
	state.connectMu.Lock()
	defer state.connectMu.Unlock()
	state.mu.Lock()
	session := state.session
	state.session = nil
	state.tools = map[string]resolvedTool{}
	state.status = map[bool]string{true: "disconnected", false: "disabled"}[state.config.Enabled]
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
	if session != nil {
		return session.Close()
	}
	return nil
}

func (m *Manager) disconnectWithError(state *serverState, err error) {
	state.connectMu.Lock()
	state.mu.Lock()
	session := state.session
	state.session = nil
	state.lastError = err.Error()
	state.status = "error"
	state.updatedAt = time.Now().UTC()
	state.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	state.connectMu.Unlock()
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
	value.Capabilities = cloneStrings(value.Capabilities)
	value.Platforms = cloneStrings(value.Platforms)
	return value
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
	base    http.RoundTripper
	apiKey  string
	headers map[string]string
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if t.apiKey != "" {
		clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	return t.base.RoundTrip(clone)
}

func failed(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: message, IsError: true}
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	data := []byte(value[:limit])
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data) + "\n…（MCP 輸出已截斷）"
}
