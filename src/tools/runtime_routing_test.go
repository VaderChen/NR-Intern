package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"testing"
)

// fakeRemoteSource 只實作 RemoteToolSource，證明 Runtime 不需要具體的 MCP Client。
type fakeRemoteSource struct {
	resolved []string
	executed []string
}

func (f *fakeRemoteSource) Definitions(context.Context, domain.Session) ([]domain.ToolDefinition, error) {
	return []domain.ToolDefinition{{Name: "mcp__b__tool"}, {Name: "mcp__a__tool"}}, nil
}

func (f *fakeRemoteSource) Execute(_ context.Context, _ domain.Session, call domain.ToolCall, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	f.executed = append(f.executed, call.Name)
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "remote"}, nil
}

func (f *fakeRemoteSource) ResolveDefinition(_ context.Context, _ domain.Session, name string) (domain.ToolDefinition, error) {
	f.resolved = append(f.resolved, name)
	return domain.ToolDefinition{Name: name}, nil
}

func (f *fakeRemoteSource) Catalog(*domain.Session) []domain.ToolCatalogEntry {
	return []domain.ToolCatalogEntry{{Definition: domain.ToolDefinition{Name: "mcp__b__tool"}}, {Definition: domain.ToolDefinition{Name: "mcp__a__tool"}}}
}

func TestRuntimeRoutesRemoteToolsThroughInterface(t *testing.T) {
	remote := &fakeRemoteSource{}
	runtime := &Runtime{MCP: remote}
	ctx := context.Background()
	session := domain.Session{}

	definitions, err := runtime.Definitions(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || definitions[0].Name != "mcp__a__tool" || definitions[1].Name != "mcp__b__tool" {
		t.Fatalf("外部工具定義未合併排序：%+v", definitions)
	}
	if catalog := runtime.Catalog(nil); len(catalog) != 2 || catalog[0].Definition.Name != "mcp__a__tool" {
		t.Fatalf("外部工具目錄未合併排序：%+v", catalog)
	}

	if _, err := runtime.ResolveDefinition(ctx, session, "MCP__a__tool"); err != nil {
		t.Fatal(err)
	}
	execution, err := runtime.Execute(ctx, session, domain.ToolCall{ID: "1", Name: " mcp__a__tool "}, nil)
	if err != nil || execution.Content != "remote" {
		t.Fatalf("mcp__ 前綴的呼叫未轉給外部來源：%+v %v", execution, err)
	}
	if len(remote.resolved) != 1 || len(remote.executed) != 1 {
		t.Fatalf("外部來源收到的呼叫次數不符：resolve=%v execute=%v", remote.resolved, remote.executed)
	}

	// 非 mcp__ 前綴不得落到外部來源。
	if _, err := runtime.ResolveDefinition(ctx, session, "file_read"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("原生工具名稱不應由外部來源解析：%v", err)
	}
	execution, err = runtime.Execute(ctx, session, domain.ToolCall{ID: "2", Name: "file_read"}, nil)
	if err != nil || !execution.IsError {
		t.Fatalf("沒有原生 Registry 時應回報不可用：%+v %v", execution, err)
	}
	if len(remote.resolved) != 1 || len(remote.executed) != 1 {
		t.Fatal("原生工具名稱被轉給外部來源")
	}
}

func TestRuntimeWithoutRemoteSource(t *testing.T) {
	runtime := &Runtime{}
	ctx := context.Background()
	definitions, err := runtime.Definitions(ctx, domain.Session{})
	if err != nil || len(definitions) != 0 {
		t.Fatalf("未設定來源時應回傳空清單：%+v %v", definitions, err)
	}
	if _, err := runtime.ResolveDefinition(ctx, domain.Session{}, "mcp__a__tool"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未設定外部來源時應回報找不到：%v", err)
	}
	execution, err := runtime.Execute(ctx, domain.Session{}, domain.ToolCall{ID: "1", Name: "mcp__a__tool"}, nil)
	if err != nil || !execution.IsError {
		t.Fatalf("未設定外部來源時應回報不可用：%+v %v", execution, err)
	}
	if len(runtime.Catalog(nil)) != 0 {
		t.Fatal("未設定來源時目錄應為空")
	}
}
