package bootstrap

import (
	"AgenticService/src/domain"
	"AgenticService/src/mcpclient"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MCPSettings 回傳脫敏後的 MCP Server 設定與目前連線狀態。
func (r *Runtime) MCPSettings(ctx context.Context) (domain.MCPSettings, error) {
	if err := ctx.Err(); err != nil {
		return domain.MCPSettings{}, err
	}
	if r == nil || r.MCP == nil {
		return domain.MCPSettings{}, fmt.Errorf("%w: MCP client manager is unavailable", domain.ErrNotFound)
	}
	r.configMu.RLock()
	configs := make(map[string]mcpclient.ServerConfig, len(r.Config.MCPServers))
	for id, value := range r.Config.MCPServers {
		configs[id] = value
	}
	r.configMu.RUnlock()
	return mcpSettingsView(configs, r.MCP.Statuses()), nil
}

// UpdateMCPSettings 驗證並原子替換完整 MCP Server 集合。敏感的 API Key、
// 環境變數與 HTTP Headers 未出現在 payload 時會保留，明確傳入空值才會清除。
func (r *Runtime) UpdateMCPSettings(ctx context.Context, input domain.UpdateMCPSettingsInput) (domain.MCPSettings, error) {
	if err := ctx.Err(); err != nil {
		return domain.MCPSettings{}, err
	}
	if r == nil || r.MCP == nil {
		return domain.MCPSettings{}, fmt.Errorf("%w: MCP client manager is unavailable", domain.ErrNotFound)
	}
	r.configMu.Lock()
	defer r.configMu.Unlock()

	existing := r.Config.MCPServers
	next := make(map[string]mcpclient.ServerConfig, len(input.Servers))
	values := make([]mcpclient.ServerConfig, 0, len(input.Servers))
	for _, setting := range input.Servers {
		id := strings.TrimSpace(setting.ID)
		if _, exists := next[id]; exists {
			return domain.MCPSettings{}, fmt.Errorf("%w: duplicate MCP id %q", domain.ErrConflict, id)
		}
		trustAnnotations := setting.TrustAnnotations
		value := mcpclient.ServerConfig{
			ID: id, DisplayName: setting.DisplayName, Enabled: setting.Enabled, Transport: setting.Transport,
			Command: setting.Command, Args: setting.Args, Environment: setting.Environment, WorkDir: setting.WorkDir,
			URL: setting.URL, Headers: setting.Headers, StartupTimeoutSeconds: setting.StartupTimeoutSeconds,
			CallTimeoutSeconds: setting.CallTimeoutSeconds, TrustAnnotations: &trustAnnotations,
		}
		if previous, exists := existing[id]; exists {
			if setting.APIKey == nil {
				value.APIKey = previous.APIKey
			}
			if setting.Environment == nil {
				value.Environment = previous.Environment
			}
			if setting.Headers == nil {
				value.Headers = previous.Headers
			}
			if setting.EnabledTools == nil {
				value.EnabledTools = previous.EnabledTools
			}
			if setting.Username == nil {
				value.Username = previous.Username
			}
			if setting.Password == nil {
				value.Password = previous.Password
			}
		}
		if setting.EnabledTools != nil {
			value.EnabledTools = append([]string(nil), (*setting.EnabledTools)...)
		}
		if setting.APIKey != nil {
			value.APIKey = strings.TrimSpace(*setting.APIKey)
		}
		if setting.Username != nil {
			value.Username = strings.TrimSpace(*setting.Username)
		}
		if setting.Password != nil {
			value.Password = *setting.Password
		}
		normalized, err := value.Normalize()
		if err != nil {
			return domain.MCPSettings{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
		}
		next[normalized.ID] = normalized
		values = append(values, normalized)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	if err := persistMCPSettings(r.Config.DataDir, next); err != nil {
		return domain.MCPSettings{}, err
	}
	if err := r.MCP.Replace(values); err != nil {
		return domain.MCPSettings{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	r.Config.MCPServers = next
	r.MCP.Warm(context.Background())
	return mcpSettingsView(next, r.MCP.Statuses()), nil
}

// TestMCP 重新建立指定 MCP Server 的連線並刷新工具清單。
func (r *Runtime) TestMCP(ctx context.Context, id string) (domain.MCPTestResult, error) {
	if r == nil || r.MCP == nil {
		return domain.MCPTestResult{}, fmt.Errorf("%w: MCP client manager is unavailable", domain.ErrNotFound)
	}
	id = strings.TrimSpace(id)
	for _, current := range r.MCP.Statuses() {
		if current.ID == id && !current.Enabled {
			return domain.MCPTestResult{ID: id, Status: "disabled"}, fmt.Errorf("%w: MCP %q is disabled", domain.ErrInvalidInput, id)
		}
	}
	status, err := r.MCP.Refresh(ctx, id)
	result := domain.MCPTestResult{ID: status.ID, OK: err == nil && status.Status == "connected", Status: status.Status, ToolCount: status.ToolCount, Error: status.Error}
	for _, tool := range status.Tools {
		result.Tools = append(result.Tools, domain.MCPToolSetting{Name: tool.Name, DisplayName: tool.DisplayName})
	}
	if err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("%w: test MCP %q: %v", domain.ErrInvalidInput, id, err)
	}
	return result, nil
}

func mcpSettingsView(configs map[string]mcpclient.ServerConfig, statuses []mcpclient.ServerStatus) domain.MCPSettings {
	statusByID := make(map[string]mcpclient.ServerStatus, len(statuses))
	for _, status := range statuses {
		statusByID[status.ID] = status
	}
	ids := make([]string, 0, len(configs))
	for id := range configs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := domain.MCPSettings{Servers: make([]domain.MCPServerSetting, 0, len(ids))}
	for _, id := range ids {
		config := configs[id]
		status := statusByID[id]
		setting := domain.MCPServerSetting{
			ID: config.ID, DisplayName: config.DisplayName, Enabled: config.Enabled, Transport: config.Transport,
			Command: config.Command, Args: append([]string(nil), config.Args...), HasEnvironment: len(config.Environment) > 0,
			WorkDir: config.WorkDir, URL: config.URL, HasAPIKey: config.APIKey != "", HasHeaders: len(config.Headers) > 0,
			HasBasicAuth:          config.Username != "" || config.Password != "",
			StartupTimeoutSeconds: config.StartupTimeoutSeconds, CallTimeoutSeconds: config.CallTimeoutSeconds,
			TrustAnnotations: config.TrustsAnnotations(), AuthMode: config.AuthMode(),
			Status: status.Status, Error: status.Error, ToolCount: status.ToolCount,
		}
		if !status.UpdatedAt.IsZero() {
			setting.UpdatedAt = status.UpdatedAt.Format(time.RFC3339)
		}
		for _, tool := range status.Tools {
			setting.Tools = append(setting.Tools, domain.MCPToolSetting{Name: tool.Name, DisplayName: tool.DisplayName})
		}
		for _, tool := range status.AvailableTools {
			setting.AvailableTools = append(setting.AvailableTools, domain.MCPToolSetting{Name: tool.Name, DisplayName: tool.DisplayName})
		}
		enabled := append([]string(nil), config.EnabledTools...)
		setting.EnabledTools = &enabled
		result.Servers = append(result.Servers, setting)
	}
	return result
}
