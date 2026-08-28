package harness

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunNeverAcceptsPrematureFinalAnswerWhilePlanIsActive(t *testing.T) {
	sessions := newMemorySessions(testSession())
	plans, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	value, err := domain.NewPlan(testSession().ID, domain.CreatePlanInput{
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
	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "執行長任務"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BudgetExceeded == nil || result.BudgetExceeded.Resource != domain.RunBudgetResourceTurns {
		t.Fatalf("budget result = %+v, want turn limit while plan remains active", result.BudgetExceeded)
	}
	if result.Message.Metadata["internal"] == true || result.Message.Content == "" {
		t.Fatalf("final message must be visible: %+v", result.Message)
	}
	messages, err := sessions.ListMessages(context.Background(), testSession().ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	internalChecks := 0
	for _, message := range messages {
		if message.Metadata != nil && message.Metadata["phase"] == "plan_completion_check" {
			internalChecks++
		}
	}
	if internalChecks != runner.Budget.MaxTurns {
		t.Fatalf("internal plan checks = %d, want %d before safety pause", internalChecks, runner.Budget.MaxTurns)
	}
	if !strings.Contains(result.Message.Content, "工作持續次數過長") || !strings.Contains(result.Message.Content, "請告訴我「繼續」") {
		t.Fatalf("final message = %q, want an explicit continue prompt", result.Message.Content)
	}
}
