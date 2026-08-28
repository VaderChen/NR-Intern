package harness

import (
	"AgenticService/src/domain"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type ToolCallMode string

const (
	// ToolCallModeNative 使用 OpenAI-compatible tools/tool_calls 欄位。
	ToolCallModeNative ToolCallMode = "native"
	// ToolCallModeInstruction 參考舊 AgenticService，由 Harness 強制模型輸出
	// 結構化工具指令，再由後端解析與執行。這個模式不依賴 Provider 轉換工具欄位。
	ToolCallModeInstruction ToolCallMode = "instruction"
)

var (
	taggedToolInstructionPattern  = regexp.MustCompile(`(?is)<\s*(tool_call|tool_use)\s*>(.*?)<\s*/\s*(tool_call|tool_use)\s*>`)
	bracketToolInstructionPattern = regexp.MustCompile(`(?is)\[\s*(tool_call|tool_use)\s*\](.*?)\[\s*/\s*(tool_call|tool_use)\s*\]`)
)

func NormalizeToolCallMode(value string) ToolCallMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ToolCallModeInstruction):
		return ToolCallModeInstruction
	default:
		return ToolCallModeNative
	}
}

func ValidToolCallMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ToolCallModeNative), string(ToolCallModeInstruction):
		return true
	default:
		return false
	}
}

type instructionToolCatalogEntry struct {
	Name               string         `json:"name"`
	Description        string         `json:"description,omitempty"`
	InputSchema        map[string]any `json:"input_schema"`
	Platforms          []string       `json:"platforms,omitempty"`
	Capabilities       []string       `json:"capabilities,omitempty"`
	ReadOnly           bool           `json:"read_only"`
	RequiresPermission bool           `json:"requires_permission"`
}

type instructionToolUse struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Tool   string         `json:"tool"`
	Input  map[string]any `json:"input"`
	Reason string         `json:"reason,omitempty"`
}

type instructionToolResult struct {
	Type       string `json:"type"`
	ToolCallID string `json:"tool_call_id"`
	Tool       string `json:"tool"`
	IsError    bool   `json:"is_error"`
	Content    string `json:"content"`
}

// toolInstructionPrompt 建立獨立的 tools prompt，把工具名稱與參數 schema 放進
// Harness 協定。Provider 只需能輸出文字 JSON，不需要理解 OpenAI tool_calls。
func toolInstructionPrompt(definitions []domain.ToolDefinition) string {
	if len(definitions) == 0 {
		return "本輪沒有可用工具。不可聲稱已讀取檔案、執行指令或查詢外部狀態。"
	}
	catalog := make([]instructionToolCatalogEntry, 0, len(definitions))
	for _, definition := range definitions {
		schema := definition.InputSchema
		if len(schema) == 0 {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		catalog = append(catalog, instructionToolCatalogEntry{
			Name:               strings.TrimSpace(definition.Name),
			Description:        strings.TrimSpace(definition.Description),
			InputSchema:        schema,
			Platforms:          append([]string(nil), definition.Platforms...),
			Capabilities:       append([]string(nil), definition.Capabilities...),
			ReadOnly:           definition.ReadOnly,
			RequiresPermission: definition.RequiresPermission,
		})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return "工具目錄無法編碼；本輪不得呼叫或假設任何工具結果。"
	}
	return `## Harness 工具指令協定

當任務需要讀取檔案、列出目錄、搜尋、Shell、SSH、記憶或任何外部狀態時，你必須先要求後端執行工具，不可直接假設結果，也不可聲稱工具未提供。

需要工具時，每一輪只能輸出一個嚴格 JSON object，不得加入 Markdown code fence、前言、結語、to=tool、函式標記或其他文字：
{"type":"tool_use","tool":"工具名稱","input":{},"reason":"簡短理由"}

後端會執行工具，並在下一輪提供 type=tool_result 的 JSON 訊息。請根據該結果決定下一個工具；工具失敗時應修正參數或改用其他可用工具。只有已取得足以回答使用者的實際結果後，才輸出一般文字作為最終答案。最終答案不得包含工具指令 JSON。

可用工具目錄：` + string(encoded)
}

// instructionMessages 將內部結構化 transcript 轉成純文字協定，避免舊代理
// 在轉送 assistant.tool_calls 與 role=tool 時遺失欄位。
func instructionMessages(messages []domain.Message) []domain.Message {
	result := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "assistant":
			if len(message.ToolCalls) == 0 {
				result = append(result, message)
				continue
			}
			for _, call := range message.ToolCalls {
				content, _ := json.Marshal(instructionToolUse{
					Type:  "tool_use",
					ID:    call.ID,
					Tool:  call.Name,
					Input: call.Arguments,
				})
				result = append(result, domain.Message{Role: "assistant", Content: string(content)})
			}
		case "tool":
			content, _ := json.Marshal(instructionToolResult{
				Type:       "tool_result",
				ToolCallID: message.ToolCallID,
				Tool:       message.ToolName,
				IsError:    message.IsError,
				Content:    message.Content,
			})
			result = append(result, domain.Message{Role: "user", Content: string(content)})
		default:
			result = append(result, message)
		}
	}
	return result
}

