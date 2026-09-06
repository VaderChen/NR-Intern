package ports

import (
	"AgenticService/src/domain"
	"context"
)

// PlanLoopController 讓工具層能查詢與中斷多輪執行。
//
// 做成 port 而不是直接給 Service：工具只需要「有沒有在多輪」與「立刻中止」
// 兩件事，把整個 Service 交出去會讓工具能做遠超過必要的事。
type PlanLoopController interface {
	// ActivePlanLoop 回傳這個 Session 正在多輪執行的計畫。
	ActivePlanLoop(ctx context.Context, sessionID string) (domain.Plan, bool)
	// InterruptPlanLoop 立刻中止當前這一輪並留下檢查點。
	InterruptPlanLoop(ctx context.Context, sessionID, planID string, by domain.PlanLoopStopReason, reason, note string) (domain.Plan, error)
}
