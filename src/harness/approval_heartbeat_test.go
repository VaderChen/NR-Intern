package harness

import (
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"context"
	"testing"
	"time"
)

// 核准對話框可能因為切換對話而沒被看到。等待期間必須持續回報，否則 Run 會安靜地
// 卡住到 wall-clock 預算用完，畫面上只有一個沒有說明的轉圈圈。
func TestApprovalWaitEmitsHeartbeat(t *testing.T) {
	original := approvalHeartbeatInterval
	approvalHeartbeatInterval = 40 * time.Millisecond
	defer func() { approvalHeartbeatInterval = original }()

	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_wait", Name: "shell_exec", Arguments: map[string]any{"command": "pwd"}}}},
		{Content: "完成"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "shell_exec"}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	})
	coordinator := approval.NewCoordinator([]string{"shell_exec"})
	runner.Approvals = coordinator

	requests := make(chan domain.ToolApprovalRequest, 1)
	heartbeats := make(chan map[string]any, 8)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), Input{RunID: "run_wait", Session: testSession(), UserInput: "執行"}, func(event domain.EngineEvent) error {
			switch event.Type {
			case "run.approval_required":
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				requests <- request
			case "tool.execution.update":
				update, _ := event.Payload["update"].(domain.ToolExecution)
				if update.Details["phase"] == "waiting_approval" {
					select {
					case heartbeats <- update.Details:
					default:
					}
				}
			}
			return nil
		})
		done <- err
	}()

	var request domain.ToolApprovalRequest
	select {
	case request = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("approval request was not emitted")
	}
	select {
	case details := <-heartbeats:
		if details["approval_id"] != request.ID {
			t.Fatalf("heartbeat does not identify the approval: %+v", details)
		}
		if _, ok := details["elapsed_seconds"].(int); !ok {
			t.Fatalf("heartbeat has no elapsed time: %+v", details)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat was emitted while waiting for approval")
	}

	if err := coordinator.Decide("run_wait", domain.ToolApprovalDecisionInput{
		ApprovalID: request.ID, Decision: domain.ToolApprovalApprove,
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not continue after approval")
	}
}
