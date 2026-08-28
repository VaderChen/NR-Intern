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
