package ports

import (
	"AgenticService/src/domain"
	"context"
)

// RunEventRepository 是 Run 事件的 durable log。
// Append 必須拒絕同一 Run 不連續或重複的 sequence。
type RunEventRepository interface {
	Append(context.Context, domain.Event) error
	List(context.Context, string, int64) ([]domain.Event, error)
}

type RunEventDeleter interface {
	Delete(string) error
}

// SessionEventDeleter 從事件本身辨識歸屬，包含 Run 明細已淘汰的事件。
type SessionEventDeleter interface {
	DeleteSession(context.Context, string) error
}
