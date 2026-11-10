package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"sort"
	"strings"
)

// Runtime 將內建工具與使用者在後台加入的 MCP 工具聚合成 Harness
// 唯一看見的 ToolRuntime，並以公開工具名稱將呼叫路由回正確來源。
type Runtime struct {
	Native *Registry
	MCP    RemoteToolSource
}

// RemoteToolSource 是 Runtime 對外部工具來源（目前是 MCP Client）的全部需求。
// 介面定義在使用端，tools 因此不必 import mcpclient，外部工具的連線、設定與
// 狀態管理留在 bootstrap 與 mcpclient。
//
// 欄位不可放入型別為指標、值為 nil 的實作：介面值不等於 nil，Runtime 會照常
// 轉發而在實作內部 panic。沒有外部工具來源時讓欄位保持未設定。
type RemoteToolSource interface {
	ports.ToolRuntime
	ports.ToolContractResolver
	Catalog(session *domain.Session) []domain.ToolCatalogEntry
}

func (r *Runtime) ResolveDefinition(ctx context.Context, session domain.Session, name string) (domain.ToolDefinition, error) {
	if strings.HasPrefix(strings.ToLower(name), "mcp__") && r != nil && r.MCP != nil {
		return r.MCP.ResolveDefinition(ctx, session, name)
	}
	if r != nil && r.Native != nil {
		return r.Native.ResolveDefinition(ctx, session, name)
	}
	return domain.ToolDefinition{}, fmt.Errorf("%w: 工具不可用", domain.ErrNotFound)
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
