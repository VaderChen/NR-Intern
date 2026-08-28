package modelrouter

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
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

// Capabilities 回報實際會被使用的模型限制。ContextWindow 為 0 代表未宣告。
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
