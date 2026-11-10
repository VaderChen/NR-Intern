package openaicompat

// Claude Messages 使用獨立編解碼；只共用連線、取消與禁止重送可見輸出的政策。
import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func claudeEndpoint(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = "https://api.anthropic.com/v1"
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid Claude base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	if !strings.HasSuffix(u.Path, "/messages") {
		u.Path += "/messages"
	}
	return u.String(), nil
}

type claudeBlock struct {
	Thinking  *string         `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	// CacheControl 是 prompt caching 的斷點標記，只影響計費與延遲，不屬於內容。
	CacheControl *claudeCacheControl `json:"cache_control,omitempty"`
}

type claudeCacheControl struct {
	Type string `json:"type"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Content []claudeBlock `json:"content"`
}

// 只保存摘要，不複製 system、工具定義與歷史內容到不透明狀態。
// 與 Claude 的 prefix binding 一致，輸出預算及串流開關不屬於前綴。
type claudePrefix struct {
	System   []claudeBlock    `json:"system"`
	Tools    []map[string]any `json:"tools"`
	Messages []claudeMessage  `json:"messages"`
}

// digest 先移除快取斷點再計算。斷點不是內容，位置也可能隨請求改變；算進去會讓
// 思考簽章的綁定失效，也會讓加入快取之前保存的摘要全部對不上。
func (p claudePrefix) digest() (string, error) {
	data, err := json.Marshal(p.withoutCacheControl())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (p claudePrefix) withoutCacheControl() claudePrefix {
	strip := func(blocks []claudeBlock) []claudeBlock {
		for index := range blocks {
			if blocks[index].CacheControl == nil {
				continue
			}
			cleaned := append([]claudeBlock(nil), blocks...)
			for position := range cleaned {
				cleaned[position].CacheControl = nil
			}
			return cleaned
		}
		return blocks
	}
	result := claudePrefix{System: strip(p.System), Tools: p.Tools, Messages: p.Messages}
	for index, tool := range p.Tools {
		if _, marked := tool["cache_control"]; !marked {
			continue
		}
		if index == 0 || &result.Tools[0] == &p.Tools[0] {
			result.Tools = append([]map[string]any(nil), p.Tools...)
		}
		cleaned := make(map[string]any, len(tool))
		for key, value := range tool {
			if key != "cache_control" {
				cleaned[key] = value
			}
		}
		result.Tools[index] = cleaned
	}
	for index, message := range p.Messages {
		if cleaned := strip(message.Content); len(cleaned) > 0 && &cleaned[0] != &message.Content[0] {
			if &result.Messages[0] == &p.Messages[0] {
				result.Messages = append([]claudeMessage(nil), p.Messages...)
			}
			result.Messages[index].Content = cleaned
		}
	}
	return result
}

// claudeMarkCacheBreakpoints 標出每一輪都相同的請求開頭，讓上游可以重複使用。
//
// Claude 依 tools → system → messages 的順序比對前綴，斷點之前只要有一個位元組
// 不同就不會命中，而寫入快取比一般輸入貴。因此只標兩處確定穩定的位置：最後一個
// 工具定義，以及階段提示之前的最後一個 system 區塊。階段提示每一輪都帶著不同的
// 回合數，排在它之後的內容（含整段對話歷史）在目前的編排下無法命中，不標記。
func claudeMarkCacheBreakpoints(request domain.ModelRequest, system []claudeBlock, tools []map[string]any) {
	marker := &claudeCacheControl{Type: "ephemeral"}
	if len(tools) > 0 {
		last := make(map[string]any, len(tools[len(tools)-1])+1)
		for key, value := range tools[len(tools)-1] {
			last[key] = value
		}
		last["cache_control"] = marker
		tools[len(tools)-1] = last
	}
	stable := 0
	for _, prompt := range []string{request.SystemPrompt, request.HostPrompt, request.ToolPrompt} {
		if strings.TrimSpace(prompt) != "" {
			stable++
		}
	}
	if stable > 0 && stable <= len(system) {
		system[stable-1].CacheControl = marker
	}
}

func claudeRequestPrefixHash(body []byte) (string, error) {
	var prefix claudePrefix
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&prefix); err != nil {
		return "", err
	}
	return prefix.digest()
}

