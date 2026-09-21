package tools

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"testing"
)

func TestCanceledRegistryDoesNotDispatchSmoke(t *testing.T) {
	tool := readOnlyTool()
	r, err := NewRegistry(RegistryConfig{}, tool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := r.Execute(ctx, sessionWithProfile("default"), domain.ToolCall{ID: "call", Name: "file_read"}, nil)
	if !errors.Is(err, context.Canceled) || tool.calls != 0 || domain.ToolExecutionState(result.Details) != domain.ToolNotDispatched {
		t.Fatalf("%+v err=%v calls=%d", result, err, tool.calls)
	}
}

func TestRegistryRefusalsAreNotDispatchedSmoke(t *testing.T) {
	tool := elevatedTool()
	r, err := NewRegistry(RegistryConfig{AllowElevated: true, Permissions: trustedPolicy()}, tool)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "missing", "shell_exec"} {
		result, err := r.Execute(context.Background(), sessionWithProfile("default"), domain.ToolCall{ID: "call", Name: name}, nil)
		if err != nil || !result.IsError || domain.ToolExecutionState(result.Details) != domain.ToolNotDispatched || tool.calls != 0 {
			t.Fatalf("%s: %+v err=%v calls=%d", name, result, err, tool.calls)
		}
	}
}
