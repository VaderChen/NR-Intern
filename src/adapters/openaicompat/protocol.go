package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
			messages = append(messages, chatMessage{Role: "tool", Content: message.Content, ToolCallID: message.ToolCallID})
		}
	}
	if prompt := strings.TrimSpace(request.UserPrompt); prompt != "" {
		messages = append(messages, chatMessage{Role: "user", Content: prompt})
	}
	return messages
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
