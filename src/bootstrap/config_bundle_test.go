package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBundleFixture(t *testing.T, dataDir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func readBundle(t *testing.T, data []byte) map[string]string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	files := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		var buffer bytes.Buffer
		if _, err := buffer.ReadFrom(reader); err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		_ = reader.Close()
		files[file.Name] = buffer.String()
	}
	return files
}

// 設定包是拿去換一台機器重建環境用的，因此帶設定、不帶對話；而且不能把金鑰
// 明文寫進使用者的下載資料夾——這個產品每一處都把憑證當成不離開後端的東西。
func TestConfigBundleCarriesSettingsWithoutSecretsOrConversations(t *testing.T) {
	dataDir := t.TempDir()
	writeBundleFixture(t, dataDir, providerSettingsFilename, `{
      "default_provider_id": "mars",
      "providers": {"mars": {"id": "mars", "base_url": "https://llmproxy.example.com/v1", "api_key": "sk-super-secret", "model": "gpt-5.6-terra"}}
    }`)
	writeBundleFixture(t, dataDir, mcpSettingsFilename, `{
      "servers": {"mars-mes": {
        "id": "mars-mes", "enabled": true, "transport": "streamable-http", "url": "https://mcp.example.com/mcp",
        "username": "factory", "password": "hunter2",
        "headers": {"X-Site": "factory", "Authorization": "Bearer leaked"},
        "environment": {"API_TOKEN": "leaked-token"},
        "enabled_tools": ["mo_query"]
      }}
    }`)
	// max_tokens 的鍵裡有「token」，但它是數字上限，不是憑證。
	writeBundleFixture(t, dataDir, serviceSettingsFilename, `{"service_name":"永不休息的實習生","tool_retrieval":true,"max_tokens":120000,"max_tool_calls":50}`)
	writeBundleFixture(t, dataDir, "netpass.json", `{"api_key":"netpass-secret","domain":"example.com"}`)
	// 這些不該出現在設定包裡。
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o750); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	writeBundleFixture(t, dataDir, filepath.Join("sessions", "session_1.jsonl"), `{"secret":"conversation"}`)

	runtime := &Runtime{Config: Config{DataDir: dataDir}}
	data, err := runtime.ConfigBundle(context.Background())
	if err != nil {
		t.Fatalf("ConfigBundle: %v", err)
	}
	files := readBundle(t, data)

	for _, name := range []string{"manifest.json", providerSettingsFilename, mcpSettingSettingsName(), serviceSettingsFilename, "netpass.json"} {
		if _, exists := files[name]; !exists {
			t.Fatalf("%s missing from the bundle: %v", name, keysOf(files))
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "sessions/") || strings.HasPrefix(name, "workspaces/") || strings.HasPrefix(name, "projects/") {
			t.Fatalf("conversation data leaked into the config bundle: %s", name)
		}
	}

	joined := strings.Join(valuesOf(files), "\n")
	for _, secret := range []string{"sk-super-secret", "hunter2", "Bearer leaked", "leaked-token", "netpass-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %q was exported in plaintext", secret)
		}
	}

	// 結構要保留，使用者才知道要補什麼。
	if !strings.Contains(files[providerSettingsFilename], "https://llmproxy.example.com/v1") {
		t.Fatalf("provider endpoint was lost: %s", files[providerSettingsFilename])
	}
	if !strings.Contains(files[mcpSettingsFilename], "mo_query") || !strings.Contains(files[mcpSettingsFilename], "X-Site") {
		t.Fatalf("MCP structure was lost: %s", files[mcpSettingsFilename])
	}
	if !strings.Contains(files[serviceSettingsFilename], "tool_retrieval") {
		t.Fatalf("service settings were lost: %s", files[serviceSettingsFilename])
	}
	if !strings.Contains(files[serviceSettingsFilename], "120000") || !strings.Contains(files[serviceSettingsFilename], "50") {
		t.Fatalf("a numeric limit whose name contains \"token\" was redacted: %s", files[serviceSettingsFilename])
	}

	var manifest configBundleManifest
	if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Format != "nr-intern-config-bundle" || len(manifest.Included) != 4 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if len(manifest.Redacted) == 0 {
		t.Fatal("manifest must list the redacted fields so the user knows what to re-enter")
	}
}

func TestConfigBundleSkipsMissingFiles(t *testing.T) {
	dataDir := t.TempDir()
	writeBundleFixture(t, dataDir, serviceSettingsFilename, `{"service_name":"nr-intern"}`)
	runtime := &Runtime{Config: Config{DataDir: dataDir}}
	data, err := runtime.ConfigBundle(context.Background())
	if err != nil {
		t.Fatalf("ConfigBundle: %v", err)
	}
	files := readBundle(t, data)
	if _, exists := files[serviceSettingsFilename]; !exists {
		t.Fatalf("service settings missing: %v", keysOf(files))
	}
	if _, exists := files[providerSettingsFilename]; exists {
		t.Fatal("a provider file that does not exist must not appear in the bundle")
	}
}

func mcpSettingSettingsName() string { return mcpSettingsFilename }

func keysOf(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	return names
}

func valuesOf(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
