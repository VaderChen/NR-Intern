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
