package mcpclient

import (
	"AgenticService/src/domain"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// ServerDefinitions 回傳單一 Server 的完整工具定義，含描述與輸入 schema。
//
// Definitions 給模型用，會把所有 Server 的工具混在一起，而且只含本次公開的那些。
// 解讀契約要的是「這一台提供什麼」，包含被工具白名單擋掉的：那些工具仍然是這台
// Server 的能力，只是這次沒公開，寫進記憶時應該讓人知道它們存在。
func (m *Manager) ServerDefinitions(ctx context.Context, id string) ([]domain.ToolDefinition, error) {
	id = strings.TrimSpace(id)
	for _, state := range m.states() {
		if stateID(state) != id {
			continue
		}
		if err := m.ensureConnected(ctx, state, true); err != nil {
			return nil, err
		}
		state.mu.RLock()
		defer state.mu.RUnlock()
		definitions := make([]domain.ToolDefinition, 0, len(state.tools))
		for _, tool := range state.tools {
			definitions = append(definitions, cloneDefinition(tool.definition))
		}
		sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
		return definitions, nil
	}
	return nil, fmt.Errorf("%w: MCP %q", domain.ErrNotFound, id)
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
			Name: name, Label: label, Category: "mcp", Description: strings.TrimSpace(tool.Description), InputSchema: continuationSchema(schemaMap(tool.InputSchema)),
			OutputSchema: optionalSchemaMap(tool.OutputSchema), ServerInstructions: serverInstructions,
			Capabilities: []string{"mcp", "mcp:" + config.ID}, ReadOnly: readOnly, RequiresPermission: true,
		}, idempotent: idempotent}
		value := resolved[name]
		value.definition.ContractID = domain.ToolContractFingerprint(value.definition, fmt.Sprintf("%s:%s:%t", state.contractScope, tool.Name, idempotent))
		resolved[name] = value
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
