package plans

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"context"
	"strings"
)

// InterruptTool 是 Agent 唯一能對多輪執行下的指令。
//
// 刻意只有「中斷」沒有「完成」：完成的唯一路徑是把每個步驟做到 completed
// 並附上證據。若給 Agent 一個宣告完成的工具，中斷就會變成「我覺得差不多了」
// 的後門，而那正是這個功能要防的走鐘。
type InterruptTool struct{ Controller ports.PlanLoopController }

func NewInterruptTool(controller ports.PlanLoopController) *InterruptTool {
	return &InterruptTool{Controller: controller}
}

func (t *InterruptTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: "plan_loop_interrupt", Label: "中止 LOOP", Version: "1.0.0", Category: "planning",
		Description: "在 LOOP 中判斷再做下去沒有意義時，立刻中止這一次。" +
			"適用於：需要使用者決定才能繼續、發現自己在重複同樣的嘗試、或前提已經不成立。" +
			"必須寫下做到哪裡（state_note），使用者續跑時會原樣交還給你，你要從那裡接續而不是重來。" +
			"這不是宣告完成——完成的唯一方式是把每個步驟做到 completed 並附上證據。",
		Platforms: []string{"darwin", "linux", "windows"},
		// 它減少而非增加副作用（停止繼續花錢），要求人工核准會讓「停不下來」變成常態。
		ReadOnly:     true,
		Capabilities: []string{"loop-control", "checkpoint"},
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"reason", "state_note"},
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "為什麼現在該停，一句話講完。使用者會看到這句話",
				},
				"state_note": map[string]any{
					"type":        "string",
					"description": "你做到哪裡、下一步打算做什麼。續跑時會原樣交還給你",
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t *InterruptTool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	if t == nil || t.Controller == nil {
		return failure(invocation.Call, "plan loop control is unavailable"), nil
	}
	plan, active := t.Controller.ActivePlanLoop(ctx, invocation.Session.ID)
	if !active {
		return failure(invocation.Call, "這個對話目前沒有進行中的 LOOP"), nil
	}
	reason := strings.TrimSpace(stringArgument(invocation.Call.Arguments, "reason"))
	note := strings.TrimSpace(stringArgument(invocation.Call.Arguments, "state_note"))
	if reason == "" || note == "" {
		return failure(invocation.Call, "reason 與 state_note 都必填；少了交接內容，續跑就只能重新開始"), nil
	}
	updated, err := t.Controller.InterruptPlanLoop(ctx, invocation.Session.ID, plan.ID, domain.PlanLoopStopAgent, reason, note)
	if err != nil {
		return failure(invocation.Call, err.Error()), nil
	}
	return jsonExecution(invocation.Call, map[string]any{
		"interrupted_round": updated.Loop.Round,
		"rounds_left":       updated.Loop.RoundsLeft(),
		"resumable":         updated.Loop.Resumable(),
	})
}
