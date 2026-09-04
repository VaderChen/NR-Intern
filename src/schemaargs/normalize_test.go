package schemaargs

import (
	"encoding/json"
	"os"
	"testing"
)

func documentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string"},
			"overwrite": map[string]any{"type": "boolean"},
			"dpi":       map[string]any{"type": "integer"},
			"content":   map[string]any{"type": "string"},
			"metadata":  map[string]any{"type": "object"},
			"blocks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":  map[string]any{"type": "string"},
						"level": map[string]any{"type": "integer"},
						"rows": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
			},
			"loose": map[string]any{"type": "array"},
		},
	}
}

// 模型把巢狀結構整個字串化，工具解碼直接失敗——實測一個 session 連續三次栽在這裡。
func TestNormalizeDecodesStringifiedStructures(t *testing.T) {
	result := Normalize(map[string]any{
		"path":     "設備狀況.xlsx",
		"loose":    `[{"name":"設備","rows":[["部門","代碼"]]}]`,
		"metadata": `{"author":"agent"}`,
	}, documentSchema())
	if _, ok := result["loose"].([]any); !ok {
		t.Fatalf("loose stayed a string: %T", result["loose"])
	}
	if _, ok := result["metadata"].(map[string]any); !ok {
		t.Fatalf("metadata stayed a string: %T", result["metadata"])
	}
	if result["path"] != "設備狀況.xlsx" {
		t.Fatalf("path was altered: %#v", result["path"])
	}
}

// 表格的數量欄寫成 264 而不是 "264" 是最自然的寫法。校正只看最上層時，
// 巢狀第四層的這個數字完全沒被碰到，整個 document_create 就失敗。
func TestNormalizeCoercesNestedScalarsToStrings(t *testing.T) {
	result := Normalize(map[string]any{
		"blocks": []any{map[string]any{
			"type": "table",
			"rows": []any{
				[]any{"批號", "數量", "已完成"},
				[]any{"A-001", float64(264), true},
				[]any{"A-002", float64(3.5), nil},
			},
		}},
	}, documentSchema())

	rows := result["blocks"].([]any)[0].(map[string]any)["rows"].([]any)
	for index, want := range [][]string{
		{"批號", "數量", "已完成"},
		{"A-001", "264", "true"},
		{"A-002", "3.5", ""},
	} {
		row, ok := rows[index].([]any)
		if !ok {
			t.Fatalf("row %d is not an array: %#v", index, rows[index])
		}
		for column, expected := range want {
			if got, ok := row[column].(string); !ok || got != expected {
				t.Fatalf("row %d column %d = %#v, want %q", index, column, row[column], expected)
			}
		}
	}
}

func TestNormalizeCoercesNestedScalarsFromStrings(t *testing.T) {
	result := Normalize(map[string]any{
		"blocks": []any{map[string]any{"type": "heading", "level": "2"}},
	}, documentSchema())
	block := result["blocks"].([]any)[0].(map[string]any)
	if level, ok := block["level"].(float64); !ok || level != 2 {
		t.Fatalf("level = %#v, want the number 2", block["level"])
	}
}

func TestNormalizeCoercesTopLevelScalars(t *testing.T) {
	result := Normalize(map[string]any{"overwrite": "true", "dpi": "300"}, documentSchema())
	if result["overwrite"] != true {
		t.Fatalf("overwrite = %#v, want true", result["overwrite"])
	}
	if dpi, ok := result["dpi"].(float64); !ok || dpi != 300 {
		t.Fatalf("dpi = %#v, want 300", result["dpi"])
	}
}

// 結構寫錯不是型別寫錯：硬轉成 "map[...]" 只會把錯誤藏進文件內容裡。
func TestNormalizeLeavesStructuralMistakesAlone(t *testing.T) {
	result := Normalize(map[string]any{
		"blocks": []any{map[string]any{"type": "table", "rows": []any{map[string]any{"批號": "A-001"}}}},
	}, documentSchema())
	rows := result["blocks"].([]any)[0].(map[string]any)["rows"].([]any)
	if _, isObject := rows[0].(map[string]any); !isObject {
		t.Fatalf("an object row must be left untouched, got %#v", rows[0])
	}
}

