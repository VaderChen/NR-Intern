package modelrouter

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

type Provider struct {
	Descriptor domain.ProviderDescriptor
	Model      ports.Model
	// Limits 讓 adapter 宣告個別模型的限制。留空時退回 Descriptor 的 Provider 預設值，
	// 由呼叫端自行決定未知限制的處理方式。
	Limits func(model string) domain.ModelCapabilities
}

type Router struct {
	mu        sync.RWMutex
	defaultID string
	providers map[string]Provider
}

var _ ports.ProviderCatalog = (*Router)(nil)

func New(defaultID string, values map[string]Provider) (*Router, error) {
	defaultID = strings.TrimSpace(defaultID)
	if defaultID == "" {
		return nil, fmt.Errorf("%w: default provider id is required", domain.ErrInvalidInput)
	}
	providers := make(map[string]Provider, len(values))
	for id, value := range values {
		id = strings.TrimSpace(id)
		if id == "" || value.Model == nil {
			return nil, fmt.Errorf("%w: provider id and model are required", domain.ErrInvalidInput)
		}
		value.Descriptor.ID = id
		if strings.TrimSpace(value.Descriptor.DisplayName) == "" {
			value.Descriptor.DisplayName = id
		}
		providers[id] = value
	}
	if _, exists := providers[defaultID]; !exists {
		return nil, fmt.Errorf("%w: default provider %q", domain.ErrNotFound, defaultID)
	}
	return &Router{defaultID: defaultID, providers: providers}, nil
}

// Replace 原子替換整組 Provider。已開始的請求仍持有舊 adapter，新的請求立即使用新設定。
func (r *Router) Replace(defaultID string, values map[string]Provider) error {
	replacement, err := New(defaultID, values)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.defaultID = replacement.defaultID
	r.providers = replacement.providers
	r.mu.Unlock()
	return nil
}

func (r *Router) Stream(ctx context.Context, request domain.ModelRequest, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	r.mu.RLock()
	providerID := strings.TrimSpace(request.ProviderID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return domain.ModelResponse{}, fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	request.ProviderID = providerID
	response, err := provider.Model.Stream(ctx, request, sink)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	response.ProviderID = providerID
	return response, nil
}

func (r *Router) DefaultProviderID() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultID
}

func (r *Router) HasProvider(providerID string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.providers[strings.TrimSpace(providerID)]
	return exists
}

// ProviderUsage 回傳指定 Provider 最近一次由上游明確提供的用量快照。
// 不支援或尚未收到資料的 Provider 仍回傳 provider_id，但兩個視窗皆為不可用。
// ConsumeRateLimitReset 兌換指定 Provider 的一次用量上限重置。
//
// 不支援這項功能的 Provider 回 ErrConflict 而不是靜默成功：使用者按了按鈕就會
// 期待有事發生，回報「這條路線沒有這功能」才是誠實的。
func (r *Router) ConsumeRateLimitReset(ctx context.Context, providerID, idempotencyKey string) (domain.ProviderResetResult, error) {
	if r == nil {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: provider router is unavailable", domain.ErrNotFound)
	}
	r.mu.RLock()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	resetter, ok := provider.Model.(ports.ProviderRateLimitResetter)
	if !ok {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: provider %q 不支援用量上限重置", domain.ErrConflict, providerID)
	}
	result, err := resetter.ConsumeRateLimitReset(ctx, idempotencyKey)
	if err != nil {
		return domain.ProviderResetResult{}, err
	}
	result.ProviderID = providerID
	return result, nil
}

func (r *Router) ProviderUsage(providerID string) (domain.ProviderUsage, error) {
	if r == nil {
		return domain.ProviderUsage{}, fmt.Errorf("%w: provider router is unavailable", domain.ErrNotFound)
	}
	r.mu.RLock()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return domain.ProviderUsage{}, fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	usage := domain.ProviderUsage{ProviderID: providerID}
	if source, ok := provider.Model.(ports.ProviderUsageSource); ok {
		usage = source.ProviderUsage()
		usage.ProviderID = providerID
	}
	return usage, nil
}

// RefreshProviderUsage 要求指定 Provider 透過專用唯讀端點更新配額快照。
func (r *Router) RefreshProviderUsage(ctx context.Context, providerID string) error {
	if r == nil {
		return fmt.Errorf("%w: provider router is unavailable", domain.ErrNotFound)
	}
	r.mu.RLock()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	refresher, ok := provider.Model.(ports.ProviderUsageRefresher)
	if !ok {
		return nil
	}
	return refresher.RefreshProviderUsage(ctx)
}

// ListProviderModels 使用目前實際路由中的 Provider adapter 讀取模型目錄。
// adapter 可同步保留後端回傳的模型限制，讓後續 Capabilities 與推理共用同一份資料。
func (r *Router) ListProviderModels(ctx context.Context, providerID string) ([]string, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: provider router is unavailable", domain.ErrNotFound)
	}
	r.mu.RLock()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	source, ok := provider.Model.(interface {
		ListModels(context.Context) ([]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("%w: provider %q does not expose a model catalog", domain.ErrInvalidInput, providerID)
	}
	return source.ListModels(ctx)
}

// Capabilities 回報實際會被使用的模型限制。ContextWindow 為 0 代表未宣告。
// DiscoverModelLimits 在背景向每個 Provider 的模型目錄問一次限制。
//
// context window 未宣告時，Harness 會退回設定檔的後備預算（預設 256K）——對本機
// 模型那是天文數字，壓縮門檻因此永遠碰不到，實測造成每輪 8 萬 token、prefill 二十
// 分鐘。多數 OpenAI-compatible 服務（LM Studio、vLLM、llama.cpp）會在 /v1/models
// 回報 context_length 或 max_model_len，問一次就能省去人工宣告。
//
// 問不到不是錯誤：OpenAI 本身與部分本機服務（例如 mlx-server）不回報這個欄位，
// 那些情況仍然需要在 Provider 設定裡填寫。因此這裡只記錄結果，不影響啟動。
func (r *Router) DiscoverModelLimits(ctx context.Context, logger *slog.Logger) {
	r.mu.RLock()
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if _, err := r.ListProviderModels(ctx, id); err != nil {
			if logger != nil {
				logger.Debug("model catalog probe failed", "provider_id", id, "error", err)
			}
			continue
		}
		if logger == nil {
			continue
		}
		capabilities := r.Capabilities(id, "")
		if capabilities.ContextWindow > 0 {
			logger.Info("model limits discovered from the provider",
				"provider_id", id,
				"context_window", capabilities.ContextWindow,
				"max_output_tokens", capabilities.MaxOutputTokens,
			)
			continue
		}
		logger.Warn("provider does not report a context window; declare it in the provider settings",
			"provider_id", id,
		)
	}
}

func (r *Router) Capabilities(providerID, model string) domain.ModelCapabilities {
	if r == nil {
		return domain.ModelCapabilities{}
	}
	r.mu.RLock()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = r.defaultID
	}
	provider, exists := r.providers[providerID]
	r.mu.RUnlock()
	if !exists {
		return domain.ModelCapabilities{}
	}
	if provider.Limits != nil {
		return provider.Limits(model)
	}
	return domain.ModelCapabilities{
		ContextWindow:   provider.Descriptor.ContextWindow,
		MaxOutputTokens: provider.Descriptor.MaxOutputTokens,
		Streaming:       provider.Descriptor.Streaming,
		SupportsTools:   true,
	}
}

func (r *Router) ListProviders() []domain.ProviderDescriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make([]domain.ProviderDescriptor, 0, len(r.providers))
	for _, provider := range r.providers {
		values = append(values, provider.Descriptor)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}
