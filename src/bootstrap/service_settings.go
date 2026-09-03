package bootstrap

import (
	"AgenticService/src/domain"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const serviceSettingsFilename = "service-settings.json"

type storedServiceSettings struct {
	ServiceName          string `json:"service_name"`
	UILanguage           string `json:"ui_language,omitempty"`
	NotificationsEnabled *bool  `json:"notifications_enabled,omitempty"`
	MaxWallClockSeconds  *int   `json:"max_wall_clock_seconds,omitempty"`
	MaxTokens            *int   `json:"max_tokens,omitempty"`
	MaxToolCalls         *int   `json:"max_tool_calls,omitempty"`
	// 指標型別讓「設定檔提供、管理介面沒動過」與「管理介面明確關閉」可以區分。
	HTTPFetchEnabled              *bool   `json:"http_fetch_enabled,omitempty"`
	HTTPFetchAllowPrivateNetworks *bool   `json:"http_fetch_allow_private_networks,omitempty"`
	ExtendedTools                 *bool   `json:"extended_tools,omitempty"`
	ToolCallMode                  *string `json:"tool_call_mode,omitempty"`
	ToolRetrieval                 *bool   `json:"tool_retrieval,omitempty"`
	// LegacyMCPToolRetrieval 是 ToolRetrieval 的舊名稱，只讀不寫。
	LegacyMCPToolRetrieval *bool `json:"mcp_tool_retrieval,omitempty"`
	MemorySpace            *bool `json:"memory_space,omitempty"`
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
	if stored.ServiceName != "" {
		config.ServiceName = stored.ServiceName
		if config.ServiceName == legacyDefaultServiceName {
			config.ServiceName = DefaultServiceName
		}
	}
	if stored.UILanguage != "" {
		config.UILanguage = stored.UILanguage
	}
	if stored.NotificationsEnabled != nil {
		config.NotificationsEnabled = *stored.NotificationsEnabled
	}
	if stored.MaxWallClockSeconds != nil {
		config.MaxWallClockSeconds = *stored.MaxWallClockSeconds
	}
	if stored.MaxTokens != nil {
		config.MaxTokens = *stored.MaxTokens
	}
	if stored.MaxToolCalls != nil {
		config.MaxToolCalls = *stored.MaxToolCalls
	}
	if stored.HTTPFetchEnabled != nil {
		config.HTTPFetch.Enabled = *stored.HTTPFetchEnabled
	}
	if stored.HTTPFetchAllowPrivateNetworks != nil {
		config.HTTPFetch.AllowPrivateNetworks = *stored.HTTPFetchAllowPrivateNetworks
	}
	if stored.ExtendedTools != nil {
		config.ExtendedTools = *stored.ExtendedTools
	}
	if stored.ToolCallMode != nil {
		config.ToolCallMode = *stored.ToolCallMode
	}
	// 舊的 service-settings.json 沒有這個欄位，nil 會保留 DefaultConfig 的 true：
	// 升級上來的安裝不會突然變回整份目錄進提示的行為。
	if stored.LegacyMCPToolRetrieval != nil {
		config.ToolRetrieval = *stored.LegacyMCPToolRetrieval
	}
	if stored.ToolRetrieval != nil {
		config.ToolRetrieval = *stored.ToolRetrieval
	}
	if stored.MemorySpace != nil {
		config.MemorySpace = *stored.MemorySpace
	}
	return nil
}

func persistServiceSettings(dataDir string, settings domain.ServiceSettings) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create service settings directory: %w", err)
	}
	data, err := json.MarshalIndent(storedServiceSettings{
		ServiceName:          settings.ServiceName,
		UILanguage:           settings.UILanguage,
		NotificationsEnabled: boolPointer(settings.NotificationsEnabled),
		MaxWallClockSeconds:  intPointer(settings.MaxWallClockSeconds),
		MaxTokens:            intPointer(settings.MaxTokens),
		MaxToolCalls:         intPointer(settings.MaxToolCalls),

		HTTPFetchEnabled:              boolPointer(settings.HTTPFetch.Enabled),
		HTTPFetchAllowPrivateNetworks: boolPointer(settings.HTTPFetch.AllowPrivateNetworks),
		ExtendedTools:                 boolPointer(settings.ExtendedTools),
		ToolCallMode:                  stringPointer(settings.ToolCallMode),
		ToolRetrieval:                 boolPointer(settings.ToolRetrieval),
		MemorySpace:                   boolPointer(settings.MemorySpace),
	}, "", "  ")
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

func stringPointer(value string) *string {
	return &value
}

func intPointer(value int) *int {
	return &value
}
