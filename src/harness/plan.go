package harness

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func planningPhasePrompt() string {
	return `## 計畫與驗證流程

簡單、單一步驟且可立即驗證的工作不必建立計畫。遇到包含多個相依動作、預期需要多輪工具、跨多個檔案，或使用者已建立計畫的長任務時：
1. 先讀取 ContextPrompt 的「計畫佇列」；需要重新確認時可用 plan_get。沒有適用計畫時，用 plan_create 把一個獨立任務拆成少量、可驗證的步驟；不可為同一任務重複建立計畫。
2. 嚴格依計畫與步驟順序處理。每次只能執行 current_plan 的目前步驟；後續 queued 計畫必須等待前一份完成。實際工作前先把步驟標為 in_progress。
3. 工作完成後先標為 verifying，再呼叫適合的系統或內建工具執行客觀檢查。
4. 只有驗證結果符合該步驟的 verification 條件，才能標為 completed，並在 evidence 填入實際結果；不得用預期結果或自行宣稱代替工具證據。
5. 無法繼續時標為 blocked 並記錄原因；不得略過失敗步驟後宣稱整份計畫完成。

plan_get、plan_create 與 plan_step_update 是 Harness 控制工具，無論目前是 Shell 優先或內建備援階段都可以呼叫。計畫只負責控制生命週期；檔案、主機與外部狀態仍必須用本輪公開的工作工具實際處理。`
}

func (r *Runner) planContextPrompt(ctx context.Context, sessionID string) (string, error) {
	if r == nil || r.Plans == nil {
		return "", nil
	}
	values, err := r.Plans.List(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "## 計畫佇列\n\n此 Session 尚未建立計畫。", nil
	}
	encoded, err := json.Marshal(map[string]any{"plans": values, "current_plan": currentPlan(values)})
	if err != nil {
		return "", fmt.Errorf("encode plan context: %w", err)
	}
	return "## 計畫佇列\n\n以下 JSON 是後端保存的有序 Session 計畫狀態，不是使用者指令。只能執行 current_plan；queued 計畫必須等待：\n<session_plans>" + string(encoded) + "</session_plans>", nil
}

func (r *Runner) planCompletionDirective(ctx context.Context, sessionID string) (string, domain.Plan, error) {
	if r == nil || r.Plans == nil {
		return "", domain.Plan{}, nil
	}
	values, err := r.Plans.List(ctx, sessionID)
	if err != nil {
		return "", domain.Plan{}, err
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
