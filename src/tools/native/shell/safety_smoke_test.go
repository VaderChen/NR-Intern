//go:build !windows

package shell

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"testing"
	"time"
)

func TestShellTimeoutIsUnknownSmoke(t *testing.T) {
	result, err := New(1024, 2*time.Second).Execute(context.Background(), tools.Invocation{
		WorkspaceRoot: t.TempDir(), Call: domain.ToolCall{ID: "smoke", Name: "shell_exec", Arguments: map[string]any{"command": "echo started; sleep 3", "timeout_seconds": 1}},
	}, nil)
	if err != nil || !result.IsError || domain.ToolExecutionState(result.Details) != domain.ToolOutcomeUnknown {
		t.Fatalf("timeout: %+v %v", result, err)
	}
}
