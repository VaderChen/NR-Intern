package approval

import (
	"AgenticService/src/domain"
	"context"
	"testing"
	"time"
)

func TestCoordinatorDeliversOneDurableDecisionToWaitingTool(t *testing.T) {
	coordinator := NewCoordinator([]string{"shell_exec"})
	request := domain.ToolApprovalRequest{
		ID:         "approval_1",
		RunID:      "run_1",
		SessionID:  "session_1",
		ToolCallID: "call_1",
		ToolName:   "shell_exec",
	}
	if !coordinator.Required("shell_exec") || coordinator.Required("file_read") {
		t.Fatal("required-tool policy does not match configured names")
	}
	if err := coordinator.Begin(request); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	result := make(chan domain.ToolApprovalDecision, 1)
	go func() {
		decision, _ := coordinator.Wait(context.Background(), request.ID)
		result <- decision
	}()
	if err := coordinator.Decide("run_1", domain.ToolApprovalDecisionInput{
		ApprovalID: "approval_1",
		Decision:   domain.ToolApprovalApprove,
		Reason:     "已確認唯讀參數",
		Permanent:  true,
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	select {
	case decision := <-result:
		if decision.Decision != domain.ToolApprovalApprove || decision.Reason != "已確認唯讀參數" || !decision.Permanent {
			t.Fatalf("decision = %+v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting tool did not receive the decision")
	}
	if err := coordinator.Decide("run_1", domain.ToolApprovalDecisionInput{ApprovalID: "approval_1", Decision: domain.ToolApprovalDeny}); err == nil {
		t.Fatal("a completed approval must reject duplicate decisions")
	}
}

func TestCoordinatorCancellationRemovesPendingApproval(t *testing.T) {
	coordinator := NewCoordinator([]string{"shell_exec"})
	request := domain.ToolApprovalRequest{ID: "approval_cancel", RunID: "run_cancel", ToolCallID: "call_cancel", ToolName: "shell_exec"}
	if err := coordinator.Begin(request); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Wait(ctx, request.ID); err == nil {
		t.Fatal("Wait should return the canceled context")
	}
	if err := coordinator.Decide(request.RunID, domain.ToolApprovalDecisionInput{ApprovalID: request.ID, Decision: domain.ToolApprovalApprove}); err == nil {
		t.Fatal("canceled approval remained pending")
	}
}
