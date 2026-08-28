package ports

import (
	"AgenticService/src/domain"
	"context"
)

type RunRepository interface {
	Save(context.Context, domain.Run) error
	Get(context.Context, string) (domain.Run, error)
	List(context.Context, string) ([]domain.Run, error)
	FindByIdempotencyKey(context.Context, string, string) (domain.Run, error)
}
