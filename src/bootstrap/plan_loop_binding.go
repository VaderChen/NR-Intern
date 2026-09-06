package bootstrap

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"sync/atomic"
)

// planLoopBinding 讓工具在 Service 建立之前就能拿到控制器。
//
// 原生工具在 runtime 早期就要組好，Service 卻要等儲存層全部就緒才建得出來。
// 與其為此調整整個啟動順序（那會牽動很多不相干的東西），不如給工具一個
// 之後才填上的間接層；沒填之前所有方法都回「沒有多輪」，行為與未啟用相同。
type planLoopBinding struct {
	controller atomic.Pointer[ports.PlanLoopController]
}

var _ ports.PlanLoopController = (*planLoopBinding)(nil)

func (b *planLoopBinding) bind(controller ports.PlanLoopController) {
	if b == nil || controller == nil {
		return
	}
	b.controller.Store(&controller)
}

func (b *planLoopBinding) ActivePlanLoop(ctx context.Context, sessionID string) (domain.Plan, bool) {
	if b == nil {
		return domain.Plan{}, false
	}
	if controller := b.controller.Load(); controller != nil {
		return (*controller).ActivePlanLoop(ctx, sessionID)
	}
	return domain.Plan{}, false
}

func (b *planLoopBinding) InterruptPlanLoop(ctx context.Context, sessionID, planID string, by domain.PlanLoopStopReason, reason, note string) (domain.Plan, error) {
	if b == nil {
		return domain.Plan{}, domain.ErrNotFound
	}
	if controller := b.controller.Load(); controller != nil {
		return (*controller).InterruptPlanLoop(ctx, sessionID, planID, by, reason, note)
	}
	return domain.Plan{}, domain.ErrNotFound
}
