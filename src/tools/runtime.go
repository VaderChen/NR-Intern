package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/mcpclient"
	"AgenticService/src/ports"
	"context"
	"sort"
	"strings"
)

// Runtime 將內建工具與使用者在後台加入的 MCP 工具聚合成 Harness
// 唯一看見的 ToolRuntime，並以公開工具名稱將呼叫路由回正確來源。
type Runtime struct {
	Native *Registry
	MCP    *mcpclient.Manager
}

func (r *Runtime) Definitions(ctx context.Context, session domain.Session) ([]domain.ToolDefinition, error) {
	values := []domain.ToolDefinition{}
	if r != nil && r.Native != nil {
		native, err := r.Native.Definitions(ctx, session)
		if err != nil {
			return nil, err
		}
		values = append(values, native...)
	}
	if r != nil && r.MCP != nil {
		remote, err := r.MCP.Definitions(ctx, session)
		if err != nil {
			return nil, err
		}
		values = append(values, remote...)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

func (r *Runtime) Execute(ctx context.Context, session domain.Session, call domain.ToolCall, sink ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(call.Name)), "mcp__") && r != nil && r.MCP != nil {
		return r.MCP.Execute(ctx, session, call, sink)
	}
	if r == nil || r.Native == nil {
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "工具 Runtime 不可用", IsError: true}, nil
	}
	return r.Native.Execute(ctx, session, call, sink)
}

func (r *Runtime) Catalog(session *domain.Session) []domain.ToolCatalogEntry {
	values := []domain.ToolCatalogEntry{}
	if r != nil && r.Native != nil {
		values = append(values, r.Native.Catalog(session)...)
	}
	if r != nil && r.MCP != nil {
		values = append(values, r.MCP.Catalog(session)...)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Definition.Name < values[j].Definition.Name })
	return values
}
