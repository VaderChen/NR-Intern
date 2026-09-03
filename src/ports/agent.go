package ports

import (
	"AgenticService/src/domain"
	"context"
)

type AgentEventSink func(domain.EngineEvent) error

// AgentEngine 是新 Agent Application 唯一認得的推理引擎介面。
// 舊 chatCore 或未來的新引擎，都必須透過 Adapter 實作這個介面。
type AgentEngine interface {
	Descriptor() domain.AgentDescriptor
	CreateSession(context.Context, domain.CreateSessionInput) (domain.Session, error)
	ListSessions(context.Context) ([]domain.Session, error)
	GetSession(context.Context, string) (domain.Session, error)
	UpdateSession(context.Context, string, domain.UpdateSessionInput) (domain.Session, error)
	SetPermanentToolApproval(context.Context, string, bool) (domain.Session, error)
	DeleteSession(context.Context, string) error
	ListMessages(context.Context, string) ([]domain.Message, error)
	// RetractMessages 讓「重新提問」把某一則使用者訊息之後的內容移出對話。
	RetractMessages(context.Context, string, string) ([]domain.Message, error)
	ListEntries(context.Context, string) ([]domain.SessionEntry, error)
	// ListEntriesPage 讓分頁在儲存層生效，不必為了一頁載入整份 transcript。
	ListEntriesPage(context.Context, string, int64, int) ([]domain.SessionEntry, bool, error)
	Run(context.Context, domain.RunInput, AgentEventSink) (domain.RunResult, error)
}
