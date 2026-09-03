package harness

import (
	"AgenticService/src/domain"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
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
	// toolInstructionPrefixPattern 只比對 JSON object 的開頭鍵，避免把一般文字裡
	// 提到的大括號誤判成被截斷的工具指令。
	toolInstructionPrefixPattern = regexp.MustCompile(`(?is)^\{\s*"(type|tool|tool_name|action)"\s*:\s*"[^"]*"`)
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
	Name string `json:"name"`
	// Label 是工具的顯示名稱。MCP Server 的 title 常常比 name 更貼近使用者的說法
	// （例如「查詢製令」對上 query_work_orders），少了它模型就得自己跨語言猜。
	Label        string         `json:"label,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	// Platforms 與 Capabilities 刻意不進模型看到的目錄：那是工具目錄 UI 用的中繼資料，
	// 對「該叫哪個工具、參數怎麼填」沒有幫助，卻要每一輪重送一次。
	ReadOnly           bool `json:"read_only"`
	RequiresPermission bool `json:"requires_permission"`
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
			Label:              strings.TrimSpace(definition.Label),
			Description:        strings.TrimSpace(definition.Description),
			InputSchema:        schema,
			OutputSchema:       definition.OutputSchema,
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

需要工具時只輸出嚴格 JSON，不得加入 Markdown code fence、前言、結語、to=tool、函式標記或其他文字：
{"type":"tool_use","tool":"工具名稱","input":{},"reason":"簡短理由"}

一個問題需要多份彼此獨立的資料時（例如同時要部門數與人員數），在同一輪直接輸出多個 tool_use object 或一個 JSON 陣列，Harness 會一起執行；不要先用一輪描述打算怎麼分次查。彼此有相依關係時才分輪，先取得前一個結果再決定下一個。

後端會執行工具，並在下一輪提供 type=tool_result 的 JSON 訊息。請根據該結果決定下一個工具；工具失敗時應修正參數或改用其他可用工具。只有已取得足以回答使用者的實際結果後，才輸出一般文字作為最終答案。最終答案不得包含工具指令 JSON。

若工具結果指出 input_required，請依結果中的輸入請求補齊資料，使用同一工具重試；MCP 的多輪控制欄位只能放在工具 input 的 "_mcp_input_responses" 與 "_mcp_request_state"，不可猜測或省略請求 ID。

可用工具目錄：` + string(encoded) + serverInstructionsSection(definitions)
}

