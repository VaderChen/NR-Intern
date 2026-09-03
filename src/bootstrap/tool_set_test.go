package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 精簡工具集是預設值：工具目錄每一輪都會整份進入提示，小型與本機模型在十幾個工具
// 之間挑選既慢又容易挑錯。需要完整能力時才由管理介面打開。
func TestEffectiveAllowedToolsUsesLeanSetByDefault(t *testing.T) {
	config := Config{AllowedTools: []string{
		"plan_get", "plan_create", "plan_step_update", "directory_list", "directory_create",
		"file_read", "file_search", "file_compare", "file_write", "file_edit",
		"document_inspect", "document_read", "document_create", "document_convert",
		"document_edit", "http_fetch", "shell_exec", "ssh_exec",
		"memory_search", "memory_remember", "memory_forget",
	}}

	lean := EffectiveAllowedTools(config)

	if len(lean) != len(LeanToolNames) {
		t.Fatalf("lean set = %v, want %v", lean, LeanToolNames)
	}
	// 文件產出留在精簡集合裡：使用者說「給我 Excel」時，沒有這些工具就只能用
	// shell 寫一個沒有 BOM 的 CSV 交差。寫入、編輯、SSH 與記憶仍然要擴充工具集。
	if !strings.Contains(strings.Join(lean, ","), "document_create") {
		t.Fatalf("document_create must stay in the lean set: %v", lean)
	}
	for _, name := range []string{"file_write", "file_edit", "ssh_exec", "document_edit", "memory_remember", "http_fetch"} {
		if strings.Contains(strings.Join(lean, ","), name) {
			t.Fatalf("%q must not be in the lean set: %v", name, lean)
		}
	}

	config.ExtendedTools = true
	full := EffectiveAllowedTools(config)
	if len(full) != len(config.AllowedTools) {
		t.Fatalf("extended set = %d tools, want the full allowlist (%d)", len(full), len(config.AllowedTools))
	}
}

// allowlist 明確排除的工具，不會因為精簡集合而被放回來。
func TestLeanSetNeverWidensTheConfiguredAllowlist(t *testing.T) {
	config := Config{AllowedTools: []string{"file_read", "plan_get"}}

	lean := EffectiveAllowedTools(config)

	if strings.Join(lean, ",") != "file_read,plan_get" {
		t.Fatalf("lean set = %v, want only the configured tools", lean)
	}
}

// allowlist 為空原本代表不設限；精簡模式下改為只公開精簡集合。
func TestLeanSetAppliesWhenAllowlistIsEmpty(t *testing.T) {
	lean := EffectiveAllowedTools(Config{})
	if len(lean) != len(LeanToolNames) {
		t.Fatalf("lean set = %v, want the lean tool names", lean)
	}
}

// 工具檢索預設開啟，而且升級上來的安裝必須維持開啟：舊的 service-settings.json
// 沒有這個欄位，若被當成 false，大型 MCP 目錄就會悄悄回到整份進提示的行為。
func TestToolRetrievalDefaultsToOnAndSurvivesLegacySettings(t *testing.T) {
	if !DefaultConfig().ToolRetrieval {
		t.Fatal("MCP tool retrieval must default to on")
	}

	directory := t.TempDir()
	legacy := `{"service_name":"nr-intern","extended_tools":true}`
	if err := os.WriteFile(filepath.Join(directory, serviceSettingsFilename), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}
	config := DefaultConfig()
	config.DataDir = directory
	if err := loadPersistedServiceSettings(&config); err != nil {
		t.Fatalf("loadPersistedServiceSettings: %v", err)
	}
	if !config.ToolRetrieval {
		t.Fatal("legacy settings turned MCP tool retrieval off")
	}
	if !config.ExtendedTools {
		t.Fatal("legacy settings were not applied at all")
	}

	// 管理介面明確關閉後，重新載入必須維持關閉。
	settings := serviceSettingsFromConfig(config)
	settings.ToolRetrieval = false
	if err := persistServiceSettings(directory, settings); err != nil {
		t.Fatalf("persistServiceSettings: %v", err)
	}
	reloaded := DefaultConfig()
	reloaded.DataDir = directory
	if err := loadPersistedServiceSettings(&reloaded); err != nil {
		t.Fatalf("loadPersistedServiceSettings: %v", err)
	}
	if reloaded.ToolRetrieval {
		t.Fatal("an explicit off was not persisted")
	}
}
