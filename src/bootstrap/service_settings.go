package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const serviceSettingsFilename = "service-settings.json"

type storedServiceSettings struct {
	ServiceName string `json:"service_name"`
}

func loadPersistedServiceSettings(config *Config) error {
	path := filepath.Join(config.DataDir, serviceSettingsFilename)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open service settings: %w", err)
	}
	var stored storedServiceSettings
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&stored)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode service settings: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close service settings: %w", closeErr)
	}
	config.ServiceName = stored.ServiceName
	return nil
}

func persistServiceSettings(dataDir, serviceName string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create service settings directory: %w", err)
	}
	data, err := json.MarshalIndent(storedServiceSettings{ServiceName: serviceName}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode service settings: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dataDir, serviceSettingsFilename)
	temporary, err := os.CreateTemp(dataDir, serviceSettingsFilename+".tmp.*")
	if err != nil {
		return fmt.Errorf("create service settings temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure service settings temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write service settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync service settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close service settings temporary file: %w", err)
	}
	if err := replaceConfigFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace service settings: %w", err)
	}
	return nil
}
