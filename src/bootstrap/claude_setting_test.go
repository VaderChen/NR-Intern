package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeSettingsPersistenceSmoke(t *testing.T) {
	config := Config{DefaultProviderID: "claude", Providers: map[string]ProviderConfig{"claude": {Type: "openai-compatible", OpenAICompatible: &openaicompat.Config{UseClaude: true, BaseURL: "https://api.anthropic.com/v1", Model: "synthetic", APIKey: "synthetic-secret"}}}}
	root := t.TempDir()
	if err := persistProviderSettings(root, config.DefaultProviderID, config.Providers, config.ModelPrices); err != nil {
		t.Fatal(err)
	}
	restored := Config{DataDir: root}
	if err := loadPersistedProviderSettings(&restored); err != nil {
		t.Fatal(err)
	}
	view := providerSettingsView(restored, nil)
	encoded, _ := json.Marshal(view)
	if !view.Providers[0].OpenAICompatible.UseClaude || !view.Providers[0].OpenAICompatible.HasAPIKey || strings.Contains(string(encoded), "synthetic-secret") {
		t.Fatal("Claude 設定遺失或金鑰外洩")
	}
}
