package filestore

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestPlanRepositoryPersistsOrderedPlansAndPromotesNext(t *testing.T) {
	repository, err := NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	first := newTestPlan(t, "session_1", "第一份")
	second := newTestPlan(t, "session_1", "第二份")
	first, err = repository.Create(context.Background(), first)
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err = repository.Create(context.Background(), second)
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if first.Status != domain.PlanStatusActive || second.Status != domain.PlanStatusQueued {
		t.Fatalf("statuses = %s, %s", first.Status, second.Status)
	}

	for _, status := range []domain.PlanStepStatus{
		domain.PlanStepStatusInProgress,
		domain.PlanStepStatusVerifying,
		domain.PlanStepStatusCompleted,
	} {
		first, err = domain.TransitionPlanStep(first, first.Steps[0].ID, domain.UpdatePlanStepInput{
			Status: status, Evidence: map[bool]string{true: "測試通過"}[status == domain.PlanStepStatusCompleted],
		}, time.Now())
		if err != nil {
			t.Fatalf("TransitionPlanStep(%s): %v", status, err)
		}
		first, err = repository.Update(context.Background(), first)
		if err != nil {
			t.Fatalf("Update(%s): %v", status, err)
		}
	}
	values, err := repository.List(context.Background(), "session_1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(values) != 2 || values[0].Status != domain.PlanStatusCompleted || values[1].Status != domain.PlanStatusActive {
		t.Fatalf("promoted plans = %#v", values)
	}
	if values[0].Position != 0 || values[1].Position != 1 {
		t.Fatalf("positions = %d, %d", values[0].Position, values[1].Position)
	}
	if err := repository.Delete(context.Background(), "session_1", first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repository.Get(context.Background(), "session_1", first.ID); err == nil {
		t.Fatalf("Get deleted plan should fail")
	}
}

func TestPlanRepositoryMigratesLegacySinglePlanOnWrite(t *testing.T) {
	repository, err := NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	legacy := newTestPlan(t, "session_legacy", "舊計畫")
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path, err := repository.path(legacy.SessionID)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	values, err := repository.List(context.Background(), legacy.SessionID)
	if err != nil || len(values) != 1 || values[0].ID != legacy.ID {
		t.Fatalf("legacy list = %#v, err = %v", values, err)
	}
	if _, err := repository.Create(context.Background(), newTestPlan(t, legacy.SessionID, "新計畫")); err != nil {
		t.Fatalf("Create after legacy: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var migrated planFile
	if err := json.Unmarshal(stored, &migrated); err != nil {
		t.Fatalf("Unmarshal migrated: %v", err)
	}
	if migrated.Version != planFileVersion || len(migrated.Plans) != 2 {
		t.Fatalf("migrated file = %#v", migrated)
	}
}

func TestPlanRepositoryRejectsDemotingStartedActivePlan(t *testing.T) {
	repository, err := NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	first, err := repository.Create(context.Background(), newTestPlan(t, "session_order", "進行中"))
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := repository.Create(context.Background(), newTestPlan(t, "session_order", "排隊中"))
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	first, err = domain.TransitionPlanStep(first, first.Steps[0].ID, domain.UpdatePlanStepInput{Status: domain.PlanStepStatusInProgress}, time.Now())
	if err != nil {
		t.Fatalf("TransitionPlanStep: %v", err)
	}
	if _, err := repository.Update(context.Background(), first); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := repository.Reorder(context.Background(), first.SessionID, []string{second.ID, first.ID}); err == nil {
		t.Fatalf("Reorder should reject demoting a started active plan")
	}
}

func newTestPlan(t *testing.T, sessionID, title string) domain.Plan {
	t.Helper()
	value, err := domain.NewPlan(sessionID, domain.CreatePlanInput{
		Title: title, Steps: []domain.CreatePlanStepInput{{Title: "步驟", Verification: "檢查結果"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	return value
}
