package ports

import (
	"AgenticService/src/domain"
	"context"
)

type MemoryRepository interface {
	Remember(context.Context, domain.RememberMemoryInput) (domain.Memory, error)
	Get(context.Context, string, string) (domain.Memory, error)
	Search(context.Context, domain.MemoryQuery) ([]domain.Memory, error)
	Forget(context.Context, string, string, string) (domain.Memory, error)
}
