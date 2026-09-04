package tools

import (
	"testing"

	"AgenticService/src/domain"
)

// 校正規則本身由 schemaargs 的測試涵蓋，端到端（含真的產出 DOCX）由
// documents 的測試涵蓋。這一層只負責一件事：把工具定義裡的 schema 交出去。
// 少了這道接線，所有校正規則都正確卻一條也沒生效。
func TestNormalizeCallArgumentsAppliesTheToolSchema(t *testing.T) {
	definition := domain.ToolDefinition{
		Name: "document_create",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"overwrite": map[string]any{"type": "boolean"},
				"sheets":    map[string]any{"type": "array"},
			},
		},
	}
	call := domain.ToolCall{ID: "call_1", Name: "document_create", Arguments: map[string]any{
		"overwrite": "true",
		"sheets":    `[{"name":"設備","rows":[["部門","代碼"]]}]`,
	}}

	normalized := normalizeCallArguments(call, definition)

	if normalized.Arguments["overwrite"] != true {
		t.Fatalf("overwrite = %#v, want the schema to have been applied", normalized.Arguments["overwrite"])
	}
	if _, ok := normalized.Arguments["sheets"].([]any); !ok {
		t.Fatalf("sheets stayed a string: %T", normalized.Arguments["sheets"])
	}
	if normalized.ID != "call_1" || normalized.Name != "document_create" {
		t.Fatalf("the call identity must survive normalization: %#v", normalized)
	}
}

// 沒有 schema 就沒有可依據的規則，原樣送出去。
func TestNormalizeCallArgumentsSkipsToolsWithoutASchema(t *testing.T) {
	call := domain.ToolCall{Name: "anything", Arguments: map[string]any{"value": "true"}}
	normalized := normalizeCallArguments(call, domain.ToolDefinition{Name: "anything"})
	if normalized.Arguments["value"] != "true" {
		t.Fatalf("value was altered without a schema: %#v", normalized.Arguments["value"])
	}
}
