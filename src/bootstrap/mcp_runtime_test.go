package bootstrap

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/mcpclient"
	"context"
	"testing"
)

func newMCPTestRuntime(t *testing.T, servers ...mcpclient.ServerConfig) *Runtime {
	t.Helper()
	manager, err := mcpclient.New(servers, "nr-intern-test", "test", 64*1024, logging.Discard())
	if err != nil {
		t.Fatalf("mcpclient.New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	stored := make(map[string]mcpclient.ServerConfig, len(servers))
	for _, server := range servers {
		normalized, err := server.Normalize()
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		stored[normalized.ID] = normalized
	}
	return &Runtime{
		Config: Config{DataDir: t.TempDir(), MCPServers: stored},
		MCP:    manager,
		logger: logging.Discard(),
	}
}

// 管理介面不會取回明文憑證，儲存時也不會重送；沒有出現在 payload 的欄位必須保留，
// 否則使用者每次調整任何設定都會把金鑰洗掉。
func TestUpdateMCPSettingsKeepsStoredCredentials(t *testing.T) {
	runtime := newMCPTestRuntime(t, mcpclient.ServerConfig{
		ID: "mars", Enabled: true, Transport: mcpclient.TransportStreamableHTTP,
		URL: "https://mcp.example.com/mcp", Username: "fixture-user", Password: "fixture-password",
		Headers: map[string]string{"X-Site": "factory"},
	})

	updated, err := runtime.UpdateMCPSettings(context.Background(), domain.UpdateMCPSettingsInput{
		Servers: []domain.MCPServerSetting{{
			ID: "mars", DisplayName: "Mars", Enabled: true, Transport: mcpclient.TransportStreamableHTTP,
			URL: "https://mcp.example.com/mcp", StartupTimeoutSeconds: 20, CallTimeoutSeconds: 1800,
		}},
	})
	if err != nil {
		t.Fatalf("UpdateMCPSettings: %v", err)
	}
	if len(updated.Servers) != 1 || !updated.Servers[0].HasBasicAuth || !updated.Servers[0].HasHeaders {
		t.Fatalf("settings view lost the credential markers: %+v", updated.Servers)
	}
	stored := runtime.Config.MCPServers["mars"]
	if stored.Username != "fixture-user" || stored.Password != "fixture-password" || stored.Headers["X-Site"] != "factory" {
		t.Fatalf("stored credentials were dropped: %+v", stored)
	}
	live := runtime.MCP.Configs()
	if len(live) != 1 || live[0].Username != "fixture-user" || live[0].Password != "fixture-password" {
		t.Fatalf("live MCP config lost the credentials: %+v", live)
	}
}
