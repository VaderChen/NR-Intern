package mcpclient

import (
	"encoding/json"
	"strings"
)

// normalizeArgumentsForSchema 把「該是陣列或物件、卻被當成字串送來」的參數解回結構。
//
// 與原生工具端同一套判讀：只在 schema 明確宣告該欄位是 array／object 時才轉換，
// 解不開就原樣保留，讓遠端 Server 照常回報它自己的驗證錯誤。
func normalizeArgumentsForSchema(arguments map[string]any, schema map[string]any) map[string]any {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(arguments) == 0 {
		return arguments
	}
	for key, value := range arguments {
		property, ok := properties[key].(map[string]any)
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		switch schemaKind(property) {
		case "array":
			var decoded []any
			if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err == nil {
				arguments[key] = decoded
			}
		case "object":
			var decoded map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err == nil {
				arguments[key] = decoded
			}
		}
	}
	return arguments
}

func schemaKind(property map[string]any) string {
	switch declared := property["type"].(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(declared))
	case []any:
		for _, item := range declared {
			if text, ok := item.(string); ok {
				if lower := strings.ToLower(strings.TrimSpace(text)); lower != "" && lower != "null" {
					return lower
				}
			}
		}
	}
	return ""
}
