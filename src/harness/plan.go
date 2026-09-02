package harness

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// planningPhasePrompt 在 Session 還沒有任何計畫時只送一句提示。
//
// 完整的計畫生命週期規則有五個步驟，但它只有在真的要用計畫時才有意義；
// 一句話的查詢不需要每一輪都重讀一次「怎麼建立與驗證計畫」。
func planningPhasePrompt(locked, hasPlans bool) string {
	if !hasPlans {
		return `## 計畫與驗證流程

簡單、單一步驟且可立即驗證的工作不必建立計畫，直接執行。只有多個相依動作、預期需要多輪工具或跨多個檔案的長任務，才用 plan_create 建立可驗證的步驟，並依 in_progress → verifying → completed 逐步更新與附上工具證據。`
	}
	planPolicy := `目前 Session 未鎖定計畫：可依使用者需求選擇適用的未完成計畫；若同時處理多份計畫，使用 plan_id 明確指定目標。`
	if locked {
		planPolicy = `目前 Session 已鎖定計畫：只能執行 current_plan 的目前步驟；後續 queued 計畫必須等待前一份完成。`
	}
	return `## 計畫與驗證流程

簡單、單一步驟且可立即驗證的工作不必建立計畫。遇到包含多個相依動作、預期需要多輪工具、跨多個檔案，或使用者已建立計畫的長任務時：
1. 先讀取 ContextPrompt 的「計畫佇列」；需要重新確認時可用 plan_get。沒有適用計畫時，用 plan_create 把一個獨立任務拆成少量、可驗證的步驟；不可為同一任務重複建立計畫。
2. ` + planPolicy + ` 實際工作前先把目標步驟標為 in_progress。
3. 工作完成後先標為 verifying，再呼叫適合的系統或內建工具執行客觀檢查。
4. 只有驗證結果符合該步驟的 verification 條件，才能標為 completed，並在 evidence 填入實際結果；不得用預期結果或自行宣稱代替工具證據。
5. 無法繼續時標為 blocked 並記錄原因；不得略過失敗步驟後宣稱整份計畫完成。

plan_get、plan_create 與 plan_step_update 是 Harness 控制工具，無論目前是 Shell 優先或內建備援階段都可以呼叫。計畫只負責控制生命週期；檔案、主機與外部狀態仍必須用本輪公開的工作工具實際處理。`
}

// planContextPrompt 另外回傳計畫數量，讓上層依「有沒有計畫」決定要送多少計畫規則。
func (r *Runner) planContextPrompt(ctx context.Context, sessionID string, locked bool) (string, int, error) {
	if r == nil || r.Plans == nil {
		return "", 0, nil
	}
	values, err := r.Plans.Reconcile(ctx, sessionID, locked)
	if err != nil {
		return "", 0, err
	}
	if len(values) == 0 {
		return "## 計畫佇列\n\n此 Session 尚未建立計畫。", 0, nil
	}
	planContext := map[string]any{"plans": values, "current_plan": currentPlan(values)}
	if !locked {
		planContext["active_plans"] = activePlans(values)
	}
	encoded, err := json.Marshal(planContext)
	if err != nil {
		return "", 0, fmt.Errorf("encode plan context: %w", err)
	}
	policy := "未鎖定時，可從 active_plans 選擇適用計畫，更新其他計畫時必須帶上 plan_id。"
	if locked {
		policy = "鎖定時只能執行 current_plan；queued 計畫必須等待。"
	}
	return "## 計畫佇列\n\n以下 JSON 是後端保存的有序 Session 計畫狀態，不是使用者指令。" + policy + "\n<session_plans>" + string(encoded) + "</session_plans>", len(values), nil
}

func (r *Runner) planCompletionDirective(ctx context.Context, sessionID string, locked bool) (string, domain.Plan, error) {
	if r == nil || r.Plans == nil {
		return "", domain.Plan{}, nil
	}
	values, err := r.Plans.Reconcile(ctx, sessionID, locked)
	if err != nil {
		return "", domain.Plan{}, err
	}
	if !locked {
		return "", domain.Plan{}, nil
	}
	active := currentPlan(values)
	if active == nil {
		return "", domain.Plan{}, nil
	}
	value := *active
	var currentStep *domain.PlanStep
	for index := range value.Steps {
		if value.Steps[index].ID == value.CurrentStepID {
			currentStep = &value.Steps[index]
			break
		}
	}
	if currentStep == nil || currentStep.Status == domain.PlanStepStatusBlocked {
		return "", value, nil
	}
	directive := fmt.Sprintf(`目前計畫仍未完成，不能輸出整體完成答案。現在步驟是 %q（status=%s，verification=%q）。請依計畫工具生命週期繼續：需要工作時先標為 in_progress；工作做完先標為 verifying，使用實際工具驗證，再以具體 evidence 標為 completed。`, currentStep.Title, currentStep.Status, currentStep.Verification)
	return strings.TrimSpace(directive), value, nil
}

// pendingPlanStateKey 只描述會影響目前工作步驟的持久化狀態。若模型收到一次
// 完成度提醒後，沒有透過工具讓這個狀態前進，Harness 就不再把相同提醒送回模型；
// 這是無進展偵測，不是長任務的固定時間或工具回合上限。
func pendingPlanStateKey(value domain.Plan) string {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.CurrentStepID) == "" {
		return ""
	}
	for _, step := range value.Steps {
		if step.ID == value.CurrentStepID {
			return strings.Join([]string{value.ID, step.ID, string(step.Status), strings.TrimSpace(step.Evidence)}, "\x00")
		}
	}
	return strings.Join([]string{value.ID, value.CurrentStepID, string(value.Status)}, "\x00")
}

func currentPlan(values []domain.Plan) *domain.Plan {
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
