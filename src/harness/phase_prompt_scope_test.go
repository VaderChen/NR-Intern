package harness

import (
	"AgenticService/src/domain"
	"strings"
	"testing"
)

func queryStageTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{Name: "shell_exec", Category: "system"},
		{Name: "file_read", ReadOnly: true, Category: "files"},
		{Name: "directory_list", ReadOnly: true, Category: "files"},
		{Name: "plan_get", ReadOnly: true, Category: "planning"},
		{Name: "mcp__mars-mes__query_work_orders", RequiresPermission: true, Category: "mcp"},
	}
}

// 每一輪都會送出的策略提示裡，只有與本輪可用工具相關的段落才該出現。
// 問一句「有多少部門」不需要先讀完辦公文件流程、遠端部署守則與寫入生命週期。
func TestExplorationPromptOnlyCoversAvailableCapabilities(t *testing.T) {
	prompt := explorationPhasePrompt(false, queryStageTools())

	for _, absent := range []string{"document_inspect", "遠端部署", "單一資源生命週期"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("prompt carries guidance for unavailable tools (%q): %s", absent, prompt)
		}
	}
	if !strings.Contains(prompt, "工具呼叫必須服務於原始需求") {
		t.Fatalf("core scoping rule is missing: %s", prompt)
	}

	full := append(queryStageTools(),
		domain.ToolDefinition{Name: "document_read", ReadOnly: true, Category: "documents"},
		domain.ToolDefinition{Name: "ssh_exec", RequiresPermission: true, Category: "remote"},
		domain.ToolDefinition{Name: "file_write", RequiresPermission: true, Category: "files"},
	)
	prompt = explorationPhasePrompt(false, full)
	for _, expected := range []string{"document_inspect", "遠端部署", "單一資源生命週期"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt is missing guidance for an available capability (%q): %s", expected, prompt)
		}
	}
}

// 沒有任何計畫時只送一句提示，不必每輪重讀完整的計畫生命週期規則。
func TestPlanningPromptShrinksWithoutPlans(t *testing.T) {
	short := planningPhasePrompt(false, false)
	full := planningPhasePrompt(false, true)

	if strings.Contains(short, "plan_step_update") || strings.Contains(short, "blocked") {
		t.Fatalf("the short planning prompt should not repeat the full lifecycle: %s", short)
	}
	if !strings.Contains(short, "不必建立計畫") {
		t.Fatalf("the short planning prompt must still say simple work needs no plan: %s", short)
	}
	if !strings.Contains(full, "blocked") || len([]rune(full)) <= len([]rune(short)) {
		t.Fatalf("sessions with plans must still get the full rules: %s", full)
	}
}

// 模型看到的工具目錄只放挑選與呼叫工具需要的欄位。platforms／capabilities 是工具目錄
// UI 用的中繼資料，每一輪重送只是白白吃掉小模型的 context。
func TestToolCatalogOmitsUIOnlyMetadata(t *testing.T) {
	prompt := toolInstructionPrompt([]domain.ToolDefinition{{
		Name:         "shell_exec",
		Label:        "執行命令",
		Description:  "在 Sandbox 內執行命令。",
		Platforms:    []string{"darwin", "linux", "windows"},
		Capabilities: []string{"shell", "direct-exec", "timeout"},
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
	}})

	for _, absent := range []string{"platforms", "capabilities", "direct-exec", "darwin"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("tool catalog still carries UI-only metadata (%q): %s", absent, prompt)
		}
	}
	for _, expected := range []string{`"name":"shell_exec"`, `"label":"執行命令"`, `"input_schema"`, `"read_only"`} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("tool catalog is missing %q: %s", expected, prompt)
		}
	}
}

// native 模式的工具清單已經走 OpenAI-compatible 的 tools 欄位；文字提示只補使用規則，
// 不再重列一次工具名稱與說明。
func TestNativeToolPromptDoesNotRepeatTheCatalog(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "shell_exec", Description: "在 Sandbox 內執行命令。"},
		{Name: "mcp__mars-mes__dispatchstatus_query", Description: "查詢派工狀態。", ServerInstructions: "本服務只提供唯讀查詢。"},
	}

	prompt := nativeToolPrompt(definitions)

	for _, absent := range []string{"shell_exec", "dispatchstatus_query", "在 Sandbox 內執行命令"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("native tool prompt still repeats the catalog (%q): %s", absent, prompt)
		}
	}
	if !strings.Contains(prompt, "必須透過 tool_calls 呼叫") {
		t.Fatalf("native tool prompt lost the calling rule: %s", prompt)
	}
	// Server instructions 不在 tools 欄位裡，仍必須保留。
	if !strings.Contains(prompt, "本服務只提供唯讀查詢。") {
		t.Fatalf("server instructions were dropped: %s", prompt)
	}
}

// thinking 關閉時不會有 reasoning 欄位，那段寫作規範就是多餘的。
func TestProgressPromptSkippedWhenThinkingIsOff(t *testing.T) {
	if progressPresentationPrompt(domain.ThinkingModeNone) != "" {
		t.Fatal("thinking off must not carry the reasoning style rules")
	}
	if progressPresentationPrompt("") == "" || progressPresentationPrompt(domain.ThinkingModeMedium) == "" {
		t.Fatal("sessions that can emit reasoning still need the style rules")
	}
}