// serverInstructionsSection 把同一個 MCP Server 的說明只放一次。
//
// Server instructions 屬於整個 Server，過去被複製到該 Server 的每一個工具定義裡：
// 20 個工具就等於同一段文字在每一輪的提示中出現 20 次。
func serverInstructionsSection(definitions []domain.ToolDefinition) string {
	seen := map[string]bool{}
	lines := make([]string, 0, 4)
	for _, definition := range definitions {
		instructions := strings.TrimSpace(definition.ServerInstructions)
		if instructions == "" || seen[instructions] {
			continue
		}
		seen[instructions] = true
		if server := mcpServerID(definition); server != "" {
			lines = append(lines, "- "+server+"："+instructions)
			continue
		}
		lines = append(lines, "- "+instructions)
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nMCP Server 說明（同一個 Server 的所有工具共用）：\n" + strings.Join(lines, "\n")
}

func mcpServerID(definition domain.ToolDefinition) string {
	for _, capability := range definition.Capabilities {
		if value := strings.TrimSpace(capability); strings.HasPrefix(strings.ToLower(value), "mcp:") {
			return strings.TrimSpace(value[len("mcp:"):])
		}
	}
	name := strings.TrimSpace(definition.Name)
	if !strings.HasPrefix(strings.ToLower(name), "mcp__") {
		return ""
	}
	remainder := name[len("mcp__"):]
	if index := strings.Index(remainder, "__"); index > 0 {
		return remainder[:index]
	}
	return ""
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
// parseInstructionToolCalls 允許同一輪輸出多個工具指令。
//
// 複合問題（「有多少部門跟人員」）需要兩份彼此獨立的資料。過去協定限制一輪只能輸出
// 一個 JSON object，模型只好花回合去規劃怎麼分次查——畫面上就是一句
// 「Planning parallel queries…」然後什麼都沒發生。實際執行端本來就支援一輪多個工具
// 呼叫（唯讀且免核准的會並行），因此協定跟著放寬。
func parseInstructionToolCalls(content string, definitions []domain.ToolDefinition) ([]domain.ToolCall, bool, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, false, nil
	}
	stripped := stripJSONFence(trimmed)
	calls := make([]domain.ToolCall, 0, 2)
	for _, candidate := range jsonObjects(stripped) {
		call, matched, err := decodeInstructionToolCall(candidate)
		if err != nil {
			return nil, false, err
		}
		if !matched {
			continue
		}
		if !instructionToolExists(call.Name, definitions) {
			return nil, false, fmt.Errorf("%w: tool instruction references unavailable tool %q", domain.ErrProviderProtocol, call.Name)
		}
		calls = append(calls, call)
		if len(calls) == maxParallelTools {
			break
		}
	}
	if len(calls) > 1 {
		return calls, true, nil
	}
	// 單一指令仍走原本的完整比對，涵蓋標籤包裝與 to=<tool> 等退化格式。
	call, matched, err := parseInstructionToolCall(content, definitions)
	if err != nil || !matched {
		return nil, false, err
	}
	return []domain.ToolCall{call}, true, nil
}

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
	if unterminatedToolInstruction(trimmed) {
		return domain.ToolCall{}, false, fmt.Errorf("%w: tool instruction JSON is incomplete, most likely cut off by the output length limit; send a smaller instruction (for example write long scripts to a file first, then run the file)", domain.ErrProviderProtocol)
	}
	return domain.ToolCall{}, false, nil
}

