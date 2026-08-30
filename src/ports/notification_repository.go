package ports

import (
	"AgenticService/src/domain"
	"context"
)

type NotificationRepository interface {
	Save(context.Context, domain.Notification) error
	List(context.Context, int, bool) ([]domain.Notification, error)
	MarkRead(context.Context, string) error
	MarkAllRead(context.Context) error
	DeleteRead(context.Context) error
}
