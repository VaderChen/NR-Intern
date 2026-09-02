package harness

import (
	"strings"
	"testing"

	"AgenticService/src/domain"
)

func retrievalCatalog(extra ...domain.ToolDefinition) []domain.ToolDefinition {
	definitions := []domain.ToolDefinition{
		{Name: "shell_exec", Description: "執行系統命令"},
		{Name: "file_read", Description: "讀取檔案", ReadOnly: true},
	}
	for _, tool := range largeMCPTools {
		definitions = append(definitions, domain.ToolDefinition{
			Name:        "mcp__mars-mes__" + tool.name,
			Description: tool.description,
			ReadOnly:    true,
		})
	}
	for index := 0; index < 40; index++ {
		definitions = append(definitions, domain.ToolDefinition{
			Name:        "mcp__mars-mes__plugin_report" + string(rune('a'+index%26)) + "__report_query",
			Description: "查詢報表統計資料",
			ReadOnly:    true,
		})
	}
	return append(definitions, extra...)
}

func TestRetrievalSelectsTheRelevantToolsForACompoundQuestion(t *testing.T) {
	catalog := retrievalCatalog()
	retriever := newToolRetriever(catalog, "公司有多少部門？各部門有多少人員？", true)
	if !retriever.enabled() {
		t.Fatal("retrieval should be active for a large catalog")
	}
	staged := retriever.stage(catalog)
	names := availableToolNamesSorted(staged)
	for _, want := range []string{
		"mcp__mars-mes__plugin_department__department_list",
		"mcp__mars-mes__plugin_employee__employee_list",
	} {
		if !definitionNamed(staged, want) {
			t.Fatalf("%s was not retrieved; staged %v", want, names)
		}
	}
	// 內建工具不受檢索影響，find tool 一定在。
	if !definitionNamed(staged, "shell_exec") || !definitionNamed(staged, findToolsToolName) {
		t.Fatalf("staged catalog lost required tools: %v", names)
	}
	mcp := 0
	for _, definition := range staged {
		if isMCPToolName(definition.Name) {
			mcp++
		}
	}
	if mcp > mcpRetrievalLimit {
		t.Fatalf("staged %d MCP tools, want at most %d", mcp, mcpRetrievalLimit)
	}
}

// 共通詞不能主導排序：外掛型 Server 的工具名稱幾乎都含 query／plugin。
func TestRetrievalIgnoresTermsSharedByTheWholeCatalog(t *testing.T) {
	catalog := retrievalCatalog()
	retriever := newToolRetriever(catalog, "query", true)
	if got := retriever.search("query", mcpRetrievalLimit); len(got) > 0 {
		t.Fatalf("a catalog-wide term matched %d tools: %v", len(got), got[0].Name)
	}
	matched := retriever.search("庫存", mcpRetrievalLimit)
	if len(matched) == 0 || matched[0].Name != "mcp__mars-mes__plugin_warehouse__warehouse_stock_query" {
		t.Fatalf("specific term did not rank the right tool first: %+v", matched)
	}
}

func TestRetrievalStaysOffForASmallCatalog(t *testing.T) {
	catalog := []domain.ToolDefinition{
		{Name: "shell_exec", Description: "執行系統命令"},
		{Name: "mcp__mars-mes__mo_query", Description: "查詢製令"},
		{Name: "mcp__mars-mes__department_list", Description: "列出部門"},
	}
	retriever := newToolRetriever(catalog, "查詢製令", true)
	if retriever.enabled() {
		t.Fatal("retrieval should stay off below the threshold")
	}
	staged := retriever.stage(catalog)
	if len(staged) != len(catalog) {
		t.Fatalf("small catalog was filtered: %v", availableToolNamesSorted(staged))
	}
	if definitionNamed(staged, findToolsToolName) {
		t.Fatal("find tool should not be added when retrieval is off")
	}
}

func TestRetrievalCanBeDisabled(t *testing.T) {
	catalog := retrievalCatalog()
	retriever := newToolRetriever(catalog, "查詢製令", false)
	if retriever.enabled() {
		t.Fatal("retrieval was disabled but stayed active")
	}
	if got := len(retriever.stage(catalog)); got != len(catalog) {
		t.Fatalf("disabled retrieval filtered the catalog: %d of %d", got, len(catalog))
	}
}

