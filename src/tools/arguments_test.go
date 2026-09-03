package tools

import (
	"encoding/json"
	"os"
	"testing"

	"AgenticService/src/domain"
)

// 這是實測 transcript 裡連續失敗三次的參數形狀：模型把巢狀結構整個字串化，
// 工具解碼直接失敗（cannot unmarshal string into Go struct field），
// 使用者看到的是「呼叫工具失敗」一直重複。
func TestNormalizeArgumentsDecodesStringifiedStructures(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "document_create",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":         map[string]any{"type": "string"},
				"sheets":       map[string]any{"type": "array"},
				"cell_updates": map[string]any{"type": "array"},
				"metadata":     map[string]any{"type": "object"},
			},
		},
	}
	call := domain.ToolCall{
		ID:   "call_1",
		Name: "document_create",
		Arguments: map[string]any{
			"path":         "設備狀況.xlsx",
			"sheets":       `[{"name":"設備","rows":[["部門","代碼"],["CNC","MC0002"]]}]`,
			"cell_updates": `[{"sheet":"設備","cell":"A1","value":"部門"}]`,
			"metadata":     `{"author":"agent"}`,
		},
	}

	normalized := normalizeCallArguments(call, definition)

	if _, ok := normalized.Arguments["sheets"].([]any); !ok {
		t.Fatalf("sheets stayed a string: %T", normalized.Arguments["sheets"])
	}
	if _, ok := normalized.Arguments["cell_updates"].([]any); !ok {
		t.Fatalf("cell_updates stayed a string: %T", normalized.Arguments["cell_updates"])
	}
	if _, ok := normalized.Arguments["metadata"].(map[string]any); !ok {
		t.Fatalf("metadata stayed a string: %T", normalized.Arguments["metadata"])
	}
	// 宣告為 string 的欄位不能被動到。
	if normalized.Arguments["path"] != "設備狀況.xlsx" {
		t.Fatalf("path was rewritten: %v", normalized.Arguments["path"])
	}
	// 修正後必須能真的解進工具的結構。
	encoded, err := json.Marshal(normalized.Arguments)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var target struct {
		Path   string `json:"path"`
		Sheets []struct {
			Name string     `json:"name"`
			Rows [][]string `json:"rows"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(encoded, &target); err != nil {
		t.Fatalf("the normalized arguments still do not decode: %v", err)
	}
	if len(target.Sheets) != 1 || target.Sheets[0].Name != "設備" {
		t.Fatalf("decoded sheets = %+v", target.Sheets)
	}
}

// 真的要收 JSON 文字的參數不能被解開——例如寫檔內容本身就是一份 JSON。
func TestNormalizeArgumentsLeavesStringFieldsAlone(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "file_write",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
		},
	}
	content := `{"equipment":[{"code":"CNC01_01"}]}`
	call := domain.ToolCall{Name: "file_write", Arguments: map[string]any{"path": "a.json", "content": content}}

	normalized := normalizeCallArguments(call, definition)

	if normalized.Arguments["content"] != content {
		t.Fatalf("a JSON string payload was decoded into a structure: %T", normalized.Arguments["content"])
	}
}

// 解不開就原樣保留，讓工具照常回報自己的錯誤，不要無聲吞掉。
func TestNormalizeArgumentsKeepsUnparsableStrings(t *testing.T) {
	definition := domain.ToolDefinition{
		Name:        "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"sheets": map[string]any{"type": "array"}}},
	}
	call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{"sheets": "第一張工作表"}}

	normalized := normalizeCallArguments(call, definition)

	if normalized.Arguments["sheets"] != "第一張工作表" {
		t.Fatalf("an unparsable value was rewritten: %v", normalized.Arguments["sheets"])
	}
}

// 小型模型產生長 JSON 時，錯的往往是格式而不是內容。這些都要救得回來。
func TestNormalizeAcceptsDirtyJSON(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"cell_updates": map[string]any{"type": "array"},
			"metadata":     map[string]any{"type": "object"},
		}},
	}
	cases := []struct {
		name  string
		value string
		cells int
	}{
		{"漏一個右括號", `[{"sheet":"S","value":{"A1":"部門","B1":"數量"}]`, 1},
		{"結尾未關閉", `[{"sheet":"S","value":{"A1":"部門"}`, 1},
		{"多餘的尾逗號", `[{"sheet":"S","value":{"A1":"部門",}},]`, 1},
		{"單引號", `[{'sheet':'S','value':{'A1':'部門'}}]`, 1},
		{"鍵沒加引號", `[{sheet:"S",value:{"A1":"部門"}}]`, 1},
		{"Python 字面值", `[{"sheet":"S","auto_filter":True,"value":{"A1":None}}]`, 1},
		{"智慧引號", "[{\u201csheet\u201d:\u201cS\u201d,\u201cvalue\u201d:{\u201cA1\u201d:\u201c部門\u201d}}]", 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{"cell_updates": testCase.value}}
			normalized := normalizeCallArguments(call, definition)
			updates, ok := normalized.Arguments["cell_updates"].([]any)
			if !ok {
				t.Fatalf("not repaired: %T", normalized.Arguments["cell_updates"])
			}
			if len(updates) != testCase.cells {
				t.Fatalf("elements = %d, want %d", len(updates), testCase.cells)
			}
			first, _ := updates[0].(map[string]any)
			if first["sheet"] != "S" {
				t.Fatalf("content changed: %+v", first)
			}
		})
	}
}

// 內容真的壞掉、補不回來時要保持原樣，讓工具回報自己的錯誤，不能默默吞掉。
func TestNormalizeLeavesHopelessJSONAlone(t *testing.T) {
	definition := domain.ToolDefinition{
		Name:        "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"cell_updates": map[string]any{"type": "array"}}},
	}
	for _, value := range []string{`這不是 JSON`, `[{"sheet": `, `[[[[[[[[`} {
		call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{"cell_updates": value}}
		normalized := normalizeCallArguments(call, definition)
		if _, repaired := normalized.Arguments["cell_updates"].([]any); repaired {
			t.Fatalf("%q was silently turned into data", value)
		}
	}
}

// 保留缺少右大括號的失敗形狀，但使用虛構內容，避免將實際設備資料帶入公開測試。
func TestNormalizeRepairsMalformedCellUpdatesFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata_broken_cell_updates.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if json.Valid(raw) {
		t.Fatal("the fixture is supposed to be malformed")
	}
	definition := domain.ToolDefinition{
		Name:        "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"cell_updates": map[string]any{"type": "array"}}},
	}
	call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{"cell_updates": string(raw)}}

	normalized := normalizeCallArguments(call, definition)

	updates, ok := normalized.Arguments["cell_updates"].([]any)
	if !ok {
		t.Fatalf("the payload was not repaired: %T", normalized.Arguments["cell_updates"])
	}
	if len(updates) != 2 {
		t.Fatalf("elements = %d, want 2", len(updates))
	}
	second, _ := updates[1].(map[string]any)
	cells, _ := second["value"].(map[string]any)
	if len(cells) < 15 {
		t.Fatalf("the repair lost content: %d cells", len(cells))
	}
	t.Logf("修復後：%d 筆項目，第二筆有 %d 個儲存格", len(updates), len(cells))
}

// 實測：模型把 overwrite 送成字串 "true"，整個工具呼叫因此失敗。
func TestNormalizeCoercesStringifiedScalars(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"overwrite":   map[string]any{"type": "boolean"},
			"header_rows": map[string]any{"type": "integer"},
			"path":        map[string]any{"type": "string"},
		}},
	}
	call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{
		"overwrite": "true", "header_rows": "2", "path": "a.xlsx",
	}}

	normalized := normalizeCallArguments(call, definition)

	if normalized.Arguments["overwrite"] != true {
		t.Fatalf("overwrite = %#v", normalized.Arguments["overwrite"])
	}
	if normalized.Arguments["header_rows"] != float64(2) {
		t.Fatalf("header_rows = %#v", normalized.Arguments["header_rows"])
	}
	if normalized.Arguments["path"] != "a.xlsx" {
		t.Fatalf("path was rewritten: %#v", normalized.Arguments["path"])
	}
}

// 太離譜的值不要硬轉：留給工具回報自己的錯誤。
func TestNormalizeLeavesNonsenseScalarsAlone(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "document_create",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"overwrite":   map[string]any{"type": "boolean"},
			"header_rows": map[string]any{"type": "integer"},
		}},
	}
	call := domain.ToolCall{Name: "document_create", Arguments: map[string]any{
		"overwrite": "大概吧", "header_rows": "很多",
	}}

	normalized := normalizeCallArguments(call, definition)

	if normalized.Arguments["overwrite"] != "大概吧" || normalized.Arguments["header_rows"] != "很多" {
		t.Fatalf("nonsense was coerced: %#v", normalized.Arguments)
	}
}
