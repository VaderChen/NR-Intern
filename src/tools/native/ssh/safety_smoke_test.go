package ssh

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"errors"
	"testing"
	"time"
)

func TestSSHDispatchStateSmoke(t *testing.T) {
	for _, tc := range []struct {
		result remoteCommandResult
		state  string
	}{
		{remoteCommandResult{runErr: context.DeadlineExceeded}, domain.ToolNotDispatched},
		{remoteCommandResult{dispatched: true, runErr: context.DeadlineExceeded}, domain.ToolOutcomeUnknown},
		{remoteCommandResult{dispatched: true, runErr: context.Canceled}, domain.ToolOutcomeUnknown},
		{remoteCommandResult{dispatched: true, runErr: context.DeadlineExceeded, exitError: true}, domain.ToolFailed},
		{remoteCommandResult{dispatched: true}, domain.ToolSucceeded},
	} {
		if got := domain.ToolExecutionState(remoteCommandDetails("test", tc.result)); got != tc.state {
			t.Fatalf("state=%s want=%s", got, tc.state)
		}
	}
}

func TestSSHWaitCanceledBeforeDispatchSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewWaitTool(map[string]Profile{"synthetic": {}}, 1024, time.Minute)
	result, err := tool.Execute(ctx, tools.Invocation{Call: domain.ToolCall{Name: "ssh_wait", Arguments: map[string]any{"profile": "synthetic", "command": "never-dispatched"}}}, nil)
	if !errors.Is(err, context.Canceled) || result.Details["canceled"] != true || result.Details["timed_out"] != false || domain.ToolExecutionState(result.Details) != domain.ToolNotDispatched {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSSHWaitTerminalStateSmoke(t *testing.T) {
	last := remoteCommandResult{dispatched: true, exitCode: 7, exitError: true, runErr: errors.New("synthetic exit status 7")}
	details := successfulSSHCheckDetails("synthetic", last)
	if domain.ToolExecutionState(details) != domain.ToolSucceeded || details["command_execution_state"] != domain.ToolFailed {
		t.Fatal("expected nonzero exit was not recorded as successful check")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		result := sshWaitInterrupted(domain.ToolCall{}, "synthetic", 60, 1, 1, last, true, true, cause)
		if result.Details["canceled"] != errors.Is(cause, context.Canceled) || result.Details["timed_out"] != errors.Is(cause, context.DeadlineExceeded) {
			t.Fatalf("wrong interruption flags: %+v", result.Details)
		}
		if result.Details["last_exit_code"] != 7 || domain.ToolExecutionState(result.Details) != domain.ToolFailed {
			t.Fatal("lost completed check evidence")
		}
	}
	result := sshWaitInterrupted(domain.ToolCall{}, "synthetic", 60, 0, 0, remoteCommandResult{}, false, false, context.Canceled)
	if domain.ToolExecutionState(result.Details) != domain.ToolNotDispatched {
		t.Fatal("cancellation before dispatch mislabeled")
	}
}
