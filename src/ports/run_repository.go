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

// SessionRunUsageReader 讓明細有保留期限的儲存仍可提供完整用量；不要求其他
// Repository 為了相容而偽造歷史資料。
type SessionRunUsageReader interface {
	ListSessionRunUsage(context.Context, string) ([]domain.RunUsage, error)
}

type SessionRunDeleter interface {
	DeleteSession(context.Context, string) ([]string, error)
}

// RunRetentionGuard 區分「已寫終態」與「執行緒已完成收尾」。
type RunRetentionGuard interface {
	ProtectRun(string)
	ReleaseRun(string)
}
