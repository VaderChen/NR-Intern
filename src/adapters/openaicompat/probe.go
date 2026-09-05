package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
)

const (
	maxModelCatalogBytes      = 4 * 1024 * 1024
	maxCodexModelCatalogBytes = 8 * 1024 * 1024
)

const (
	codexModelsEndpoint = "https://chatgpt.com/backend-api/codex/models"
	// codexManifestClientVersion 決定上游回傳哪一份模型清單。
	//
	// manifest 是依 client version 分流的：版本太舊會拿到舊的清單，新模型不會出現，
	// 而且不會有任何錯誤——看起來就只是「模型沒有更新」。新模型上線後對不到時，
	// 這裡是第一個要檢查的地方。對照 Codex CLI 目前的版本更新即可。
	codexManifestClientVersion = "0.153.0"
)

// ListModels 讀取 OpenAI-compatible 的模型目錄。
// 除了標準 data 陣列，也接受部分相容服務使用的 models 陣列。
func (m *Model) ListModels(ctx context.Context) ([]string, error) {
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("OpenAI-compatible model is unavailable")
	}
	if m.authMode == "oauth" {
		return m.listCodexModels(ctx)
	}
	endpoint, err := modelCatalogEndpoint(m.endpoint)
	if err != nil {
		return nil, err
	}
	clientRequestID := domain.NewID("llmreq")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range m.extraHeaders {
		httpRequest.Header.Set(name, value)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "AgenticService/openai-compatible")
	if err := m.applyAuthorization(ctx, httpRequest); err != nil {
		return nil, &ProviderError{Operation: "authorization", Message: err.Error(), ClientRequestID: clientRequestID, Retryable: false, Cause: err}
	}
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return nil, &ProviderError{Operation: "model catalog", Message: err.Error(), ClientRequestID: clientRequestID, Retryable: ctx.Err() == nil, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr, _ := providerHTTPError(response, clientRequestID)
		providerErr.Operation = "model catalog"
		return nil, providerErr
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxModelCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read OpenAI-compatible model catalog: %w", err)
	}
	if len(data) > maxModelCatalogBytes {
		return nil, fmt.Errorf("OpenAI-compatible model catalog exceeds %d bytes", maxModelCatalogBytes)
	}
	models, limits, err := decodeModelCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("decode OpenAI-compatible model catalog: %w", err)
	}
	m.replaceReportedModelLimits(limits)
	return models, nil
}

// listCodexModels 讀取 ChatGPT 帳號專屬的 Codex model manifest。這不是
// OpenAI API 的 /v1/models，且必須附帶 OAuth Token、帳號 ID 與 client version。
func (m *Model) listCodexModels(ctx context.Context) ([]string, error) {
	endpoint, err := url.Parse(codexModelsEndpoint)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("client_version", codexManifestClientVersion)
	endpoint.RawQuery = query.Encode()
	clientRequestID := domain.NewID("llmreq")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	for name, value := range m.extraHeaders {
		httpRequest.Header.Set(name, value)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Originator", "codex-tui")
	httpRequest.Header.Set("Version", codexManifestClientVersion)
	httpRequest.Header.Set("User-Agent", fmt.Sprintf("codex-tui/%s (%s; %s)", codexManifestClientVersion, runtime.GOOS, runtime.GOARCH))
	httpRequest.Header.Set("X-Client-Request-Id", clientRequestID)
	if err := m.applyAuthorization(ctx, httpRequest); err != nil {
		return nil, &ProviderError{Operation: "Codex model catalog authorization", Message: err.Error(), ClientRequestID: clientRequestID, Retryable: false, Cause: err}
	}
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return nil, &ProviderError{Operation: "Codex model catalog", Message: err.Error(), ClientRequestID: clientRequestID, Retryable: ctx.Err() == nil, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr, _ := providerHTTPError(response, clientRequestID)
		providerErr.Operation = "Codex model catalog"
		return nil, providerErr
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCodexModelCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Codex model catalog: %w", err)
	}
	if len(data) > maxCodexModelCatalogBytes {
		return nil, fmt.Errorf("Codex model catalog exceeds %d bytes", maxCodexModelCatalogBytes)
	}
	models, limits, err := decodeCodexModelCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("decode Codex model catalog: %w", err)
	}
	m.replaceReportedModelLimits(limits)
	return models, nil
}

func decodeCodexModelCatalog(data []byte) ([]string, map[string]ModelLimits, error) {
	var manifest struct {
		Models []struct {
			Slug            string `json:"slug"`
			Visibility      string `json:"visibility,omitempty"`
			ContextWindow   int    `json:"context_window,omitempty"`
			MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, err
	}
	if len(manifest.Models) == 0 {
		return nil, nil, fmt.Errorf("model manifest is empty")
	}
	seen := make(map[string]struct{}, len(manifest.Models))
	models := make([]string, 0, len(manifest.Models))
	limits := make(map[string]ModelLimits, len(manifest.Models))
	for _, model := range manifest.Models {
		slug := strings.TrimSpace(model.Slug)
		key := strings.ToLower(slug)
		if slug == "" || key == "auto" || strings.EqualFold(strings.TrimSpace(model.Visibility), "hide") {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, slug)
		if model.ContextWindow > 0 || model.MaxOutputTokens > 0 {
			limits[slug] = ModelLimits{ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens}
		}
	}
	if len(models) == 0 {
		return nil, nil, fmt.Errorf("model manifest has no visible models")
	}
	return models, limits, nil
}

func modelCatalogEndpoint(completionURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(completionURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid OpenAI-compatible completion URL %q", completionURL)
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/chat/completions")
	parsed.Path = strings.TrimRight(path, "/") + "/models"
	return parsed.String(), nil
}

func decodeModelCatalog(data []byte) ([]string, map[string]ModelLimits, error) {
	var catalog json.RawMessage
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		catalog = json.RawMessage(data)
	} else {
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Models json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, nil, err
		}
		catalog = envelope.Data
		if len(catalog) == 0 || string(catalog) == "null" {
			catalog = envelope.Models
		}
	}
	if len(catalog) == 0 || string(catalog) == "null" {
		return []string{}, nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(catalog, &entries); err != nil {
		return nil, nil, err
	}
	unique := make(map[string]struct{}, len(entries))
	limits := make(map[string]ModelLimits, len(entries))
	for _, entry := range entries {
		var id string
		if err := json.Unmarshal(entry, &id); err != nil {
			var value struct {
				ID              string `json:"id"`
				Name            string `json:"name"`
				ContextWindow   int    `json:"context_window,omitempty"`
				ContextLength   int    `json:"context_length,omitempty"`
				MaxModelLength  int    `json:"max_model_len,omitempty"`
				MaxInputTokens  int    `json:"max_input_tokens,omitempty"`
				MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
			}
			if err := json.Unmarshal(entry, &value); err != nil {
				continue
			}
			id = value.ID
			if strings.TrimSpace(id) == "" {
				id = value.Name
			}
			contextWindow := firstPositive(value.ContextWindow, value.ContextLength, value.MaxModelLength, value.MaxInputTokens)
			if normalizedID := strings.TrimSpace(id); normalizedID != "" && (contextWindow > 0 || value.MaxOutputTokens > 0) {
				limits[normalizedID] = ModelLimits{ContextWindow: contextWindow, MaxOutputTokens: value.MaxOutputTokens}
			}
		}
		if id = strings.TrimSpace(id); id != "" {
			unique[id] = struct{}{}
		}
	}
	models := make([]string, 0, len(unique))
	for id := range unique {
		models = append(models, id)
	}
	sort.Strings(models)
	return models, limits, nil
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
