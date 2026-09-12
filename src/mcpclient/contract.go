package mcpclient

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
)

func (m *Manager) ResolveDefinition(ctx context.Context, _ domain.Session, name string) (domain.ToolDefinition, error) {
	state, _ := m.route(name)
	if state == nil {
		return domain.ToolDefinition{}, fmt.Errorf("%w: MCP 工具已不可用", domain.ErrNotFound)
	}
	if err := m.ensureConnected(ctx, state, false); err != nil {
		return domain.ToolDefinition{}, err
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	tool, exists := state.tools[name]
	if !exists || !state.config.Enabled || state.session == nil {
		return domain.ToolDefinition{}, fmt.Errorf("%w: MCP 工具已不可用", domain.ErrNotFound)
	}
	return cloneDefinition(tool.definition), nil
}
