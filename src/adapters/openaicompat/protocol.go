package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []chatMessage  `json:"messages"`
	Tools           []functionTool `json:"tools,omitempty"`
	ToolChoice      string         `json:"tool_choice,omitempty"`
	Stream          bool           `json:"stream"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content,omitempty"`
	Refusal          string     `json:"refusal,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type functionTool struct {
	Type     string             `json:"type"`
	Function functionDefinition `json:"function"`
}

type functionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamChunk struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []streamChoice `json:"choices,omitempty"`
	Usage   usagePayload   `json:"usage,omitempty"`
	Error   *apiChunkError `json:"error,omitempty"`
}

type apiChunkError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content any    `json:"content,omitempty"`
	Refusal string `json:"refusal,omitempty"`
	// 相容服務對思考內容的欄位名稱不一致：DeepSeek 用 reasoning_content，OpenRouter 用 reasoning。
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

type jsonResponse struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []jsonChoice   `json:"choices"`
	Usage   usagePayload   `json:"usage,omitempty"`
	Error   *apiChunkError `json:"error,omitempty"`
}

type jsonChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type usagePayload struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type partialCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func (m *Model) messages(request domain.ModelRequest) []chatMessage {
	messages := make([]chatMessage, 0, len(request.History)+6)
	if prompt := strings.TrimSpace(request.SystemPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: m.instructionRole, Content: prompt})
	}
	if prompt := strings.TrimSpace(request.HostPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: m.instructionRole, Content: prompt})
	}
	if prompt := strings.TrimSpace(request.ToolPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: m.instructionRole, Content: prompt})
	}
	if prompt := strings.TrimSpace(request.PhasePrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: m.instructionRole, Content: prompt})
	}
	// ContextPrompt 是記憶、壓縮摘要、Sandbox 與執行狀態等資料，不提升成
	// system/developer 指令；以獨立 user context 訊息提供並明確保持資料邊界。
	if prompt := strings.TrimSpace(request.ContextPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: prompt})
	}
	for _, message := range request.History {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			messages = append(messages, chatMessage{Role: "user", Content: message.Content})
		case "assistant":
			messages = append(messages, chatMessage{Role: "assistant", Content: message.Content, ToolCalls: encodeToolCalls(message.ToolCalls)})
		case "tool":
			// 空的工具結果要換成明確的文字。工具正常完成卻沒有輸出是常態
			// （讀到空檔案、grep 沒有命中），但把空字串原樣送出去，上游或
			// 中間的相容層很容易把它當成「這個欄位不存在」——實測經由
			// 代理轉成 Responses 協定時，空的 tool content 讓代理省略了
			// output 欄位，上游直接回 400 Missing required parameter。
			// 順帶讓模型看得出「這個工具沒有輸出」，而不是收到一段空白
			// 自己揣測。與 codexInput 的處理保持一致。
			content := message.Content
			if strings.TrimSpace(content) == "" {
				content = emptyToolResult
			}
			messages = append(messages, chatMessage{Role: "tool", Content: content, ToolCallID: message.ToolCallID})
		}
	}
	if prompt := strings.TrimSpace(request.UserPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: prompt})
	}
	return messages
}

// DescribeChatMessages 回傳訊息序列的結構摘要，供事後診斷用。
//
// 這一版存在的理由很具體：上游拒收時回報的是索引（例如 input[40].output），
// 而索引本身說不出那一項是什麼。更麻煩的是中間如果有相容代理，失敗可能根本
// 不是 4xx——實測遇過代理把 400 包成 200 加一段錯誤文字送回來，Harness 全程
// 看不出有任何異常，於是連「該去看什麼」都無從得知。
//
// 只記結構，不記內容：需要回答的問題是形狀，而把整段對話抄進日誌檔的診斷
// 不值得擁有。工具結果額外標出空與非空——空值被中途某一層的 omitempty
// 吃掉，正是實際發生過的事故。
func DescribeChatMessages(messages []chatMessage) string {
	var builder strings.Builder
	for index, message := range messages {
		if index > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(":")
		switch {
		case message.ToolCallID != "":
			state := "set"
			if text, ok := message.Content.(string); !ok || strings.TrimSpace(text) == "" {
				state = "empty"
			}
			builder.WriteString("out/" + shortCallID(message.ToolCallID) + "/" + state)
		case len(message.ToolCalls) > 0:
			builder.WriteString(message.Role + "/calls:" + strconv.Itoa(len(message.ToolCalls)))
			for _, call := range message.ToolCalls {
				builder.WriteString("/" + shortCallID(call.ID))
			}
		default:
			builder.WriteString(message.Role)
		}
	}
	return builder.String()
}

func functionTools(definitions []domain.ToolDefinition) []functionTool {
	result := make([]functionTool, 0, len(definitions))
	for _, definition := range definitions {
		parameters := definition.InputSchema
		if len(parameters) == 0 {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, functionTool{Type: "function", Function: functionDefinition{Name: definition.Name, Description: definition.Description, Parameters: parameters}})
	}
	return result
}

func encodeToolCalls(calls []domain.ToolCall) []toolCall {
	result := make([]toolCall, 0, len(calls))
	for _, call := range calls {
		arguments, _ := json.Marshal(call.Arguments)
		result = append(result, toolCall{ID: call.ID, Type: "function", Function: functionCall{Name: call.Name, Arguments: string(arguments)}})
	}
	return result
}

func decodeToolCalls(calls []toolCall) ([]domain.ToolCall, error) {
	result := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			return nil, fmt.Errorf("OpenAI-compatible response contains a tool call without a function name")
		}
		arguments := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			decoder := json.NewDecoder(strings.NewReader(call.Function.Arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&arguments); err != nil {
				return nil, fmt.Errorf("decode tool arguments for %s: %w", name, err)
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				return nil, fmt.Errorf("decode tool arguments for %s: arguments must contain one JSON object", name)
			}
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = domain.NewID("call")
		}
		result = append(result, domain.ToolCall{ID: id, Name: name, Arguments: arguments})
	}
	return result, nil
}

func finalizeCalls(partials map[int]*partialCall) ([]domain.ToolCall, error) {
	indexes := make([]int, 0, len(partials))
	for index := range partials {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	encoded := make([]toolCall, 0, len(indexes))
	for _, index := range indexes {
		partial := partials[index]
		encoded = append(encoded, toolCall{ID: partial.ID, Function: functionCall{Name: partial.Name, Arguments: partial.Arguments.String()}})
	}
	return decodeToolCalls(encoded)
}

func mergeName(existing, fragment string) string {
	if fragment == "" || fragment == existing || strings.HasSuffix(existing, fragment) {
		return existing
	}
	if existing == "" || strings.HasPrefix(fragment, existing) {
		return fragment
	}
	return existing + fragment
}

func contentText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		var content strings.Builder
		for _, item := range typed {
			content.WriteString(contentText(item))
		}
		return content.String()
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		if content, ok := typed["content"].(string); ok {
			return content
		}
	}
	return ""
}

func (usage usagePayload) domainUsage(current domain.Usage) domain.Usage {
	if usage.PromptTokens != 0 {
		current.InputTokens = usage.PromptTokens
	}
	if usage.CompletionTokens != 0 {
		current.OutputTokens = usage.CompletionTokens
	}
	if usage.TotalTokens != 0 {
		current.TotalTokens = usage.TotalTokens
	}
	return current
}
