package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"AgenticService/src/domain"
)

// normalizeArgumentsForSchema 把「該是陣列或物件、卻被當成字串送來」的參數解回結構。
//
// 小型與本機模型很常把巢狀參數整個 JSON 字串化：
//
//	{"path": "out.xlsx", "sheets": "[{\"name\":\"S\",\"rows\":[[\"a\"]]}]"}
//
// 工具的 struct 解碼會直接失敗（cannot unmarshal string into Go struct field），
// 使用者看到的就是「呼叫工具失敗」一直重複——模型的意圖完全正確，只是編碼方式錯了。
// 實測一個 session 裡 document_create 連續三次都栽在這件事上。
//
// 轉換只在 schema 明確宣告該欄位是 array／object 時才做，因此不會動到真的要收
// JSON 文字的參數（例如 file_write 的 content）。解不開就原樣保留，讓工具照常回報錯誤。
func normalizeArgumentsForSchema(arguments map[string]any, schema map[string]any) map[string]any {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(arguments) == 0 {
		return arguments
	}
	normalized := make(map[string]any, len(arguments))
	for key, value := range arguments {
		normalized[key] = value
		property, ok := properties[key].(map[string]any)
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(text)
		switch schemaKind(property) {
		case "array":
			var decoded []any
			if decodeLooseJSON(trimmed, &decoded) {
				normalized[key] = decoded
			}
		case "object":
			var decoded map[string]any
			if decodeLooseJSON(trimmed, &decoded) {
				normalized[key] = decoded
			}
		case "boolean":
			// 實測看過 overwrite 被送成字串 "true"，整個工具呼叫因此失敗。
			if value, ok := parseLooseBool(trimmed); ok {
				normalized[key] = value
			}
		case "integer", "number":
			if value, ok := parseLooseNumber(trimmed); ok {
				normalized[key] = value
			}
		}
	}
	return normalized
}

// parseLooseBool 接受模型常用的各種布林寫法。
func parseLooseBool(text string) (bool, bool) {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), `"'`)) {
	case "true", "yes", "y", "1", "on":
		return true, true
	case "false", "no", "n", "0", "off":
		return false, true
	default:
		return false, false
	}
}

// parseLooseNumber 接受帶引號的數字。
func parseLooseNumber(text string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(text), `"'`), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// decodeLooseJSON 以逐步放寬的方式解析模型送來的 JSON。
//
// 小型與本機模型產生長 JSON 時，錯的往往不是內容而是格式：漏一個右括號、多一個
// 逗號、用單引號、鍵沒加引號、寫成 Python 的 True/None。實測一筆 1,662 字的
// cell_updates 就只少了一個 }，整包被判無效，使用者看到的是「工具失敗」，
// 而資料一個字都沒少。
//
// 每一層都保守且可逆：先照原樣解析，不行才依序套用清理，任何一步成功就停。
// 全部失敗時回傳 false，讓工具照常回報它自己的錯誤，不會默默吞掉。
func decodeLooseJSON(text string, target any) bool {
	candidates := []string{text}
	if cleaned, ok := cleanDirtyJSON(text); ok {
		candidates = append(candidates, cleaned)
	}
	for _, candidate := range append([]string(nil), candidates...) {
		if repaired, ok := repairJSONBrackets(candidate); ok {
			candidates = append(candidates, repaired)
		}
	}
	for _, candidate := range candidates {
		if json.Unmarshal([]byte(candidate), target) == nil {
			return true
		}
	}
	return false
}

// cleanDirtyJSON 修掉常見的手寫式錯誤：單引號字串、未加引號的鍵、多餘的尾逗號、
// 全形引號、Python 風格的 True／False／None。字串內容一律不動。
func cleanDirtyJSON(text string) (string, bool) {
	var output strings.Builder
	changed := false
	inString := false
	escaped := false
	var quote byte
	for index := 0; index < len(text); index++ {
		character := text[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == quote:
				inString = false
				if quote == '\'' {
					output.WriteByte('"')
					changed = true
					continue
				}
			case character == '"' && quote == '\'':
				// 單引號字串裡的雙引號要轉義，否則換成雙引號後會壞掉。
				output.WriteString(`\"`)
				changed = true
				continue
			}
			output.WriteByte(character)
			continue
		}
		switch character {
		case '"':
			inString = true
			quote = '"'
			output.WriteByte(character)
		case '\'':
			inString = true
			quote = '\''
			output.WriteByte('"')
			changed = true
		case ',':
			if next := nextMeaningfulByte(text, index+1); next == '}' || next == ']' {
				changed = true
				continue
			}
			output.WriteByte(character)
		default:
			// 智慧引號（U+201C／U+201D）以 UTF-8 三個位元組出現，改成標準雙引號。
			if smart, length, ok := smartQuoteAt(text, index); ok {
				output.WriteByte(smart)
				index += length - 1
				changed = true
				continue
			}
			if literal, length, ok := pythonLiteralAt(text, index); ok {
				output.WriteString(literal)
				index += length - 1
				changed = true
				continue
			}
			if key, length, ok := bareObjectKeyAt(text, index); ok {
				output.WriteString(`"` + key + `"`)
				index += length - 1
				changed = true
				continue
			}
			output.WriteByte(character)
		}
	}
	if inString || !changed {
		return "", false
	}
	return output.String(), true
}

