package ports

import (
	"AgenticService/src/domain"
	"context"
)

type PlanRepository interface {
	List(context.Context, string) ([]domain.Plan, error)
	Get(context.Context, string, string) (domain.Plan, error)
	Create(context.Context, domain.Plan) (domain.Plan, error)
	Update(context.Context, domain.Plan) (domain.Plan, error)
	Delete(context.Context, string, string) error
	DeleteSession(context.Context, string) error
	Reorder(context.Context, string, []string) ([]domain.Plan, error)
}
