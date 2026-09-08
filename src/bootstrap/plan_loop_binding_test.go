package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"strings"
	"testing"
)

// plan_loop_interrupt 是 Agent 唯一能碰 LOOP 的入口，而它要能動，
// 完全取決於 runtime 尾端那一行 planLoops.bind(service)。
//
// 少了那一行不會有編譯錯誤，也不會有任何錯誤日誌：binding 未填時所有方法
// 都回「沒有 LOOP」，工具只會安靜地回一句沒有進行中的 LOOP，看起來像
// Agent 判斷錯誤。這個測試從真的 runtime 建到工具呼叫，把那一行釘住。
func TestPlanLoopControllerIsBoundForTheInterruptTool(t *testing.T) {
	config := DefaultConfig()
	config.RAMDisk.Enabled = false
	config.DataDir = t.TempDir()
	runtime, err := Build(config)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	ctx := context.Background()
	workspace, err := runtime.Application.CreateWorkspace(ctx, domain.CreateWorkspaceInput{
		Name: "loop", ProviderIDs: []string{"openai-compatible"}, DefaultProviderID: "openai-compatible",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	session, err := runtime.Application.CreateSession(ctx, "general-agent",
		domain.CreateSessionInput{Title: "接線", WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	plan, err := runtime.Application.CreatePlan(ctx, session.ID, domain.CreatePlanInput{
		Title: "接線", Objective: "確認工具接得上控制器",
		Steps: []domain.CreatePlanStepInput{{Title: "步驟一", Verification: "條件一"}},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if _, err := runtime.Application.StartPlanLoop(ctx, session.ID, plan.ID, 3); err != nil {
		t.Fatalf("StartPlanLoop: %v", err)
	}

	execution, err := runtime.NativeTools.Execute(ctx, session, domain.ToolCall{
		ID: "call_interrupt", Name: "plan_loop_interrupt",
		Arguments: map[string]any{"reason": "需要使用者確認", "state_note": "步驟一的前置檢查已完成"},
	}, nil)
	if err != nil {
		t.Fatalf("Execute plan_loop_interrupt: %v", err)
	}
	if execution.IsError {
		t.Fatalf("工具回報失敗，控制器很可能沒有綁上：%s", execution.Content)
	}

	values, err := runtime.Application.ListPlans(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	var updated domain.Plan
	for _, value := range values {
		if value.ID == plan.ID {
			updated = value
		}
	}
	if updated.Loop == nil || updated.Loop.Status != domain.PlanLoopStatusPaused {
		t.Fatalf("中斷後 LOOP 應為暫停，實際 = %+v", updated.Loop)
	}
	if updated.Loop.Checkpoint == nil || !strings.Contains(updated.Loop.Checkpoint.Note, "前置檢查已完成") {
		t.Fatalf("交接內容沒有留下來：%+v", updated.Loop.Checkpoint)
	}
}
