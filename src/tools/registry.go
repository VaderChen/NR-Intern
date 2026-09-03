package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// NativeTool 是 Agent 原生工具的最小擴充介面。工具實作不依賴 Harness、HTTP 或模型協定。
type NativeTool interface {
	Definition() domain.ToolDefinition
	Execute(context.Context, Invocation, ports.ToolUpdateSink) (domain.ToolExecution, error)
}

// ToggleableTool 讓工具可以在執行期被後端設定關閉。未實作這個介面的工具一律視為
// 可用；實作後，關閉狀態下工具不會出現在模型的工具清單，執行請求也會直接拒絕。
type ToggleableTool interface {
	Enabled() bool
	DisabledReason() string
}

type Invocation struct {
	Session        domain.Session
	Call           domain.ToolCall
	WorkspaceRoot  string
	WorkspaceRoots []string
}

// SandboxRoots 回傳本次工具呼叫可使用的全部根目錄；WorkspaceRoot 保留作為單目錄相容與預設工作目錄。
func (i Invocation) SandboxRoots() []string {
	if len(i.WorkspaceRoots) > 0 {
		return append([]string(nil), i.WorkspaceRoots...)
	}
	if root := strings.TrimSpace(i.WorkspaceRoot); root != "" {
		return []string{root}
	}
	return nil
}

type RegistryConfig struct {
	AllowedNames  []string
	AllowElevated bool
	// Permissions 決定哪些 permission profile 屬於 elevated。
	// 這份策略由後端設定提供，與建立 Session 時套用的是同一份，
	// 避免呼叫端自行宣告的 profile 成為提權依據。
	Permissions domain.PermissionPolicy
	Logger      *slog.Logger
}

// Registry 同時是工具目錄與 ports.ToolRuntime 實作。
type Registry struct {
	mu            sync.RWMutex
	items         map[string]NativeTool
	allowed       map[string]struct{}
	permissions   domain.PermissionPolicy
	allowElevated bool
	logger        *slog.Logger
}

