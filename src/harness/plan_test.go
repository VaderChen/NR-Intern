package harness

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"context"
	"testing"
	"time"
)

func TestRunStopsRepeatingWhenActivePlanMakesNoProgress(t *testing.T) {
	session := testSession()
	session.LockPlans = true
	sessions := newMemorySessions(session)
	plans, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	value, err := domain.NewPlan(session.ID, domain.CreatePlanInput{
		Title: "尚未完成的計畫", Steps: []domain.CreatePlanStepInput{{Title: "執行修改", Verification: "測試通過"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if _, err := plans.Create(context.Background(), value); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "已完成"},
		{Content: "真的已完成"},
		{Content: "目前尚未依計畫完成。"},
	}}
	runner := newTestRunner(sessions, model, &fakeTools{})
	runner.Plans = plans
	result, err := runner.Run(context.Background(), Input{Session: session, UserInput: "執行長任務"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BudgetExceeded != nil {
		t.Fatalf("budget result = %+v, want normal no-progress stop", result.BudgetExceeded)
	}
	if result.Message.Metadata["internal"] == true || result.Message.Metadata["termination"] != "plan_no_progress" || result.Message.Content != "真的已完成" {
		t.Fatalf("final message must be visible: %+v", result.Message)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want one completion reminder and one visible stop", len(model.requests))
	}
	messages, err := sessions.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	internalChecks := 0
	for _, message := range messages {
		if message.Metadata != nil && message.Metadata["phase"] == "plan_completion_check" {
			internalChecks++
		}
	}
	if internalChecks != 1 {
		t.Fatalf("internal plan checks = %d, want exactly one for unchanged plan state", internalChecks)
	}
}