func TestFindToolsReturnsSchemasAndAddsThemToTheCatalog(t *testing.T) {
	target := domain.ToolDefinition{
		Name:        "mcp__mars-mes__plugin_equipment__equipment_utilization",
		Description: "查詢設備稼動與嫁動率",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"line": map[string]any{"type": "string"}},
			"required":   []any{"line"},
		},
	}
	catalog := retrievalCatalog(target)
	retriever := newToolRetriever(catalog, "完全無關的問題", true)
	if definitionNamed(retriever.stage(catalog), target.Name) {
		t.Fatal("target tool should not be retrieved by an unrelated question")
	}
	result := retriever.execute(domain.ToolCall{ID: "call_1", Name: findToolsToolName, Arguments: map[string]any{"query": "稼動"}})
	if result.IsError {
		t.Fatalf("find tools failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, target.Name) || !strings.Contains(result.Content, "\"line\"") {
		t.Fatalf("find tools did not return the name and schema: %s", result.Content)
	}
	if !definitionNamed(retriever.stage(catalog), target.Name) {
		t.Fatal("discovered tool did not enter the catalog")
	}
}

func TestFindToolsExplainsAMissWithoutClosingTheDoor(t *testing.T) {
	catalog := retrievalCatalog()
	retriever := newToolRetriever(catalog, "查詢製令", true)
	result := retriever.execute(domain.ToolCall{ID: "call_1", Name: findToolsToolName, Arguments: map[string]any{"query": "zzzz"}})
	if result.IsError {
		t.Fatalf("a miss should not be an error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "可用工具共") {
		t.Fatalf("a miss should report what is available: %s", result.Content)
	}
	empty := retriever.execute(domain.ToolCall{ID: "call_2", Name: findToolsToolName, Arguments: map[string]any{}})
	if !empty.IsError {
		t.Fatal("a missing query should be reported as an error")
	}
}

func TestRecognizableKeepsEveryMCPToolCallable(t *testing.T) {
	catalog := retrievalCatalog()
	retriever := newToolRetriever(catalog, "查詢製令", true)
	staged := retriever.stage(catalog)
	callable := availableToolNames(retriever.recognizable(staged))
	for _, definition := range catalog {
		if !callable[definition.Name] {
			t.Fatalf("%s stopped being callable", definition.Name)
		}
	}
}

func TestCompactSchemaKeepsTheShapeOfAnOversizedSchema(t *testing.T) {
	properties := map[string]any{}
	for index := 0; index < 60; index++ {
		properties[string(rune('a'+index%26))+strings.Repeat("x", 20)+string(rune('0'+index%10))] = map[string]any{
			"type":        "string",
			"description": strings.Repeat("說明", 40),
		}
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": []any{"axxx0"}}
	compact := compactSchema(schema)
	if compact == nil {
		t.Fatal("compact schema was dropped")
	}
	shapes, ok := compact["properties"].(map[string]any)
	if !ok || len(shapes) != len(properties) {
		t.Fatalf("compact schema lost properties: %v", compact["properties"])
	}
	for name, value := range shapes {
		property, ok := value.(map[string]any)
		if !ok || property["type"] != "string" || property["description"] != nil {
			t.Fatalf("property %s was not reduced to its shape: %v", name, value)
		}
	}
	if compact["required"] == nil {
		t.Fatal("compact schema dropped the required list")
	}
}

func TestTokenizeHandlesCJKAndASCII(t *testing.T) {
	terms := tokenizeForRetrieval("查詢製令 MO-A123 的狀態")
	joined := strings.Join(terms, ",")
	for _, want := range []string{"查詢", "詢製", "製令", "mo", "a123", "狀態"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("term %q missing from %v", want, terms)
		}
	}
}

// 擴充工具集打開後有近二十個內建工具，document_* 與 ssh_* 的 schema 都不小，
// 而任何一次需求通常只會用到其中一兩個。檢索同樣要蓋到內建工具。
func nativeCatalog() []domain.ToolDefinition {
	names := map[string]string{
		"shell_exec": "執行系統命令", "file_read": "讀取檔案", "directory_list": "列出目錄",
		"file_search": "搜尋檔案內容", "plan_get": "取得計畫", "plan_create": "建立計畫",
		"plan_step_update": "更新計畫步驟", "file_write": "寫入檔案", "file_edit": "編輯檔案",
		"file_compare": "比較檔案差異", "directory_create": "建立目錄",
		"document_inspect": "檢視 PDF、DOCX、XLSX、PPTX 的結構與頁數",
		"document_read":    "分段讀取辦公文件內容",
		"document_create":  "建立辦公文件",
		"document_convert": "轉換文件格式",
		"pdf_pages":        "PDF 頁面合併、擷取、重排與拆分",
		"ssh_exec":         "在遠端主機執行命令", "ssh_wait": "等待遠端狀態",
		"memory_search": "查詢長期記憶", "memory_remember": "寫入長期記憶",
		"http_fetch": "讀取外部網頁內容",
	}
	definitions := make([]domain.ToolDefinition, 0, len(names))
	for name, description := range names {
		definitions = append(definitions, domain.ToolDefinition{Name: name, Description: description})
	}
	return definitions
}

func TestRetrievalCoversBuiltinToolsAndKeepsTheCore(t *testing.T) {
	catalog := nativeCatalog()
	retriever := newToolRetriever(catalog, "幫我看一下這份 PDF 的頁數", true)
	if !retriever.enabled() {
		t.Fatal("retrieval should be active for an extended builtin catalog")
	}
	staged := retriever.stage(catalog)
	names := availableToolNamesSorted(staged)

	for core := range coreToolNames {
		if definitionNamed(catalog, core) && !definitionNamed(staged, core) {
			t.Fatalf("core tool %s was filtered out: %v", core, names)
		}
	}
	if !definitionNamed(staged, "document_inspect") {
		t.Fatalf("the relevant document tool was not retrieved: %v", names)
	}
	if definitionNamed(staged, "ssh_exec") || definitionNamed(staged, "memory_remember") {
		t.Fatalf("unrelated tools stayed in the catalog: %v", names)
	}
	if len(staged) >= len(catalog) {
		t.Fatalf("catalog did not shrink: %d of %d", len(staged), len(catalog))
	}
	// 沒被選中的內建工具仍然可以呼叫。
	callable := availableToolNames(retriever.recognizable(staged))
	for _, definition := range catalog {
		if !callable[definition.Name] {
			t.Fatalf("%s stopped being callable", definition.Name)
		}
	}
}

func TestFindToolsReachesBuiltinTools(t *testing.T) {
	catalog := nativeCatalog()
	retriever := newToolRetriever(catalog, "列出這個目錄", true)
	if definitionNamed(retriever.stage(catalog), "ssh_exec") {
		t.Skip("retrieval already offered the tool; this case needs an unlisted one")
	}
	result := retriever.execute(domain.ToolCall{ID: "call_1", Name: findToolsToolName, Arguments: map[string]any{"query": "遠端主機"}})
	if result.IsError {
		t.Fatalf("find tools failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ssh_exec") {
		t.Fatalf("builtin tool was not reachable through %s: %s", findToolsToolName, result.Content)
	}
	if !definitionNamed(retriever.stage(catalog), "ssh_exec") {
		t.Fatal("discovered builtin tool did not enter the catalog")
	}
}

// 核心工具數量固定且 schema 很小，不該因為目錄一大就被檢索擠掉。
func TestRetrievalStaysOffWhenOnlyCoreToolsExist(t *testing.T) {
	catalog := []domain.ToolDefinition{}
	for name := range coreToolNames {
		catalog = append(catalog, domain.ToolDefinition{Name: name, Description: name})
	}
	retriever := newToolRetriever(catalog, "隨便問一句", true)
	if retriever.enabled() {
		t.Fatal("a catalog of core tools alone should not trigger retrieval")
	}
}
