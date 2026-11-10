package bootstrap

import (
	"AgenticService/src/domain"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const providerSettingsFilename = "providers.json"

type storedProviderSettings struct {
	DefaultProviderID string                    `json:"default_provider_id"`
	Providers         map[string]ProviderConfig `json:"providers"`
	// nil 表示舊版檔案，沿用基底價格；空 map 表示已明確清除所有價格。
	ModelPrices *map[string]map[string]domain.ModelPrice `json:"model_prices,omitempty"`
}

func loadPersistedProviderSettings(config *Config) error {
	path := filepath.Join(config.DataDir, providerSettingsFilename)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open provider settings: %w", err)
	}
	var stored storedProviderSettings
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&stored)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode provider settings: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close provider settings: %w", closeErr)
	}
	config.DefaultProviderID = stored.DefaultProviderID
	config.Providers = stored.Providers
	if stored.ModelPrices != nil {
		config.ModelPrices = *stored.ModelPrices
	}
	config.ModelPrices = modelPricesForProviders(config.ModelPrices, config.Providers)
	return nil
}

// 重新建立 map，驗證失敗或寫入失敗時不得提前改動使用中的設定。
func modelPricesForProviders(prices map[string]map[string]domain.ModelPrice, providers map[string]ProviderConfig) map[string]map[string]domain.ModelPrice {
	filtered := make(map[string]map[string]domain.ModelPrice)
	for id, models := range prices {
		if _, exists := providers[strings.TrimSpace(id)]; !exists {
			continue
		}
		copyModels := make(map[string]domain.ModelPrice, len(models))
		for model, price := range models {
			copyModels[model] = price
		}
		filtered[id] = copyModels
	}
	return filtered
}

func persistProviderSettings(dataDir, defaultProviderID string, providers map[string]ProviderConfig, prices map[string]map[string]domain.ModelPrice) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create provider settings directory: %w", err)
	}
	prices = modelPricesForProviders(prices, providers)
	value := storedProviderSettings{DefaultProviderID: defaultProviderID, Providers: providers, ModelPrices: &prices}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provider settings: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dataDir, providerSettingsFilename)
	temporary, err := os.CreateTemp(dataDir, providerSettingsFilename+".tmp.*")
	if err != nil {
		return fmt.Errorf("create provider settings temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure provider settings temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write provider settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync provider settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close provider settings temporary file: %w", err)
	}
	if err := replaceConfigFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace provider settings: %w", err)
	}
	return nil
}
