package modelrouter

import (
	"AgenticService/src/domain"
	"fmt"
	"strings"
)

// ReplaceProvider 只替換單一既有 Provider；舊請求仍持有舊 adapter，不能回填新帳號快取。
func (r *Router) ReplaceProvider(providerID string, value Provider) error {
	providerID = strings.TrimSpace(providerID)
	candidate, err := New(providerID, map[string]Provider{providerID: value})
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[providerID]; !exists {
		return fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	r.providers[providerID] = candidate.providers[providerID]
	return nil
}
