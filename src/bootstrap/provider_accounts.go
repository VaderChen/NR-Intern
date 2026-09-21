package bootstrap

import (
	"AgenticService/src/domain"
	"AgenticService/src/modelrouter"
	"context"
	"fmt"
	"log/slog"
	"strings"
)

func (r *Runtime) ProviderOAuthFileAccounts(ctx context.Context, providerID string, data []byte) ([]domain.ProviderOAuthFileAccount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, _, err := r.providerOAuthConfiguration(providerID); err != nil {
		return nil, err
	}
	if r.ProviderAuth == nil {
		return nil, fmt.Errorf("%w: OAuth 無法使用", domain.ErrInvalidInput)
	}
	accounts, err := r.ProviderAuth.FileAccounts(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	result := make([]domain.ProviderOAuthFileAccount, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, domain.ProviderOAuthFileAccount{ID: account.ID, Email: account.Email, Name: account.Name, ExpiresAt: account.ExpiresAt, Problem: account.Problem})
	}
	return result, nil
}

func (r *Runtime) ImportProviderOAuthFileAccount(ctx context.Context, providerID string, data []byte, selectionID string) (domain.ProviderOAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProviderOAuthStatus{}, err
	}
	id, _, err := r.providerOAuthConfiguration(providerID)
	if err != nil {
		return domain.ProviderOAuthStatus{}, err
	}
	if r.ProviderAuth == nil {
		return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: OAuth 無法使用", domain.ErrInvalidInput)
	}
	// 匯入寫入期間固定 Provider 類型，避免另一個管理請求刪除或改型後仍留下憑證。
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	provider, exists := r.Config.Providers[id]
	if !exists || !strings.EqualFold(strings.TrimSpace(provider.Type), "openai-codex-responses") || provider.OpenAICodexResponses == nil {
		return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: Provider 已變更，請重新選擇", domain.ErrConflict)
	}
	// 先建立替換用的 adapter，格式錯誤時不寫入憑證。只替換本次 Provider，
	// 不沿用上一個帳號的配額、重置額度或模型快取，也不清除其他 Provider 的快取。
	var replacement modelrouter.Provider
	if r.Model != nil && provider.IsEnabled() {
		if !r.Model.HasProvider(id) {
			return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: Provider 已變更", domain.ErrConflict)
		}
		config := r.Config
		config.Providers = map[string]ProviderConfig{id: provider}
		logger := r.logger
		if logger == nil {
			logger = slog.Default()
		}
		values, err := buildProviderValues(config, logger, r.ProviderAuth)
		if err != nil {
			return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: 無法準備 Provider", domain.ErrInvalidInput)
		}
		replacement = values[id]
	}
	status, err := r.ProviderAuth.ImportFileAccount(id, data, selectionID)
	if err != nil {
		return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	if replacement.Model != nil {
		if err := r.Model.ReplaceProvider(id, replacement); err != nil {
			return domain.ProviderOAuthStatus{}, fmt.Errorf("%w: 帳號已儲存，但 Provider 更新失敗", domain.ErrConflict)
		}
	}
	return domain.ProviderOAuthStatus{ProviderID: status.ProviderID, Status: status.Status, AccountEmail: status.AccountEmail, AccountName: status.AccountName, ExpiresAt: status.ExpiresAt}, nil
}