// schema 沒說的事就不要猜：沒有 items 宣告的陣列原樣保留。
func TestNormalizeLeavesUndeclaredItemsAlone(t *testing.T) {
	result := Normalize(map[string]any{"loose": []any{float64(1), true, "x"}}, documentSchema())
	values := result["loose"].([]any)
	if values[0] != float64(1) || values[1] != true {
		t.Fatalf("values = %#v, want them unchanged", values)
	}
}

// content 宣告是字串，就算內容看起來像 JSON 也不能被解開——那是檔案內容。
func TestNormalizeLeavesStringFieldsAlone(t *testing.T) {
	content := `{"name":"設備","rows":[["a"]]}`
	result := Normalize(map[string]any{"content": content}, documentSchema())
	if result["content"] != content {
		t.Fatalf("content was decoded: %#v", result["content"])
	}
}

// 髒 JSON：尾逗號、單引號、裸鍵、Python 字面值、彎引號、缺右括號。
func TestNormalizeAcceptsDirtyJSON(t *testing.T) {
	for name, value := range map[string]string{
		"trailing comma":   `[{"sheet":"S","cell":"A1","value":"x",},]`,
		"single quotes":    `[{'sheet':'S','cell':'A1','value':'x'}]`,
		"bare keys":        `[{sheet:"S",cell:"A1",value:"x"}]`,
		"python literals":  `[{"sheet":"S","cell":"A1","value":None,"ok":True}]`,
		"smart quotes":     `[{“sheet”:“S”,“cell”:“A1”,“value”:“x”}]`,
		"missing brackets": `[{"sheet":"S","cell":"A1","value":"x"`,
	} {
		result := Normalize(map[string]any{"loose": value}, documentSchema())
		if _, ok := result["loose"].([]any); !ok {
			t.Fatalf("%s: stayed a string: %v", name, result["loose"])
		}
	}
}

// 錯得太離譜的原樣保留，讓工具照常回報自己的錯誤。
//
// 括號修補上限是四個：超過就不是「少打了收尾」而是根本沒有結構，
// 硬補只會生出一堆空陣列，讓錯誤從「參數格式不對」變成「內容是空的」。
func TestNormalizeLeavesHopelessJSONAlone(t *testing.T) {
	for _, value := range []string{"第一張工作表", "sheets 請看上文", "[[[[[[[[", ""} {
		result := Normalize(map[string]any{"loose": value}, documentSchema())
		if _, ok := result["loose"].(string); !ok {
			t.Fatalf("%q should have been left as a string, got %#v", value, result["loose"])
		}
	}
}

func TestNormalizeHandlesEmptyInput(t *testing.T) {
	if result := Normalize(nil, documentSchema()); result != nil {
		t.Fatalf("nil arguments should stay nil, got %#v", result)
	}
	arguments := map[string]any{"path": "a.docx"}
	if result := Normalize(arguments, nil); result["path"] != "a.docx" {
		t.Fatalf("an empty schema must leave arguments untouched, got %#v", result)
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
	result := Normalize(map[string]any{"loose": string(raw)}, documentSchema())
	updates, ok := result["loose"].([]any)
	if !ok {
		t.Fatalf("the payload was not repaired: %T", result["loose"])
	}
	if len(updates) != 2 {
		t.Fatalf("elements = %d, want 2", len(updates))
	}
	second, _ := updates[1].(map[string]any)
	cells, _ := second["value"].(map[string]any)
	if len(cells) < 15 {
		t.Fatalf("the repair lost content: %d cells", len(cells))
	}
}

// 太離譜的純量不要硬轉：留給工具回報自己的錯誤。
func TestNormalizeLeavesNonsenseScalarsAlone(t *testing.T) {
	result := Normalize(map[string]any{"overwrite": "大概吧", "dpi": "很多"}, documentSchema())
	if result["overwrite"] != "大概吧" || result["dpi"] != "很多" {
		t.Fatalf("nonsense was coerced: %#v", result)
	}
}
