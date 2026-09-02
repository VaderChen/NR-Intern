package plans

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type GetTool struct{ Repository ports.PlanRepository }
type CreateTool struct{ Repository ports.PlanRepository }
type UpdateStepTool struct{ Repository ports.PlanRepository }

func NewGetTool(repository ports.PlanRepository) *GetTool { return &GetTool{Repository: repository} }
func NewCreateTool(repository ports.PlanRepository) *CreateTool {
	return &CreateTool{Repository: repository}
}
func NewUpdateStepTool(repository ports.PlanRepository) *UpdateStepTool {
	return &UpdateStepTool{Repository: repository}
}

func (t *GetTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: "plan_get", Label: "讀取工作計畫", Version: "1.0.0", Category: "planning",
		Description: "讀取目前 Session 的全部結構化計畫與步驟狀態；是否只能執行 current_plan 取決於 Session 的鎖定計畫設定。",
		Platforms:   []string{"darwin", "linux", "windows"}, Capabilities: []string{"planning", "progress-tracking"}, ReadOnly: true,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *GetTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "plan repository is unavailable"), nil
	}
	values, err := t.Repository.Reconcile(ctx, invocation.Session.ID, invocation.Session.LockPlans)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	result := map[string]any{"plans": values, "current_plan": activePlan(values)}
	if !invocation.Session.LockPlans {
		result["active_plans"] = activePlans(values)
	}
	return jsonExecution(invocation.Call, result)
}

func (t *CreateTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: "plan_create", Label: "建立工作計畫", Version: "1.0.0", Category: "planning",
		Description: "把獨立的長任務新增到計畫佇列尾端，並拆成可依序執行與驗證的步驟。每一步都必須提供可由工具確認的驗證條件；不要為同一任務重複建立計畫。",
		Platforms:   []string{"darwin", "linux", "windows"}, Capabilities: []string{"planning", "decomposition", "verification-contract"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":     map[string]any{"type": "string"},
				"objective": map[string]any{"type": "string"},
				"steps": map[string]any{
					"type": "array", "minItems": 1, "maxItems": domain.MaxPlanSteps,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"},
							"verification": map[string]any{"type": "string", "description": "完成後要用什麼客觀結果確認成功"},
						},
						"required": []string{"title", "verification"},
					},
				},
			},
			"required": []string{"title", "steps"},
		},
	}
}

func (t *CreateTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "plan repository is unavailable"), nil
	}
	if _, err := t.Repository.Reconcile(ctx, invocation.Session.ID, invocation.Session.LockPlans); err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	stepsData, err := json.Marshal(invocation.Call.Arguments["steps"])
	if err != nil {
		return failure(invocation.Call, "steps are invalid"), nil
	}
	var steps []domain.CreatePlanStepInput
	if err := json.Unmarshal(stepsData, &steps); err != nil {
		return failure(invocation.Call, "steps are invalid: "+err.Error()), nil
	}
	value, err := domain.NewPlan(invocation.Session.ID, domain.CreatePlanInput{
		Title: stringArgument(invocation.Call.Arguments, "title"), Objective: stringArgument(invocation.Call.Arguments, "objective"),
		CreatedBy: domain.PlanCreatedByAgent, Steps: steps,
	}, time.Now())
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	value, err = t.Repository.Create(ctx, value)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	values, err := t.Repository.Reconcile(ctx, invocation.Session.ID, invocation.Session.LockPlans)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	value, err = findPlan(values, value.ID)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	nextAction := "available for selection with plan_id"
	if invocation.Session.LockPlans {
		nextAction = "queued behind the current active plan"
	}
	if value.Status == domain.PlanStatusActive {
		nextAction = "start the first pending step"
	}
	return jsonExecution(invocation.Call, map[string]any{"plan": value, "next_action": nextAction})
}

func (t *UpdateStepTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: "plan_step_update", Label: "更新計畫步驟", Version: "1.0.0", Category: "planning",
		Description: "更新指定計畫的步驟；鎖定計畫時未提供 plan_id 會使用 current_plan，未鎖定時建議明確指定 plan_id。合法流程是 pending→in_progress→verifying→completed。",
		Platforms:   []string{"darwin", "linux", "windows"}, Capabilities: []string{"progress-tracking", "verification-gate"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan_id":  map[string]any{"type": "string", "description": "目標計畫 ID；省略時使用 current_plan"},
				"step_id":  map[string]any{"type": "string"},
				"status":   map[string]any{"type": "string", "enum": []string{"in_progress", "verifying", "completed", "blocked", "skipped"}},
				"evidence": map[string]any{"type": "string", "description": "completed 時填入工具驗證結果；blocked/skipped 時填入原因"},
			},
			"required": []string{"step_id", "status"},
		},
	}
}

func (t *UpdateStepTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Repository == nil {
		return failure(invocation.Call, "plan repository is unavailable"), nil
	}
	planID := stringArgument(invocation.Call.Arguments, "plan_id")
	if planID == "" {
		values, err := t.Repository.Reconcile(ctx, invocation.Session.ID, invocation.Session.LockPlans)
		if err != nil {
			return failure(invocation.Call, err.Error()), nil
		}
		current := activePlan(values)
		if current == nil {
			return failure(invocation.Call, "no active plan is available"), nil
		}
		planID = current.ID
	}
	value, err := t.Repository.Get(ctx, invocation.Session.ID, planID)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	value, err = domain.TransitionPlanStep(value, stringArgument(invocation.Call.Arguments, "step_id"), domain.UpdatePlanStepInput{
		Status: domain.PlanStepStatus(stringArgument(invocation.Call.Arguments, "status")), Evidence: stringArgument(invocation.Call.Arguments, "evidence"),
	}, time.Now())
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	value, err = t.Repository.Update(ctx, value)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	values, err := t.Repository.Reconcile(ctx, invocation.Session.ID, invocation.Session.LockPlans)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	value, err = findPlan(values, value.ID)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	return jsonExecution(invocation.Call, map[string]any{"plan": value})
}

func stringArgument(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

func activePlan(values []domain.Plan) *domain.Plan {
	for index := range values {
		if values[index].Status == domain.PlanStatusActive {
			return &values[index]
		}
	}
	return nil
}

func activePlans(values []domain.Plan) []domain.Plan {
	result := make([]domain.Plan, 0, len(values))
	for _, value := range values {
		if value.Status == domain.PlanStatusActive {
			result = append(result, value)
		}
	}
	return result
}

func findPlan(values []domain.Plan, planID string) (domain.Plan, error) {
	for _, value := range values {
		if value.ID == strings.TrimSpace(planID) {
			return value, nil
		}
	}
	return domain.Plan{}, fmt.Errorf("%w: plan %q", domain.ErrNotFound, planID)
}

func jsonExecution(call domain.ToolCall, value any) (domain.ToolExecution, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return domain.ToolExecution{}, fmt.Errorf("encode plan result: %w", err)
	}
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: string(data), Details: map[string]any{"plan_changed": call.Name != "plan_get"}}, nil
}

func failure(call domain.ToolCall, message string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), IsError: true}
}
