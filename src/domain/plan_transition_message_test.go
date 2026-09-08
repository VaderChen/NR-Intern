package domain

import (
	"strings"
	"testing"
	"time"
)

// 這三種寫法是從實際 session 的事件記錄裡數出來的，不是想像出來的：
// in_progress → completed 四次、pending → verifying 兩次、verifying → verifying 一次。
// 錯誤訊息是模型唯一的回饋，只說「不行」會讓它換一個同樣不行的值再試，
// 一個步驟因此耗掉兩三個回合。
func TestPlanStepTransitionErrorSaysWhatToSendNext(t *testing.T) {
	tests := []struct {
		name     string
		from     PlanStepStatus
		to       PlanStepStatus
		expected []string
	}{
		{
			name: "跳過查證", from: PlanStepStatusInProgress, to: PlanStepStatusCompleted,
			expected: []string{"in_progress", "completed", "verifying", "evidence"},
		},
		{
			name: "跳過開始", from: PlanStepStatusPending, to: PlanStepStatusVerifying,
			expected: []string{"pending", "verifying", "in_progress"},
		},
		{
			name: "重送同一個狀態", from: PlanStepStatusVerifying, to: PlanStepStatusVerifying,
			expected: []string{"verifying", "completed", "evidence"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := invalidPlanTransition(test.from, test.to).Error()
			for _, want := range test.expected {
				if !strings.Contains(message, want) {
					t.Fatalf("訊息缺少 %q：%s", want, message)
				}
			}
		})
	}
}

// 每一種狀態都要說得出下一步，否則模型在那個狀態下就沒有回饋可用。
func TestEveryPlanStepStatusExplainsItsNextMove(t *testing.T) {
	for _, status := range []PlanStepStatus{
		PlanStepStatusPending, PlanStepStatusInProgress, PlanStepStatusVerifying,
		PlanStepStatusBlocked, PlanStepStatusCompleted, PlanStepStatusSkipped,
	} {
		if strings.TrimSpace(planStepNextMove(status)) == "" {
			t.Fatalf("狀態 %s 沒有說明下一步", status)
		}
	}
}

// 走完整條合法路徑仍然要能通過：訊息改寫不該動到狀態機本身。
func TestPlanStepLifecycleStillCompletesThroughTheLegalPath(t *testing.T) {
	now := time.Now().UTC()
	plan, err := NewPlan("session_1", CreatePlanInput{
		Title: "任務", Objective: "目標",
		Steps: []CreatePlanStepInput{{Title: "步驟一", Verification: "條件一"}},
	}, now)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	stepID := plan.Steps[0].ID
	for _, move := range []UpdatePlanStepInput{
		{Status: PlanStepStatusInProgress},
		{Status: PlanStepStatusVerifying},
		{Status: PlanStepStatusCompleted, Evidence: "指令輸出顯示條件一成立"},
	} {
		plan, err = TransitionPlanStep(plan, stepID, move, now)
		if err != nil {
			t.Fatalf("轉移到 %s 失敗：%v", move.Status, err)
		}
	}
	if plan.Steps[0].Status != PlanStepStatusCompleted {
		t.Fatalf("步驟最終狀態 = %s", plan.Steps[0].Status)
	}
}
