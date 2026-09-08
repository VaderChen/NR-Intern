package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func exportFixture(t *testing.T) *Runtime {
	t.Helper()
	dataDir := t.TempDir()
	writeBundleFixture(t, dataDir, providerSettingsFilename, `{
      "default_provider_id": "mars",
      "providers": {"mars": {"display_name": "工廠模型", "type": "openai-compatible", "enabled": true,
        "openai_compatible": {"base_url": "https://llm.example.com/v1", "api_key": "sk-super-secret", "model": "gpt-4o"}}}
    }`)
	writeBundleFixture(t, dataDir, mcpSettingsFilename, `{
      "servers": {"mes": {"id": "mes", "display_name": "MES", "enabled": true, "transport": "streamable-http",
        "url": "https://mcp.example.com/mcp", "username": "factory", "password": "hunter2"}}
    }`)
	return &Runtime{Config: Config{DataDir: dataDir}}
}

// 匯出的檔案必須匯得回去：格式就是拖放匯入吃的那一種，而且 id 要寫進物件裡
// ——儲存時 id 是 map 的鍵，收檔案的人可能改鍵名，匯入端以物件裡的 id 為準。
func TestExportProviderSettingProducesImportableShape(t *testing.T) {
	data, err := exportFixture(t).ExportProviderSetting(context.Background(), "mars", true)
	if err != nil {
		t.Fatalf("ExportProviderSetting: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	providers, _ := root["providers"].(map[string]any)
	entry, _ := providers["mars"].(map[string]any)
	if entry == nil {
		t.Fatalf("匯出應是 providers 物件對映：%s", data)
	}
	if entry["id"] != "mars" {
		t.Fatalf("id 應寫進物件裡：%v", entry["id"])
	}
	if root["contains_secrets"] != true {
		t.Fatalf("帶憑證時 contains_secrets 應為 true：%v", root["contains_secrets"])
	}
	if !strings.Contains(string(data), "sk-super-secret") {
		t.Fatalf("明確要求時應帶出金鑰：%s", data)
	}
}

// 預設遮蔽。帶明文必須是呼叫端要求的，不是漏傳參數的後果。
func TestExportProviderSettingRedactsByDefault(t *testing.T) {
	data, err := exportFixture(t).ExportProviderSetting(context.Background(), "mars", false)
	if err != nil {
		t.Fatalf("ExportProviderSetting: %v", err)
	}
	if strings.Contains(string(data), "sk-super-secret") {
		t.Fatalf("預設不得帶出明文金鑰：%s", data)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if root["contains_secrets"] != false {
		t.Fatalf("遮蔽時 contains_secrets 應為 false：%v", root["contains_secrets"])
	}
	// 結構要留著，收檔案的人才知道有哪些欄位要補。
	providers, _ := root["providers"].(map[string]any)
	entry, _ := providers["mars"].(map[string]any)
	settings, _ := entry["openai_compatible"].(map[string]any)
	if settings["base_url"] != "https://llm.example.com/v1" {
		t.Fatalf("非機密欄位不該被遮蔽：%v", settings)
	}
}

// MCP 匯出用 mcpServers 這個鍵：那是 MCP 生態共通的寫法，匯出的檔案因此
// 也餵得進其他工具。
func TestExportMCPSettingUsesTheCommonKey(t *testing.T) {
	data, err := exportFixture(t).ExportMCPSetting(context.Background(), "mes", true)
	if err != nil {
		t.Fatalf("ExportMCPSetting: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if _, found := root["mcpServers"]; !found {
		t.Fatalf("MCP 匯出應使用 mcpServers：%s", data)
	}
	if !strings.Contains(string(data), "hunter2") {
		t.Fatalf("明確要求時應帶出密碼：%s", data)
	}
}

func TestExportMCPSettingRedactsByDefault(t *testing.T) {
	data, err := exportFixture(t).ExportMCPSetting(context.Background(), "mes", false)
	if err != nil {
		t.Fatalf("ExportMCPSetting: %v", err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("預設不得帶出密碼：%s", data)
	}
}

// 找不到就說找不到，不要回一個空殼讓使用者以為匯出成功了。
func TestExportSettingRejectsUnknownID(t *testing.T) {
	runtime := exportFixture(t)
	if _, err := runtime.ExportProviderSetting(context.Background(), "missing", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未知 Provider 應回 ErrNotFound，得到 %v", err)
	}
	if _, err := runtime.ExportMCPSetting(context.Background(), "missing", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未知 MCP Server 應回 ErrNotFound，得到 %v", err)
	}
	if _, err := runtime.ExportProviderSetting(context.Background(), "  ", false); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空白 id 應回 ErrInvalidInput，得到 %v", err)
	}
}
