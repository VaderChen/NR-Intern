package harness

import (
	"context"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

func documentCreateWithBlocks() domain.ToolDefinition {
	definition := documentCreateDefinition()
	properties, _ := definition.InputSchema["properties"].(map[string]any)
	properties["blocks"] = map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object", "required": []string{"type"},
			"properties": map[string]any{
				"type":  map[string]any{"type": "string"},
				"text":  map[string]any{"type": "string"},
				"level": map[string]any{"type": "integer"},
				"rows":  map[string]any{"type": "array"},
			},
		},
	}
	return definition
}

func documentCreateDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "document_create",
		Category:           "documents",
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"format": map[string]any{"type": "string"},
				"cell_updates": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"sheet", "cell"},
						"properties": map[string]any{
							"sheet": map[string]any{"type": "string"},
							"cell":  map[string]any{"type": "string"},
							"value": map[string]any{},
						},
					},
				},
			},
			"required": []string{"path"},
		},
	}
}

// 使用者實測收到的畫面：模型把 cell_updates 整包印成回答，檔案根本沒有被建立。
func TestToolArgumentsLeakDetectsStringifiedCellUpdates(t *testing.T) {
	content := `[{"cell": "A1", "sheet": "設備清單", "value": "設備狀況總覽表"}, ` +
		`{"cell": "A2", "sheet": "設備清單", "value": "部門 | 設備數量 | 設備代碼"}, ` +
		`{"cell": "A3", "sheet": "設備清單", "value": "BOM 整合測試部 | 4 台"}]`

	if got := toolArgumentsLeak(content, []domain.ToolDefinition{documentCreateDefinition()}); got != "document_create" {
		t.Fatalf("leak detection = %q, want document_create", got)
	}
}

// 實測第二次漏掉的形狀：元素只有 {"cell","value"}，少了 schema 標為必填的 sheet，
// 那一大片 JSON 就被當成答案顯示出來。參數不完整仍然是參數。
func TestToolArgumentsLeakDetectsIncompleteCellUpdates(t *testing.T) {
	content := `[{"cell": "sheet0/1/1", "value": "設備狀況報告"}, ` +
		`{"cell": "sheet0/1/2", "value": "設備分佈總覽"}, ` +
		`{"cell": "sheet0/1/3", "value": "設備代碼"}]`

	if got := toolArgumentsLeak(content, []domain.ToolDefinition{documentCreateDefinition()}); got != "document_create" {
		t.Fatalf("leak detection = %q, want document_create", got)
	}
}

// 實測第三種漏法：JSON 前面帶著雜訊（「[] AI Agent [{...」），整段解析必然失敗，
// 那一大片 blocks 參數就被當成答案顯示出來。
func TestToolArgumentsLeakDetectsJSONEmbeddedInText(t *testing.T) {
	content := `[] AI Agent [{"text": "設備狀況總覽表", "type": "heading"}, ` +
		`{"level": 2, "text": "設備分佈總覽", "type": "heading"}, ` +
		`{"text": "", "type": "paragraph"}, {"rows": [], "type": "table"}]`

	if got := toolArgumentsLeak(content, []domain.ToolDefinition{documentCreateWithBlocks()}); got != "document_create" {
		t.Fatalf("leak detection = %q, want document_create", got)
	}
}

// 一般回答裡剛好出現的短 JSON 不該被當成參數。
func TestToolArgumentsLeakIgnoresShortInlineJSON(t *testing.T) {
	content := `查詢結果是 {"count": 23}，共 23 台設備。`
	if got := toolArgumentsLeak(content, []domain.ToolDefinition{documentCreateWithBlocks()}); got != "" {
		t.Fatalf("an ordinary answer was misread as a %s leak", got)
	}
}

func TestToolArgumentsLeakDetectsWholeArgumentObject(t *testing.T) {
	content := `{"path": "設備.xlsx", "format": "xlsx"}`
	if got := toolArgumentsLeak(content, []domain.ToolDefinition{documentCreateDefinition()}); got != "document_create" {
		t.Fatalf("leak detection = %q, want document_create", got)
	}
}

// 使用者本來就要求輸出 JSON 的情況不能被誤判成協定錯誤。
func TestToolArgumentsLeakIgnoresUnrelatedContent(t *testing.T) {
	definitions := []domain.ToolDefinition{documentCreateDefinition()}
	cases := []string{
		"設備共有 22 台，其中 CNC 8 台。",
		`{"總數": 22, "部門": "CNC"}`,
		`[{"department":"CNC","count":8},{"department":"雷雕","count":3}]`,
		`["A", "B", "C"]`,
		"",
	}
	for _, content := range cases {
		if got := toolArgumentsLeak(content, definitions); got != "" {
			t.Fatalf("content %q was misread as a %s argument leak", content, got)
		}
	}
}

// 端對端：原生模式下把參數當成回答輸出時，Harness 必須要求它改用工具呼叫，
// 而不是把那片 JSON 當成最終答案交給使用者。
func TestRunRepairsNativeArgumentsLeak(t *testing.T) {
	sessions := newMemorySessions(testSession())
	leak := `[{"cell":"A1","sheet":"設備清單","value":"設備狀況總覽表"},{"cell":"A2","sheet":"設備清單","value":"部門"}]`
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: leak},
		{Content: "已建立 設備清單.xlsx，共 22 台設備。"},
	}}
	tools := &fakeTools{definitions: []domain.ToolDefinition{documentCreateDefinition()}}
	runner := newTestRunner(sessions, model, tools)
	runner.ToolCallMode = ToolCallModeNative

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "請把上述結果轉成 EXCEL 檔案"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(result.Message.Content, "cell") {
		t.Fatalf("the argument dump was shown to the user: %s", result.Message.Content)
	}
	if result.Message.Content != "已建立 設備清單.xlsx，共 22 台設備。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
	if len(model.requests) < 2 {
		t.Fatalf("no repair turn happened: %d model calls", len(model.requests))
	}
	repair := model.requests[1]
	steering := repair.SystemPrompt + repair.HostPrompt + repair.PhasePrompt + repair.ContextPrompt
	if !strings.Contains(steering, "tool_protocol_repair") || !strings.Contains(steering, "document_create") {
		t.Fatalf("the repair turn did not name the tool: %s", steering)
	}
}
