package wait

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"testing"
	"time"
)

func TestWaitToolIsBoundedAndCancellable(t *testing.T) {
	tool := New(30 * time.Second)
	call := domain.ToolCall{ID: "call_wait", Name: "wait_for", Arguments: map[string]any{"duration_seconds": 5}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, tools.Invocation{Call: call}, nil); err != context.Canceled {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
}

func TestWaitToolDefinitionHasBoundedDuration(t *testing.T) {
	definition := New(2 * time.Minute).Definition()
	if definition.Name != "wait_for" || !definition.ReadOnly {
		t.Fatalf("definition = %+v", definition)
	}
	properties := definition.InputSchema["properties"].(map[string]any)
	duration := properties["duration_seconds"].(map[string]any)
	if duration["maximum"] != 120 {
		t.Fatalf("duration maximum = %v, want 120", duration["maximum"])
	}
}
