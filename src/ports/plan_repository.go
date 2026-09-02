package ports

import (
	"AgenticService/src/domain"
	"context"
)

type PlanRepository interface {
	List(context.Context, string) ([]domain.Plan, error)
	// Reconcile 依 Session 的計畫鎖定設定整理未完成計畫：鎖定時排隊，
	// 未鎖定時讓未完成計畫都可被 Agent 選取。
	Reconcile(context.Context, string, bool) ([]domain.Plan, error)
	Get(context.Context, string, string) (domain.Plan, error)
	Create(context.Context, domain.Plan) (domain.Plan, error)
	Update(context.Context, domain.Plan) (domain.Plan, error)
	Delete(context.Context, string, string) error
	DeleteSession(context.Context, string) error
	Reorder(context.Context, string, []string) ([]domain.Plan, error)
	ReorderWithPolicy(context.Context, string, []string, bool) ([]domain.Plan, error)
}