func NewRegistry(config RegistryConfig, values ...NativeTool) (*Registry, error) {
	registry := &Registry{
		items:         map[string]NativeTool{},
		allowed:       stringSet(config.AllowedNames),
		permissions:   config.Permissions.Normalize(),
		allowElevated: config.AllowElevated,
		logger:        logging.Or(config.Logger),
	}
	for _, value := range values {
		if err := registry.Register(value); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(value NativeTool) error {
	if value == nil {
		return fmt.Errorf("%w: native tool is nil", domain.ErrInvalidInput)
	}
	definition := value.Definition()
	name := strings.TrimSpace(definition.Name)
	if name == "" {
		return fmt.Errorf("%w: native tool name is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; exists {
		return fmt.Errorf("%w: native tool %q already registered", domain.ErrConflict, name)
	}
	r.items[name] = value
	return nil
}

func (r *Registry) Definitions(_ context.Context, session domain.Session) ([]domain.ToolDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definitions := make([]domain.ToolDefinition, 0, len(r.items))
	for name, value := range r.items {
		if !r.isAllowed(name) {
			continue
		}
		if _, disabled := toolDisabledReason(value); disabled {
			continue
		}
		definition := value.Definition()
		if definition.RequiresPermission && !r.elevatedAllowed(session.PermissionProfile) {
			continue
		}
		definitions = append(definitions, cloneDefinition(definition))
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func (r *Registry) Execute(ctx context.Context, session domain.Session, call domain.ToolCall, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return failedExecution(call, "native tool name is required"), nil
	}
	r.mu.RLock()
	value := r.items[name]
	allowed := r.isAllowed(name)
	r.mu.RUnlock()
	if value == nil || !allowed {
		r.logger.Warn("native tool refused", "tool_name", name, "session_id", session.ID, "reason", "not_registered_or_disallowed")
		return failedExecution(call, "native tool is unavailable or disabled"), nil
	}
	if reason, disabled := toolDisabledReason(value); disabled {
		r.logger.Warn("disabled tool refused", "tool_name", name, "session_id", session.ID, "reason", reason)
		return failedExecution(call, reason), nil
	}
	definition := value.Definition()
	if definition.RequiresPermission && !r.elevatedAllowed(session.PermissionProfile) {
		// 提權被擋下屬於安全事件，必須留下紀錄。
		r.logger.Warn("elevated tool refused",
			"tool_name", name,
			"session_id", session.ID,
			"permission_profile", session.PermissionProfile,
			"allow_elevated_tools", r.allowElevated,
		)
		return failedExecution(call, "native tool requires an elevated session permission profile"), nil
	}
	workspaceRoots := workspaceRootsFromSession(session)
	if len(workspaceRoots) == 0 {
		return failedExecution(call, "session workspace is unavailable"), nil
	}
	result, err := value.Execute(ctx, Invocation{
		Session:        session,
		Call:           normalizeCallArguments(cloneCall(call), definition),
		WorkspaceRoot:  workspaceRoots[0],
		WorkspaceRoots: workspaceRoots,
	}, sink)
	result.ToolCallID = call.ID
	result.ToolName = name
	return result, err
}

func (r *Registry) ListToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		if r.isAllowed(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Catalog(session *domain.Session) []domain.ToolCatalogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.ToolCatalogEntry, 0, len(r.items))
	for name, tool := range r.items {
		definition := cloneDefinition(tool.Definition())
		entry := domain.ToolCatalogEntry{
			Definition: definition,
			Allowed:    r.isAllowed(name),
			Available:  r.isAllowed(name),
		}
		if reason, disabled := toolDisabledReason(tool); disabled && entry.Allowed {
			entry.Available = false
			entry.UnavailableReason = reason
		} else if !entry.Allowed {
			entry.UnavailableReason = "tool is disabled by the backend allowlist"
		} else if definition.RequiresPermission {
			switch {
			case !r.allowElevated:
				entry.Available = false
				entry.UnavailableReason = "backend elevated tools are disabled"
			case session == nil:
				entry.Available = false
				entry.UnavailableReason = "a session is required to evaluate permission"
			case !r.elevatedAllowed(session.PermissionProfile):
				entry.Available = false
				entry.UnavailableReason = "session permission profile is not elevated"
			}
		}
		values = append(values, entry)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Definition.Name < values[j].Definition.Name })
	return values
}

func toolDisabledReason(value NativeTool) (string, bool) {
	toggleable, ok := value.(ToggleableTool)
	if !ok || toggleable.Enabled() {
		return "", false
	}
	reason := strings.TrimSpace(toggleable.DisabledReason())
	if reason == "" {
		reason = "tool is turned off in the backend settings"
	}
	return reason, true
}

// SetAllowedNames 讓管理介面在不重啟後端的情況下調整工具集。
// 空集合代表不設限制（維持既有語意）。
func (r *Registry) SetAllowedNames(names []string) {
	allowed := stringSet(names)
	r.mu.Lock()
	r.allowed = allowed
	r.mu.Unlock()
}

func (r *Registry) isAllowed(name string) bool {
	if len(r.allowed) == 0 {
		return true
	}
	_, ok := r.allowed[name]
	return ok
}

// elevatedAllowed 在沒有設定任何 elevated profile 時 fail closed：
// 未宣告就代表後端沒有授權任何 profile 使用高權限工具。
func (r *Registry) elevatedAllowed(profile string) bool {
	if !r.allowElevated {
		return false
	}
	return r.permissions.IsElevated(profile)
}

func workspaceRootsFromSession(session domain.Session) []string {
	if session.Metadata == nil {
		return nil
	}
	values := []string{}
	switch roots := session.Metadata["sandbox_roots"].(type) {
	case []string:
		values = append(values, roots...)
	case []any:
		for _, value := range roots {
			if root, ok := value.(string); ok {
				values = append(values, root)
			}
		}
	}
	result := make([]string, 0, len(values)+1)
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	if len(result) == 0 {
		if value, _ := session.Metadata["workspace_root"].(string); strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func cloneCall(call domain.ToolCall) domain.ToolCall {
	result := call
	if call.Arguments != nil {
		result.Arguments = make(map[string]any, len(call.Arguments))
		for key, value := range call.Arguments {
			result.Arguments[key] = value
		}
	}
	return result
}

func cloneDefinition(value domain.ToolDefinition) domain.ToolDefinition {
	result := value
	result.Platforms = append([]string(nil), value.Platforms...)
	result.Capabilities = append([]string(nil), value.Capabilities...)
	if value.InputSchema != nil {
		result.InputSchema = make(map[string]any, len(value.InputSchema))
		for key, item := range value.InputSchema {
			result.InputSchema[key] = item
		}
	}
	if value.OutputSchema != nil {
		result.OutputSchema = make(map[string]any, len(value.OutputSchema))
		for key, item := range value.OutputSchema {
			result.OutputSchema[key] = item
		}
	}
	return result
}

func failedExecution(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    message,
		IsError:    true,
	}
}

func stringSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