// parseInstructionToolCall 接受嚴格 JSON，以及舊 AgenticService 曾使用的標籤包裝。
// 最後的 to=<known tool> 只作 Provider 格式退化時的通用相容路徑；工具名稱仍須
// 出現在目前 catalog，參數也必須是完整 JSON object，後續照常經過 Sandbox 與權限檢查。
func parseInstructionToolCall(content string, definitions []domain.ToolDefinition) (domain.ToolCall, bool, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return domain.ToolCall{}, false, nil
	}
	stripped := stripJSONFence(trimmed)
	// Provider 可能違反「只能輸出 JSON」的協定，在 tool_use 前後混入規劃文字。
	// 掃描所有平衡 JSON object，讓合法工具決策仍可進入 Harness loop；前後文字
	// 不會被當成最終回答。Sandbox、權限與工具目錄檢查仍照原流程執行。
	candidates := jsonObjects(stripped)
	candidates = append(candidates, stripped)
	if match := taggedToolInstructionPattern.FindStringSubmatch(trimmed); len(match) == 4 && strings.EqualFold(match[1], match[3]) {
		candidates = append([]string{strings.TrimSpace(match[2])}, candidates...)
	}
	if match := bracketToolInstructionPattern.FindStringSubmatch(trimmed); len(match) == 4 && strings.EqualFold(match[1], match[3]) {
		candidates = append([]string{strings.TrimSpace(match[2])}, candidates...)
	}
	for _, candidate := range candidates {
		call, matched, err := decodeInstructionToolCall(candidate)
		if err != nil {
			return domain.ToolCall{}, false, err
		}
		if matched {
			if !instructionToolExists(call.Name, definitions) {
				return domain.ToolCall{}, false, fmt.Errorf("%w: tool instruction references unavailable tool %q", domain.ErrProviderProtocol, call.Name)
			}
			return call, true, nil
		}
	}

	lower := strings.ToLower(trimmed)
	for _, definition := range definitions {
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			continue
		}
		marker := "to=" + strings.ToLower(name)
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		object := firstJSONObject(trimmed[index+len(marker):])
		if object == "" {
			return domain.ToolCall{}, false, fmt.Errorf("%w: textual tool instruction %q has no JSON input object", domain.ErrProviderProtocol, name)
		}
		arguments, err := decodeJSONObject(object)
		if err != nil {
			return domain.ToolCall{}, false, fmt.Errorf("%w: decode textual tool input for %q: %v", domain.ErrProviderProtocol, name, err)
		}
		return domain.ToolCall{ID: domain.NewID("call"), Name: name, Arguments: arguments}, true, nil
	}
	return domain.ToolCall{}, false, nil
}

func instructionToolExists(name string, definitions []domain.ToolDefinition) bool {
	name = strings.TrimSpace(name)
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(definition.Name), name) {
			return true
		}
	}
	return false
}

func decodeInstructionToolCall(candidate string) (domain.ToolCall, bool, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || !strings.HasPrefix(candidate, "{") {
		return domain.ToolCall{}, false, nil
	}
	var value map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(candidate))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return domain.ToolCall{}, false, nil
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return domain.ToolCall{}, false, nil
	}
	directive := strings.ToLower(firstRawString(value, "type", "action"))
	name := firstRawString(value, "tool", "tool_name", "name")
	if directive == "" && name != "" {
		directive = "tool_use"
	}
	if directive != "tool_use" && directive != "tool_call" && directive != "tool" {
		return domain.ToolCall{}, false, nil
	}
	if name == "" {
		return domain.ToolCall{}, false, fmt.Errorf("%w: tool instruction is missing tool name", domain.ErrProviderProtocol)
	}
	arguments := map[string]any{}
	for _, key := range []string{"input", "arguments", "args"} {
		raw, exists := value[key]
		if !exists || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return domain.ToolCall{}, false, fmt.Errorf("%w: tool instruction input must be a JSON object: %v", domain.ErrProviderProtocol, err)
		}
		break
	}
	id := firstRawString(value, "id", "tool_call_id")
	if id == "" {
		id = domain.NewID("call")
	}
	return domain.ToolCall{ID: id, Name: name, Arguments: arguments}, true, nil
}

func firstRawString(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var value string
		if raw, exists := values[key]; exists && json.Unmarshal(raw, &value) == nil {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func stripJSONFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func firstJSONObject(content string) string {
	objects := jsonObjects(content)
	if len(objects) == 0 {
		return ""
	}
	return objects[0]
}

// jsonObjects 擷取文字中所有完整、彼此分離的 JSON object。掃描器理解 JSON
// string 與跳脫字元；遇到不完整的左大括號時會從下一個位置重新嘗試，避免一段
// Provider 前言阻斷後方真正的 tool_use。
func jsonObjects(content string) []string {
	objects := []string{}
	for offset := 0; offset < len(content); {
		relativeStart := strings.Index(content[offset:], "{")
		if relativeStart < 0 {
			break
		}
		start := offset + relativeStart
		if object := balancedJSONObject(content[start:]); object != "" {
			objects = append(objects, object)
			offset = start + len(object)
			continue
		}
		offset = start + 1
	}
	return objects
}

func balancedJSONObject(content string) string {
	if content == "" || content[0] != '{' {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(content); index++ {
		character := content[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch character {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[:index+1]
			}
			if depth < 0 {
				return ""
			}
		}
	}
	return ""
}

func decodeJSONObject(content string) (map[string]any, error) {
	result := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("input must contain exactly one JSON object")
	}
	return result, nil
}
