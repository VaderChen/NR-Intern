package ports

import (
	"AgenticService/src/domain"
	"context"
)

type SessionRepository interface {
	Create(context.Context, string, domain.CreateSessionInput) (domain.Session, error)
	List(context.Context, string) ([]domain.Session, error)
	Get(context.Context, string) (domain.Session, error)
	Update(context.Context, domain.Session) error
	Delete(context.Context, string) error
	AppendEntry(context.Context, string, domain.SessionEntry) (domain.SessionEntry, error)
	ListEntries(context.Context, string) ([]domain.SessionEntry, error)
	ListMessages(context.Context, string) ([]domain.Message, error)

	// ListEntriesAfter 只回傳序號大於 afterSequence 的 entry。
	// 每個 turn 都重新解析整份 transcript 會讓長 session 變成 O(N²)，
	// 而被丟棄的多半是體積最大的工具輸出。
	ListEntriesAfter(context.Context, string, int64) ([]domain.SessionEntry, error)
	// LatestEntryOfType 回傳指定型別中序號最大的 entry，找不到時回傳 ErrNotFound。
	LatestEntryOfType(context.Context, string, string) (domain.SessionEntry, error)
}
