package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const maxModelCatalogBytes = 4 * 1024 * 1024

// ListModels 讀取 OpenAI-compatible 的模型目錄。
// 除了標準 data 陣列，也接受部分相容服務使用的 models 陣列。
func (m *Model) ListModels(ctx context.Context) ([]string, error) {
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("OpenAI-compatible model is unavailable")
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
	if m.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+m.apiKey)
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
	models, err := decodeModelCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("decode OpenAI-compatible model catalog: %w", err)
	}
	return models, nil
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

func decodeModelCatalog(data []byte) ([]string, error) {
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
			return nil, err
		}
		catalog = envelope.Data
		if len(catalog) == 0 || string(catalog) == "null" {
			catalog = envelope.Models
		}
	}
	if len(catalog) == 0 || string(catalog) == "null" {
		return []string{}, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(catalog, &entries); err != nil {
		return nil, err
	}
	unique := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		var id string
		if err := json.Unmarshal(entry, &id); err != nil {
			var value struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(entry, &value); err != nil {
				continue
			}
			id = value.ID
			if strings.TrimSpace(id) == "" {
				id = value.Name
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
	return models, nil
}
