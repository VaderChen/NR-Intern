//go:build !windows

package shell

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestShellTimeoutTerminatesChildProcessTree 防止只殺父 shell：若背景 child 活著，
// 它會在 timeout 後建立 marker，形成難以追蹤的延遲副作用。
func TestShellTimeoutTerminatesChildProcessTree(t *testing.T) {
	root := t.TempDir()
	tool := New(64*1024, 2*time.Second)
	result, err := tool.Execute(context.Background(), tools.Invocation{
		WorkspaceRoot: root,
		Call: domain.ToolCall{ID: "call_timeout", Name: "shell_exec", Arguments: map[string]any{
			"command":         "(sleep 2; echo leaked > child-marker.txt) & wait",
			"timeout_seconds": 1,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError || result.Details["timed_out"] != true {
		t.Fatalf("timeout result = %+v", result)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child-marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("child process survived timeout; marker stat err = %v", err)
	}
}
