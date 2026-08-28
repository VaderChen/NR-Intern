package ports

import (
	"AgenticService/src/domain"
	"context"
	"io"
)

// AttachmentRepository 將對話附件限制在 Session 私有工作目錄。
// maxBytes 必須由儲存層再次執行，不能只相信 HTTP Content-Length。
type AttachmentRepository interface {
	Save(ctx context.Context, sessionID, name, mediaType string, source io.Reader, maxBytes int64) (domain.Attachment, error)
	Get(ctx context.Context, sessionID, attachmentID string) (domain.Attachment, error)
	Delete(ctx context.Context, sessionID, attachmentID string) error
}
