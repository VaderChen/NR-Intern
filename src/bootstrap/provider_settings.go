package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const providerSettingsFilename = "providers.json"

type storedProviderSettings struct {
	DefaultProviderID string                    `json:"default_provider_id"`
	Providers         map[string]ProviderConfig `json:"providers"`
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
	return nil
}

func persistProviderSettings(dataDir, defaultProviderID string, providers map[string]ProviderConfig) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create provider settings directory: %w", err)
	}
	value := storedProviderSettings{DefaultProviderID: defaultProviderID, Providers: providers}
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