// claudeContinuationPrompt 是轉接層代替 Harness 補上的續接訊息，不是使用者輸入；
// 文字明確標示來源，避免模型把它當成使用者的新要求。
const claudeContinuationPrompt = "（系統自動續接，非使用者輸入）請依目前的系統指示繼續處理上述工作。"

func claudeContinuationMessage() claudeMessage {
	return claudeMessage{Role: "user", Content: []claudeBlock{{Type: "text", Text: claudeContinuationPrompt}}}
}

// claudeNeedsContinuation 判斷尾端是否為已結束的 assistant 訊息。尾端帶有
// tool_use 時下一則必須是 tool_result，補續接訊息只會把問題換成另一種協定
// 錯誤，因此不處理。
func claudeNeedsContinuation(messages []claudeMessage) bool {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return false
	}
	for _, block := range messages[len(messages)-1].Content {
		if block.Type == "tool_use" {
			return false
		}
	}
	return true
}

func (m *Model) claudeRequest(request domain.ModelRequest, model string, streaming bool) ([]byte, error) {
	effort, err := domain.NormalizeThinkingMode(request.ThinkingMode)
	if err != nil {
		return nil, err
	}
	// 共用選單的最低兩檔在 Claude 對應 low；不假設模型允許關閉思考，
	// 也不將 effort 塞進 thinking.type 或 reasoning_effort。
	if effort == domain.ThinkingModeNone || effort == domain.ThinkingModeMinimal {
		effort = "low"
	}
	var messages []claudeMessage
	var system []claudeBlock
	var tools []map[string]any
	for _, tool := range request.Tools {
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
	}
	for _, message := range m.messages(request) {
		text, _ := message.Content.(string)
		if message.Role == "system" || message.Role == "developer" {
			system = append(system, claudeBlock{Type: "text", Text: text})
			continue
		}
		role := message.Role
		var blocks []claudeBlock
		// 連續兩則 assistant 代表前一則是 Harness 留下的內部訊息（完成度追問、
		// 協定修復、截斷重試），當時送出的請求在它後面有一則續接訊息。這裡在
		// 相同位置補回，而且必須早於下方的前綴摘要計算，前綴才會與當時一致，
		// 思考簽章的綁定不會因此失效。
		if role == "assistant" && (text != "" || len(message.ToolCalls) > 0) && claudeNeedsContinuation(messages) {
			messages = append(messages, claudeContinuationMessage())
		}
		if state := message.Continuation; role == "assistant" && state != nil && state.Protocol == "claude-messages" && state.Scope == m.continuationScope && state.Model == model {
			prefixHash, err := (claudePrefix{System: system, Tools: tools, Messages: messages}).digest()
			if err != nil {
				return nil, err
			}
			// 舊紀錄無綁定摘要、歷史重寫、壓縮或動態 system／工具變更時，
			// 不送出失效簽章。工具呼叫及結果仍照常保留，沒有重新執行。
			if state.PrefixHash != "" && state.PrefixHash == prefixHash {
				for _, item := range state.Items {
					var thinking claudeBlock
					if err := json.Unmarshal(item, &thinking); err != nil {
						return nil, fmt.Errorf("invalid Claude thinking continuation: %w", err)
					}
					if thinking.Type != "thinking" && thinking.Type != "redacted_thinking" {
						return nil, fmt.Errorf("invalid Claude thinking continuation type")
					}
					blocks = append(blocks, thinking)
				}
			}
		}
		if role == "tool" {
			role = "user"
			blocks = append(blocks, claudeBlock{Type: "tool_result", ToolUseID: message.ToolCallID, Content: text})
		} else {
			if text != "" {
				blocks = append(blocks, claudeBlock{Type: "text", Text: text})
			}
			for _, call := range message.ToolCalls {
				input := json.RawMessage(call.Function.Arguments)
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, claudeBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if len(messages) > 0 && messages[len(messages)-1].Role == role {
			messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, blocks...)
		} else {
			messages = append(messages, claudeMessage{Role: role, Content: blocks})
		}
	}
	// Claude 不接受以 assistant 訊息結尾的對話（視為 prefill，回 400）。Harness
	// 重跑一輪時只更新 system／context 指示，歷史尾端仍是上一輪的 assistant
	// 訊息，因此在這裡補一則續接訊息。
	if claudeNeedsContinuation(messages) {
		messages = append(messages, claudeContinuationMessage())
	}
	// 必須在所有前綴摘要算完之後才標記；摘要本身也會忽略斷點。
	claudeMarkCacheBreakpoints(request, system, tools)
	limit := m.Capabilities(model).MaxOutputTokens
	if limit <= 0 {
		limit = 4096
	}
	payload := map[string]any{"model": model, "messages": messages, "max_tokens": limit, "stream": streaming}
	if effort != "" {
		payload["output_config"] = map[string]string{"effort": effort}
	}
	if len(system) > 0 {
		payload["system"] = system
	}
	if len(request.Tools) > 0 {
		payload["tools"] = tools
		choice := strings.ToLower(strings.TrimSpace(request.ToolChoice))
		if choice == "" {
			choice = "auto"
		}
		if choice == "required" {
			choice = "any"
		}
		payload["tool_choice"] = map[string]string{"type": choice}
	}
	return json.Marshal(payload)
}

type claudeUsage struct {
	Input      *int `json:"input_tokens"`
	Output     *int `json:"output_tokens"`
	CacheRead  *int `json:"cache_read_input_tokens"`
	CacheWrite *int `json:"cache_creation_input_tokens"`
}
type claudeResponse struct {
	Type       string        `json:"type"`
	Model      string        `json:"model"`
	Content    []claudeBlock `json:"content"`
	StopReason string        `json:"stop_reason"`
	Usage      claudeUsage   `json:"usage"`
}

func decodeClaude(reader io.Reader, streaming bool, model, requestID string, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	result := domain.ModelResponse{Model: model, ProviderRequestID: requestID}
	emit := func(event domain.ModelEvent) error {
		if sink != nil {
			return sink(event)
		}
		return nil
	}
	var content strings.Builder
	var reasoning strings.Builder
	var thinkingItems []json.RawMessage
	var pendingBlocks []claudeBlock
	var addCall func(claudeBlock) error
	seenNonThinking, replayableThinking := false, true
	observeBlock := func(kind string) {
		if kind == "thinking" || kind == "redacted_thinking" {
			if seenNonThinking {
				replayableThinking = false
			}
		} else {
			seenNonThinking = true
		}
	}
	thinkingBytes := 0
	keepThinking := func(block claudeBlock) error {
		if block.Type == "thinking" && (block.Thinking == nil || block.Signature == "") {
			return fmt.Errorf("Claude thinking block missing signature or text field")
		}
		if block.Type == "redacted_thinking" && block.Data == "" {
			return fmt.Errorf("Claude redacted thinking block missing data")
		}
		data, err := json.Marshal(block)
		if err != nil {
			return err
		}
		thinkingBytes += len(data)
		if thinkingBytes > 16*1024*1024 {
			return fmt.Errorf("Claude thinking state too large")
		}
		thinkingItems = append(thinkingItems, data)
		return nil
	}
	var input, output, cacheRead, cacheWrite int
	usage := func(value claudeUsage) {
		if value.Input != nil {
			input = *value.Input
		}
		if value.Output != nil {
			output = *value.Output
		}
		if value.CacheRead != nil {
			cacheRead = *value.CacheRead
		}
		if value.CacheWrite != nil {
			cacheWrite = *value.CacheWrite
		}
		result.Usage = domain.Usage{InputTokens: input + cacheRead + cacheWrite, OutputTokens: output, TotalTokens: input + cacheRead + cacheWrite + output, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite}
	}
	finish := func() (domain.ModelResponse, error) {
		truncated := result.StopReason == "max_tokens" || result.StopReason == "model_context_window_exceeded"
		// 等訊息終態再驗證內容，避免把 token 截斷誤判為 JSON／簽章錯誤。
		for _, block := range pendingBlocks {
			if block.Type == "tool_use" && result.StopReason == "tool_use" {
				if err := addCall(block); err != nil {
					return domain.ModelResponse{}, err
				}
			} else if (block.Type == "thinking" || block.Type == "redacted_thinking") && !truncated && result.StopReason != "refusal" {
				if err := keepThinking(block); err != nil {
					return domain.ModelResponse{}, err
				}
			}
		}
		if result.StopReason != "tool_use" {
			result.ToolCalls = nil
		}
		switch result.StopReason {
		case "end_turn", "stop_sequence":
			result.StopReason = "stop"
		case "tool_use":
			if len(result.ToolCalls) == 0 {
				return domain.ModelResponse{}, fmt.Errorf("Claude tool_use stop reason without tool calls")
			}
			result.StopReason = "tool_calls"
		case "max_tokens", "model_context_window_exceeded":
			result.StopReason = "length"
			result.ToolCalls = nil
		case "refusal":
			result.ToolCalls = nil
		default:
			return domain.ModelResponse{}, fmt.Errorf("Claude returned unsupported or missing stop reason %q", result.StopReason)
		}
		result.Content = content.String()
		result.Reasoning = reasoning.String()
		// domain 將文字與工具分欄保存，無法重建交錯思考的原始位置；
		// 這種情況寧可省略不透明狀態，也不重新排列有前綴綁定的區塊。
		if len(thinkingItems) > 0 && replayableThinking {
			result.Continuation = &domain.ModelContinuation{Protocol: "claude-messages", Items: thinkingItems}
		}
		if truncated {
			if err := emit(domain.ModelEvent{Type: domain.ModelEventProgress, Delta: "Claude 回覆達到輸出或上下文上限，原生工具呼叫未交付執行。請檢查 Provider 的最大輸出 Tokens 與工作大小。"}); err != nil {
				return domain.ModelResponse{}, err
			}
		}
		if err := emit(domain.ModelEvent{Type: domain.ModelEventUsage, Usage: &result.Usage}); err != nil {
			return domain.ModelResponse{}, err
		}
		return result, nil
	}
	addCall = func(block claudeBlock) error {
		if block.ID == "" || block.Name == "" {
			return fmt.Errorf("Claude tool call missing identity")
		}
		for _, call := range result.ToolCalls {
			if call.ID == block.ID {
				return fmt.Errorf("duplicate Claude tool call ID")
			}
		}
		var args map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(block.Input)))
		decoder.UseNumber()
		if err := decoder.Decode(&args); err != nil {
			return fmt.Errorf("invalid Claude tool input: %w", err)
		}
		if args == nil {
			return fmt.Errorf("Claude tool input must be an object")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("trailing Claude tool input")
		}
		result.ToolCalls = append(result.ToolCalls, domain.ToolCall{ID: block.ID, Name: block.Name, Arguments: args})
		return nil
	}
	if !streaming {
		data, err := io.ReadAll(io.LimitReader(reader, 16*1024*1024+1))
		if err != nil {
			return domain.ModelResponse{}, err
		}
		if len(data) > 16*1024*1024 {
			return domain.ModelResponse{}, fmt.Errorf("Claude response too large")
		}
		var response claudeResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return domain.ModelResponse{}, err
		}
		if response.Type != "message" {
			return domain.ModelResponse{}, fmt.Errorf("invalid Claude response type")
		}
		result.Model = firstText(response.Model, model)
		result.StopReason = response.StopReason
		usage(response.Usage)
		for _, block := range response.Content {
			observeBlock(block.Type)
			switch block.Type {
			case "thinking", "redacted_thinking":
				pendingBlocks = append(pendingBlocks, block)
				delta := ""
				if block.Thinking != nil {
					delta = *block.Thinking
				}
				reasoning.WriteString(delta)
				if err := emit(domain.ModelEvent{Type: domain.ModelEventThinkingDelta, Delta: delta}); err != nil {
					return domain.ModelResponse{}, err
				}
			case "text":
				content.WriteString(block.Text)
				if err := emit(domain.ModelEvent{Type: domain.ModelEventTextDelta, Delta: block.Text}); err != nil {
					return domain.ModelResponse{}, err
				}
			case "tool_use":
				pendingBlocks = append(pendingBlocks, block)
			default:
				return domain.ModelResponse{}, fmt.Errorf("unsupported Claude content block %q", block.Type)
			}
		}
		return finish()
	}
	var block claudeBlock
	var arguments strings.Builder
	var thinkingText, signature strings.Builder
	active, index, started, stopped := false, -1, false, false
	closeBlock := func() {
		active = false
		if block.Type == "thinking" {
			value := thinkingText.String()
			block.Thinking = &value
			block.Signature = signature.String()
		}
		if block.Type == "tool_use" && arguments.Len() > 0 {
			block.Input = json.RawMessage(arguments.String())
		}
		if block.Type != "text" {
			pendingBlocks = append(pendingBlocks, block)
		}
	}
	consume := func(data string) error {
		var event struct {
			Type    string         `json:"type"`
			Index   int            `json:"index"`
			Message claudeResponse `json:"message"`
			Block   claudeBlock    `json:"content_block"`
			Usage   claudeUsage    `json:"usage"`
			Delta   struct {
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				Type      string `json:"type"`
				Text      string `json:"text"`
				JSON      string `json:"partial_json"`
				Stop      string `json:"stop_reason"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return providerStreamDecodeError("Claude stream", err, requestID, "")
		}
		if event.Type == "error" {
			return &ProviderError{Operation: "Claude stream", Code: event.Error.Type, Message: event.Error.Message, RequestID: requestID, Retryable: event.Error.Type == "overloaded_error" || event.Error.Type == "api_error" || event.Error.Type == "rate_limit_error"}
		}
		if event.Type == "ping" {
			return nil
		}
		if event.Type == "message_start" {
			if started {
				return fmt.Errorf("duplicate Claude message_start")
			}
			started = true
			usage(event.Message.Usage)
			result.Model = firstText(event.Message.Model, model)
			return nil
		}
		if !started {
			return fmt.Errorf("Claude stream missing message_start")
		}
		switch event.Type {
		case "content_block_start":
			if active || event.Index != index+1 {
				return fmt.Errorf("invalid Claude block sequence")
			}
			index = event.Index
			active = true
			block = event.Block
			observeBlock(block.Type)
			arguments.Reset()
			thinkingText.Reset()
			signature.Reset()
			if block.Type == "thinking" || block.Type == "redacted_thinking" {
				if block.Thinking != nil {
					thinkingText.WriteString(*block.Thinking)
					reasoning.WriteString(*block.Thinking)
				}
				signature.WriteString(block.Signature)
				return emit(domain.ModelEvent{Type: domain.ModelEventThinkingDelta, Delta: thinkingText.String()})
			}
			if block.Type == "tool_use" {
				return emit(domain.ModelEvent{Type: domain.ModelEventToolCallDelta, ToolCall: &domain.ToolCallDelta{Index: index, ID: block.ID, Name: block.Name}})
			}
			if block.Type != "text" {
				return fmt.Errorf("unsupported Claude content block %q", block.Type)
			}
			content.WriteString(block.Text)
			if block.Text != "" {
				return emit(domain.ModelEvent{Type: domain.ModelEventTextDelta, Delta: block.Text})
			}
		case "content_block_delta":
			if !active || event.Index != index {
				return fmt.Errorf("Claude delta without active block")
			}
			switch {
			case block.Type == "thinking" && event.Delta.Type == "thinking_delta":
				if thinkingText.Len()+len(event.Delta.Thinking) > 16*1024*1024 {
					return fmt.Errorf("Claude thinking text too large")
				}
				thinkingText.WriteString(event.Delta.Thinking)
				reasoning.WriteString(event.Delta.Thinking)
				return emit(domain.ModelEvent{Type: domain.ModelEventThinkingDelta, Delta: event.Delta.Thinking})
			case block.Type == "thinking" && event.Delta.Type == "signature_delta":
				if signature.Len()+len(event.Delta.Signature) > 16*1024*1024 {
					return fmt.Errorf("Claude thinking signature too large")
				}
				signature.WriteString(event.Delta.Signature)
				return nil
			case block.Type == "text" && event.Delta.Type == "text_delta":
				content.WriteString(event.Delta.Text)
				return emit(domain.ModelEvent{Type: domain.ModelEventTextDelta, Delta: event.Delta.Text})
			case block.Type == "tool_use" && event.Delta.Type == "input_json_delta":
				if arguments.Len()+len(event.Delta.JSON) > 16*1024*1024 {
					return fmt.Errorf("Claude tool input too large")
				}
				arguments.WriteString(event.Delta.JSON)
				return emit(domain.ModelEvent{Type: domain.ModelEventToolCallDelta, Delta: event.Delta.JSON, ToolCall: &domain.ToolCallDelta{Index: index, ID: block.ID, Name: block.Name}})
			default:
				return fmt.Errorf("invalid Claude delta type")
			}
		case "content_block_stop":
			if !active || event.Index != index {
				return fmt.Errorf("invalid Claude block stop")
			}
			closeBlock()
		case "message_delta":
			if event.Delta.Stop != "" {
				result.StopReason = event.Delta.Stop
			}
			usage(event.Usage)
		case "message_stop":
			if active {
				// 依官方 SDK 的累積快照處理訊息終態；完整 JSON 與簽章仍由 finish 驗證。
				// 不把缺少區塊 stop 直接當成傳輸失敗，也不以 EOF 猜測訊息已完成。
				closeBlock()
			}
			stopped = true
		}
		return nil
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(lines) > 0 {
				if err := consume(strings.Join(lines, "\n")); err != nil {
					return domain.ModelResponse{}, err
				}
				lines = nil
				if stopped {
					return finish()
				}
			}
		} else if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.ModelResponse{}, err
	}
	return domain.ModelResponse{}, &ProviderError{Operation: "Claude stream", Message: "stream ended before message_stop", Retryable: true, RequestID: requestID}
}

func (m *Model) listClaudeModels(ctx context.Context) ([]string, error) {
	u, _ := url.Parse(strings.TrimSuffix(m.endpoint, "/messages") + "/models")
	var models []string
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		for key, value := range m.extraHeaders {
			req.Header.Set(key, value)
		}
		if err = m.applyAuthorization(ctx, req); err != nil {
			return nil, err
		}
		response, err := m.client.Do(req)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			e, _ := providerHTTPError(response, "")
			response.Body.Close()
			return nil, e
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxModelCatalogBytes+1))
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > maxModelCatalogBytes {
			return nil, fmt.Errorf("Claude model catalog too large")
		}
		var catalog struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err = json.Unmarshal(data, &catalog); err != nil {
			return nil, err
		}
		for _, model := range catalog.Data {
			if model.ID != "" && !seen[model.ID] {
				seen[model.ID] = true
				models = append(models, model.ID)
			}
		}
		if !catalog.HasMore {
			sort.Strings(models)
			return models, nil
		}
		query := u.Query()
		if catalog.LastID == "" || catalog.LastID == query.Get("after_id") {
			return nil, fmt.Errorf("Claude model catalog cursor did not advance")
		}
		query.Set("after_id", catalog.LastID)
		u.RawQuery = query.Encode()
	}
	return nil, fmt.Errorf("Claude model catalog pagination limit exceeded")
}
