package bootstrap

import (
	"AgenticService/src/mcpclient"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const mcpSettingsFilename = "mcp-servers.json"

type storedMCPSettings struct {
	Servers map[string]mcpclient.ServerConfig `json:"servers"`
}

func loadPersistedMCPSettings(config *Config) error {
	path := filepath.Join(config.DataDir, mcpSettingsFilename)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open MCP settings: %w", err)
	}
	var stored storedMCPSettings
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&stored)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode MCP settings: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close MCP settings: %w", closeErr)
	}
	if stored.Servers == nil {
		stored.Servers = map[string]mcpclient.ServerConfig{}
	}
	config.MCPServers = stored.Servers
	return nil
}

func persistMCPSettings(dataDir string, servers map[string]mcpclient.ServerConfig) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create MCP settings directory: %w", err)
	}
	data, err := json.MarshalIndent(storedMCPSettings{Servers: servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode MCP settings: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(dataDir, mcpSettingsFilename+".tmp.*")
	if err != nil {
		return fmt.Errorf("create MCP settings temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure MCP settings temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write MCP settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync MCP settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close MCP settings temporary file: %w", err)
	}
	if err := replaceConfigFile(temporaryPath, filepath.Join(dataDir, mcpSettingsFilename)); err != nil {
		return fmt.Errorf("replace MCP settings: %w", err)
	}
	return nil
}