// smartQuoteAt 認出全形／智慧引號。
func smartQuoteAt(text string, index int) (byte, int, bool) {
	for _, mark := range []string{"\u201c", "\u201d"} {
		if strings.HasPrefix(text[index:], mark) {
			return '"', len(mark), true
		}
	}
	return 0, 0, false
}

func nextMeaningfulByte(text string, from int) byte {
	for index := from; index < len(text); index++ {
		switch text[index] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return text[index]
		}
	}
	return 0
}

// pythonLiteralAt 認出 Python 風格的字面值並換成 JSON 寫法。
func pythonLiteralAt(text string, index int) (string, int, bool) {
	for _, candidate := range []struct {
		from string
		to   string
	}{{"True", "true"}, {"False", "false"}, {"None", "null"}} {
		if strings.HasPrefix(text[index:], candidate.from) && !identifierByte(byteAt(text, index-1)) &&
			!identifierByte(byteAt(text, index+len(candidate.from))) {
			return candidate.to, len(candidate.from), true
		}
	}
	return "", 0, false
}

// bareObjectKeyAt 認出沒加引號的物件鍵（{name: 1} 或 , name: 1）。
func bareObjectKeyAt(text string, index int) (string, int, bool) {
	if !identifierStartByte(text[index]) {
		return "", 0, false
	}
	if previous := previousMeaningfulByte(text, index-1); previous != '{' && previous != ',' {
		return "", 0, false
	}
	end := index
	for end < len(text) && identifierByte(text[end]) {
		end++
	}
	if nextMeaningfulByte(text, end) != ':' {
		return "", 0, false
	}
	return text[index:end], end - index, true
}

func previousMeaningfulByte(text string, from int) byte {
	for index := from; index >= 0; index-- {
		switch text[index] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return text[index]
		}
	}
	return 0
}

func byteAt(text string, index int) byte {
	if index < 0 || index >= len(text) {
		return 0
	}
	return text[index]
}

func identifierStartByte(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || value == '_' || value == '$'
}

func identifierByte(value byte) bool {
	return identifierStartByte(value) || (value >= '0' && value <= '9')
}

// maxRepairedBrackets 是最多補幾個結尾括號。
//
// 只補少量：真正被截斷的內容補起來也是殘缺的，補太多等於默默交出一份不完整的
// 檔案。少一兩個括號才是「模型算錯層級」，那種可以救。
const maxRepairedBrackets = 4

// repairJSONBrackets 補回缺漏的 } 與 ]。
//
// 小型模型吐長 JSON 時很常漏一個右括號。實測一次 1,662 字的 cell_updates 就少了
// value 物件的 }，整包被判為無效參數，使用者只看到「工具失敗」——而內容其實一個
// 字都沒少。字串內的括號與轉義字元不列入計算。
func repairJSONBrackets(text string) (string, bool) {
	var output strings.Builder
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false
	inserted := 0
	for index := 0; index < len(text); index++ {
		character := text[index]
		if inString {
			output.WriteByte(character)
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
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			// 關錯層級時先補齊中間漏掉的括號，再關這一個。
			for len(stack) > 0 && stack[len(stack)-1] != character {
				inserted++
				if inserted > maxRepairedBrackets {
					return "", false
				}
				output.WriteByte(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				return "", false
			}
			stack = stack[:len(stack)-1]
		}
		output.WriteByte(character)
	}
	if inString {
		return "", false
	}
	// 結尾仍未關閉的層級補上。中途已經補過（關錯層級）時 stack 可能已經清空，
	// 那也算修復成功——判斷依據是「有沒有補東西」，不是「結尾還剩幾層」。
	for index := len(stack) - 1; index >= 0; index-- {
		inserted++
		if inserted > maxRepairedBrackets {
			return "", false
		}
		output.WriteByte(stack[index])
	}
	if inserted == 0 {
		return "", false
	}
	return output.String(), true
}

// schemaKind 取欄位宣告的型別。type 也可能是陣列（例如 ["array","null"]），
// 取第一個非 null 的值即可。
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

// normalizeCallArguments 依工具定義修正呼叫參數，回傳修正後的呼叫。
func normalizeCallArguments(call domain.ToolCall, definition domain.ToolDefinition) domain.ToolCall {
	if len(call.Arguments) == 0 || len(definition.InputSchema) == 0 {
		return call
	}
	call.Arguments = normalizeArgumentsForSchema(call.Arguments, definition.InputSchema)
	return call
}
