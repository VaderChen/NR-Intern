package tools

import (
	"AgenticService/src/domain"
	"AgenticService/src/schemaargs"
)

// normalizeCallArguments 依工具定義修正呼叫參數，回傳修正後的呼叫。
func normalizeCallArguments(call domain.ToolCall, definition domain.ToolDefinition) domain.ToolCall {
	if len(call.Arguments) == 0 || len(definition.InputSchema) == 0 {
		return call
	}
	call.Arguments = schemaargs.Normalize(call.Arguments, definition.InputSchema)
	return call
}
