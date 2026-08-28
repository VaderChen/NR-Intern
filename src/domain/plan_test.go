package domain

import (
	"errors"
	"testing"
	"time"
)

func testPlan(t *testing.T) Plan {
	t.Helper()
	value, err := NewPlan("session_1", CreatePlanInput{
		Title: "完成修改",
		Steps: []CreatePlanStepInput{
			{Title: "修改程式", Verification: "語法檢查通過"},
			{Title: "確認結果", Verification: "測試通過"},
		},
	}, time.Now())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	return value
}

func TestPlanRequiresOrderedExecutionAndVerificationEvidence(t *testing.T) {
	value := testPlan(t)
	if _, err := TransitionPlanStep(value, value.Steps[1].ID, UpdatePlanStepInput{Status: PlanStepStatusInProgress}, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("start second step error = %v, want conflict", err)
	}
	value, err := TransitionPlanStep(value, value.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusInProgress}, time.Now())
	if err != nil {
		t.Fatalf("start first step: %v", err)
	}
	if _, err := TransitionPlanStep(value, value.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusCompleted, Evidence: "看起來可以"}, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("complete before verification error = %v, want conflict", err)
	}
	value, err = TransitionPlanStep(value, value.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusVerifying}, time.Now())
	if err != nil {
		t.Fatalf("verify first step: %v", err)
	}
	if _, err := TransitionPlanStep(value, value.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusCompleted}, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("complete without evidence error = %v, want invalid input", err)
	}
	value, err = TransitionPlanStep(value, value.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusCompleted, Evidence: "go test ./... passed"}, time.Now())
	if err != nil {
		t.Fatalf("complete first step: %v", err)
	}
	if value.CurrentStepID != value.Steps[1].ID {
		t.Fatalf("current step = %q, want %q", value.CurrentStepID, value.Steps[1].ID)
	}
}

func TestPlanCompletesOnlyAfterEveryStepFinishes(t *testing.T) {
	value := testPlan(t)
	for index := range value.Steps {
		stepID := value.Steps[index].ID
		var err error
		value, err = TransitionPlanStep(value, stepID, UpdatePlanStepInput{Status: PlanStepStatusInProgress}, time.Now())
		if err != nil {
			t.Fatalf("start step %d: %v", index+1, err)
		}
		value, err = TransitionPlanStep(value, stepID, UpdatePlanStepInput{Status: PlanStepStatusVerifying}, time.Now())
		if err != nil {
			t.Fatalf("verify step %d: %v", index+1, err)
		}
		value, err = TransitionPlanStep(value, stepID, UpdatePlanStepInput{Status: PlanStepStatusCompleted, Evidence: "verified"}, time.Now())
		if err != nil {
			t.Fatalf("complete step %d: %v", index+1, err)
		}
	}
	if value.Status != PlanStatusCompleted || value.CurrentStepID != "" {
		t.Fatalf("plan = status %q current %q, want completed", value.Status, value.CurrentStepID)
	}
}
