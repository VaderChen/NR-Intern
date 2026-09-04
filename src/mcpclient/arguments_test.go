package mcpclient

import (
	"testing"

	"AgenticService/src/schemaargs"
)

// MCP 與原生工具共用同一份校正邏輯（src/schemaargs）。這裡守住的是「共用」本身：
// 兩份拷貝分岔過一次——原生端補了布林、數字與巢狀遞迴，MCP 端停在只處理最上層，
// 同一個模型犯同一個錯，走原生會被救回來、走 MCP 就失敗。
func TestMCPUsesTheSharedArgumentNormalizer(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rows": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"limit": map[string]any{"type": "integer"},
		},
	}
	normalized := schemaargs.Normalize(map[string]any{
		"rows":  []any{[]any{"批號", float64(264)}},
		"limit": "50",
	}, schema)

	row, ok := normalized["rows"].([]any)[0].([]any)
	if !ok {
		t.Fatalf("rows = %#v", normalized["rows"])
	}
	if row[1] != "264" {
		t.Fatalf("nested cell = %#v, want the string \"264\"", row[1])
	}
	if limit, ok := normalized["limit"].(float64); !ok || limit != 50 {
		t.Fatalf("limit = %#v, want 50", normalized["limit"])
	}
}
