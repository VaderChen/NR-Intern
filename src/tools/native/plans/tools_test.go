package plans

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"testing"
)

func TestPlanToolsCreateAndEnforceVerificationLifecycle(t *testing.T) {
	repository, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	session := domain.Session{ID: "session_1"}
	create := NewCreateTool(repository)
	created, err := create.Execute(context.Background(), tools.Invocation{
		Session: session,
		Call: domain.ToolCall{ID: "call_create", Name: "plan_create", Arguments: map[string]any{
			"title": "修改功能",
			"steps": []any{map[string]any{"title": "實作", "verification": "測試通過"}},
		}},
	}, nil)
	if err != nil || created.IsError {
		t.Fatalf("create execution = %#v, err = %v", created, err)
	}
	values, err := repository.List(context.Background(), session.ID)
	if err != nil || len(values) != 1 {
		t.Fatalf("List plans = %#v, err = %v", values, err)
	}
	value := values[0]
	update := NewUpdateStepTool(repository)
	completed, err := update.Execute(context.Background(), tools.Invocation{
		Session: session,
		Call: domain.ToolCall{ID: "call_complete", Name: "plan_step_update", Arguments: map[string]any{
			"step_id": value.Steps[0].ID, "status": "completed", "evidence": "claimed",
		}},
	}, nil)
	if err != nil {
		t.Fatalf("complete execution error: %v", err)
	}
	if !completed.IsError {
		t.Fatalf("complete before start/verify should be rejected")
	}
}

func TestPlanToolsPromoteNextQueuedPlanAfterVerifiedCompletion(t *testing.T) {
	repository, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	session := domain.Session{ID: "session_queue"}
	create := NewCreateTool(repository)
	for _, title := range []string{"第一份", "第二份"} {
		execution, executeErr := create.Execute(context.Background(), tools.Invocation{
			Session: session,
			Call: domain.ToolCall{ID: "call_create_" + title, Name: "plan_create", Arguments: map[string]any{
				"title": title,
				"steps": []any{map[string]any{"title": "執行", "verification": "測試通過"}},
			}},
		}, nil)
		if executeErr != nil || execution.IsError {
			t.Fatalf("create %s = %#v, err = %v", title, execution, executeErr)
		}
	}
	values, err := repository.List(context.Background(), session.ID)
	if err != nil || len(values) != 2 {
		t.Fatalf("List = %#v, err = %v", values, err)
	}
	first := values[0]
	update := NewUpdateStepTool(repository)
	for _, input := range []map[string]any{
		{"status": "in_progress"},
		{"status": "verifying"},
		{"status": "completed", "evidence": "go test 通過"},
	} {
		input["plan_id"] = first.ID
		input["step_id"] = first.Steps[0].ID
		execution, executeErr := update.Execute(context.Background(), tools.Invocation{
			Session: session,
			Call:    domain.ToolCall{ID: "call_update", Name: "plan_step_update", Arguments: input},
		}, nil)
		if executeErr != nil || execution.IsError {
			t.Fatalf("update %v = %#v, err = %v", input, execution, executeErr)
		}
	}
	values, err = repository.List(context.Background(), session.ID)
	if err != nil || values[0].Status != domain.PlanStatusCompleted || values[1].Status != domain.PlanStatusActive {
		t.Fatalf("promoted plans = %#v, err = %v", values, err)
	}
}