// unterminatedToolInstruction 辨識「開頭像工具指令、但 JSON 沒有收尾」的輸出。
//
// 模型把大段腳本塞進單一指令時很容易被輸出長度上限截斷，截斷後的 JSON 不是平衡
// object，原本會被當成一般文字直接回覆使用者——畫面上就出現半截的 tool_use JSON，
// 甚至連參數裡的憑證都一起顯示。這種輸出必須走協定修正流程，不能當成最終回答。
func unterminatedToolInstruction(content string) bool {
	for offset := 0; offset < len(content); {
		relativeStart := strings.Index(content[offset:], "{")
		if relativeStart < 0 {
			return false
		}
		start := offset + relativeStart
		remainder := content[start:]
		if object := balancedJSONObject(remainder); object != "" {
			// 完整的 object 內部不可能藏著被截斷的指令，直接跳過整段。
			offset = start + len(object)
			continue
		}
		if toolInstructionPrefixPattern.MatchString(remainder) {
			return true
		}
		offset = start + 1
	}
	return false
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

// toolArgumentsLeak 判斷模型是不是把工具「參數」當成回答輸出，而不是實際呼叫工具。
//
// 原生工具呼叫模式下，Harness 只認 tool_calls 欄位；模型若把參數寫進 content，
// 這一輪就會被當成最終回答，使用者畫面上出現的是一整片 JSON。實測就是這樣：
// 使用者要一份 Excel，收到的是幾百個 {"cell":"A1","sheet":"設備清單","value":...}。
//
// 判定刻意保守——內容必須是合法 JSON，而且形狀要對得上某個可用工具的 schema
// （物件對上工具參數、陣列對上某個 array 參數的 items），並且要涵蓋該處的必填欄位。
// 使用者本來就要求輸出 JSON 的情況不該被誤判成協定錯誤。
func toolArgumentsLeak(content string, definitions []domain.ToolDefinition) string {
	trimmed := stripJSONFence(strings.TrimSpace(content))
	if trimmed == "" {
		return ""
	}
	for _, candidate := range leakCandidates(trimmed) {
		if name := matchToolArgumentShape(candidate, definitions); name != "" {
			return name
		}
	}
	return ""
}

// leakCandidates 取出內容裡可能是工具參數的 JSON 片段。
//
// 模型不一定只吐 JSON：實測看過「[] AI Agent [{"text":"設備狀況總覽表",...}]」，
// 前面帶著雜訊。整段解析必然失敗，那一大片參數就當成答案顯示出來了。
// 因此除了整段之外，也掃出內嵌的平衡括號片段逐一比對。
func leakCandidates(trimmed string) []string {
	candidates := []string{trimmed}
	for _, opener := range []byte{'[', '{'} {
		for offset := 0; offset < len(trimmed); {
			index := strings.IndexByte(trimmed[offset:], opener)
			if index < 0 {
				break
			}
			start := offset + index
			span := balancedJSONSpan(trimmed[start:])
			if span == "" {
				offset = start + 1
				continue
			}
			// 太短的片段（例如空陣列）只會製造誤判。
			if len(span) >= 40 {
				candidates = append(candidates, span)
			}
			offset = start + len(span)
		}
	}
	return candidates
}

// balancedJSONSpan 從開頭的 [ 或 { 取出平衡的 JSON 片段。
func balancedJSONSpan(content string) string {
	if content == "" || (content[0] != '[' && content[0] != '{') {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(content); index++ {
		character := content[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return content[:index+1]
			}
		}
	}
	return ""
}

func matchToolArgumentShape(candidate string, definitions []domain.ToolDefinition) string {
	var decoded any
	if err := json.Unmarshal([]byte(candidate), &decoded); err != nil {
		return ""
	}
	switch value := decoded.(type) {
	case map[string]any:
		for _, definition := range definitions {
			if schemaCoversKeys(definition.InputSchema, objectKeys(value)) {
				return definition.Name
			}
		}
	case []any:
		keys := arrayElementKeys(value)
		if len(keys) == 0 {
			return ""
		}
		for _, definition := range definitions {
			properties, ok := definition.InputSchema["properties"].(map[string]any)
			if !ok {
				continue
			}
			for _, property := range properties {
				item, ok := property.(map[string]any)
				if !ok || !strings.EqualFold(schemaTypeName(item), "array") {
					continue
				}
				if items, ok := item["items"].(map[string]any); ok && schemaCoversKeys(items, keys) {
					return definition.Name
				}
			}
		}
	}
	return ""
}

// schemaCoversKeys 回報這組鍵是否正好落在 schema 宣告的欄位內，且涵蓋必填欄位。
func schemaCoversKeys(schema map[string]any, keys []string) bool {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 || len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		if _, exists := properties[key]; !exists {
			return false
		}
	}
	// 必填欄位只要對上一個就算數，不要求全到齊。
	//
	// 一度要求全部必填欄位都出現，結果實測漏掉了真實案例：模型印出的是
	// {"cell":"sheet0/1/1","value":"設備狀況報告"}，少了 schema 標為必填的 sheet，
	// 於是判定不成立，那一大片 JSON 就當成答案顯示給使用者。參數不完整仍然是參數。
	required := schemaRequiredNames(schema)
	if len(required) > 0 {
		for _, name := range required {
			if schemaKeyPresent(keys, name) {
				return true
			}
		}
		return false
	}
	// 沒有必填欄位的 schema 太容易被任意 JSON 命中，要求至少對上兩個欄位。
	return len(keys) >= 2
}

func schemaRequiredNames(schema map[string]any) []string {
	switch declared := schema["required"].(type) {
	case []string:
		return declared
	case []any:
		names := make([]string, 0, len(declared))
		for _, item := range declared {
			if text, ok := item.(string); ok {
				names = append(names, text)
			}
		}
		return names
	}
	return nil
}

func schemaTypeName(schema map[string]any) string {
	switch declared := schema["type"].(type) {
	case string:
		return declared
	case []any:
		for _, item := range declared {
			if text, ok := item.(string); ok && !strings.EqualFold(text, "null") {
				return text
			}
		}
	}
	return ""
}

func schemaKeyPresent(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func objectKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// arrayElementKeys 取陣列元素的鍵集合；元素必須全部是物件，否則不算工具參數。
func arrayElementKeys(values []any) []string {
	seen := map[string]bool{}
	for _, item := range values {
		object, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		for key := range object {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
