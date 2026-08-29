package ports

import (
	"AgenticService/src/domain"
	"context"
)

type ModelEventSink func(domain.ModelEvent) error

// Model 隔離各家 Provider 與 Harness。Harness 只接收文字、ToolCall 與使用量，
// 不知道底層使用原生 function calling 或文字協定。
type Model interface {
	Stream(context.Context, domain.ModelRequest, ModelEventSink) (domain.ModelResponse, error)
}

// ProviderUsageSource 由能取得上游配額視窗的 adapter 實作。
// 沒有用量資料時回傳 Available=false，不以猜測值取代。
type ProviderUsageSource interface {
	ProviderUsage() domain.ProviderUsage
}

// ProviderUsageRefresher 由能主動查詢上游配額的 adapter 實作。
// 實作不得用會消耗模型額度的推理請求模擬用量查詢。
type ProviderUsageRefresher interface {
	RefreshProviderUsage(context.Context) error
}

// ModelCatalog 回報特定 provider/model 的宣告限制，讓 context 預算依實際使用的模型計算。
// Workspace、Session 與 Run 三層都可以覆寫 model，因此預算不能綁在單一全域設定值。
type ModelCatalog interface {
	Capabilities(providerID, model string) domain.ModelCapabilities
}

// ProviderCatalog 是可路由多種 Provider adapter 的 Model；Harness 仍只依賴 Model。
// 新 Provider 類型只需在 bootstrap 工廠註冊 adapter，不需修改 Workspace 或 Harness。
type ProviderCatalog interface {
	Model
	ModelCatalog
	DefaultProviderID() string
	HasProvider(string) bool
	ListProviders() []domain.ProviderDescriptor
}
