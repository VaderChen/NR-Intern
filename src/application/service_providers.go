package application

import (
	"AgenticService/src/domain"
	"fmt"
	"strings"
)

func (s *Service) ListProviders() []domain.ProviderDescriptor {
	return s.providers.ListProviders()
}

// ProviderCapabilities 回傳指定 Provider／Model 實際套用的 context 與輸出限制。
// 個別模型覆寫由 ProviderCatalog 統一解析，前端不必複製模型名稱比對規則。
func (s *Service) ProviderCapabilities(providerID, model string) (domain.ModelCapabilities, error) {
	providerID = strings.TrimSpace(providerID)
	if err := s.validateProvider(providerID); err != nil {
		return domain.ModelCapabilities{}, err
	}
	return s.providers.Capabilities(providerID, strings.TrimSpace(model)), nil
}

func (s *Service) validateProvider(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	if !s.providers.HasProvider(providerID) {
		return fmt.Errorf("%w: provider %q", domain.ErrNotFound, providerID)
	}
	return nil
}

func (s *Service) defaultModelForProvider(providerID string) string {
	providerID = strings.TrimSpace(providerID)
	for _, provider := range s.providers.ListProviders() {
		if strings.TrimSpace(provider.ID) == providerID {
			return strings.TrimSpace(provider.DefaultModel)
		}
	}
	return ""
}
