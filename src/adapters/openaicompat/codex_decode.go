package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type codexCompletedResponse struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Output []codexOutputItem `json:"output"`
	Usage  codexUsage        `json:"usage"`
}

type codexOutputItem struct {
	ID        string             `json:"id,omitempty"`
	Type      string             `json:"type"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Content   []codexContentPart `json:"content,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexStreamState struct {
	partials        map[int]*partialCall
	indexes         map[string]int
	arguments       map[string]string
	metadataEmitted map[string]bool
	hasToolCalls    bool
}

func decodeCodexStream(reader io.Reader, fallbackModel, requestID, clientRequestID string, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	result := domain.ModelResponse{ProviderID: "openai-codex", ProviderRequestID: requestID, Model: fallbackModel}
	state := codexStreamState{
		partials:        map[int]*partialCall{},
		indexes:         map[string]int{},
		arguments:       map[string]string{},
		metadataEmitted: map[string]bool{},
	}
	var content strings.Builder
	var reasoning strings.Builder
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	dataLines := []string{}
	eventName := ""
	sawEvent := false
	sawTerminal := false

	consume := func(name, data string) error {
		data = strings.TrimSpace(data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			sawTerminal = true
			return nil
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode Codex Responses stream event: %w", err)
		}
		sawEvent = true
		eventType := firstText(stringValue(event["type"]), name)
		switch eventType {
		case "response.output_text.delta":
			delta := stringValue(event["delta"])
			content.WriteString(delta)
			return emitModelDelta(sink, domain.ModelEventTextDelta, delta)
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			delta := stringValue(event["delta"])
			reasoning.WriteString(delta)
			return emitModelDelta(sink, domain.ModelEventThinkingDelta, delta)
		case "response.output_item.added", "response.output_item.done":
			item, _ := event["item"].(map[string]any)
			if !strings.EqualFold(strings.TrimSpace(stringValue(item["type"])), "function_call") {
				return nil
			}
			return consumeCodexFunctionItem(event, item, &state, sink)
		case "response.function_call_arguments.delta":
			return consumeCodexArgumentDelta(event, &state, sink)
		case "response.function_call_arguments.done":
			return consumeCodexArgumentDone(event, &state, sink)
		case "response.completed":
			if raw := event["response"]; raw != nil {
				encoded, _ := json.Marshal(raw)
				var completed codexCompletedResponse
				if err := json.Unmarshal(encoded, &completed); err != nil {
					return fmt.Errorf("decode completed Codex response: %w", err)
				}
				if completed.Model != "" {
					result.Model = completed.Model
				}
				if err := consumeCodexCompleted(completed, &content, &state, sink); err != nil {
					return err
				}
				result.Usage = completed.Usage.domainUsage()
				if err := emitUsage(sink, result.Usage); err != nil {
					return err
				}
			}
			sawTerminal = true
			return nil
		case "response.failed", "response.incomplete", "error":
			return codexStreamError(eventType, event, requestID, clientRequestID)
		default:
			return nil
		}
	}

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		err := consume(eventName, strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		eventName = ""
		return err
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return domain.ModelResponse{}, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			if err := consume(eventName, strings.TrimSpace(line)); err != nil {
				return domain.ModelResponse{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.ModelResponse{}, &ProviderError{Operation: "Codex stream read", Message: err.Error(), RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true, Cause: err}
	}
	if err := flush(); err != nil {
		return domain.ModelResponse{}, err
	}
	if !sawEvent {
		return domain.ModelResponse{}, &ProviderError{Operation: "Codex stream read", Message: "stream ended without a Responses event", RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true}
	}
	if !sawTerminal {
		return domain.ModelResponse{}, &ProviderError{Operation: "Codex stream read", Message: "stream ended before response.completed", RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true}
	}
	result.Content = strings.TrimSpace(content.String())
	result.Reasoning = strings.TrimSpace(reasoning.String())
	calls, err := finalizeCalls(state.partials)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	result.ToolCalls = calls
	if len(calls) > 0 {
		result.StopReason = "tool_calls"
	} else {
		result.StopReason = "stop"
	}
	return result, nil
}

func consumeCodexFunctionItem(event, item map[string]any, state *codexStreamState, sink ports.ModelEventSink) error {
	key := codexCallKey(event, item)
	index := state.callIndex(event, key)
	partial := state.partial(index)
	callID := firstText(stringValue(item["call_id"]), stringValue(item["id"]), key)
	name := strings.TrimSpace(stringValue(item["name"]))
	if callID != "" {
		partial.ID = callID
	}
	partial.Name = mergeName(partial.Name, name)
	if !state.metadataEmitted[key] {
		if sink != nil {
			if err := sink(domain.ModelEvent{Type: domain.ModelEventToolCallDelta, ToolCall: &domain.ToolCallDelta{Index: index, ID: partial.ID, Name: partial.Name}}); err != nil {
				return err
			}
		}
		state.metadataEmitted[key] = true
	}
	state.hasToolCalls = true
	return appendCodexArguments(index, key, stringValue(item["arguments"]), state, sink)
}

func consumeCodexArgumentDelta(event map[string]any, state *codexStreamState, sink ports.ModelEventSink) error {
	key := codexCallKey(event, nil)
	index := state.callIndex(event, key)
	delta := stringValue(event["delta"])
	if delta == "" {
		return nil
	}
	state.partial(index).Arguments.WriteString(delta)
	state.arguments[key] += delta
	state.hasToolCalls = true
	if sink == nil {
		return nil
	}
	partial := state.partial(index)
	return sink(domain.ModelEvent{Type: domain.ModelEventToolCallDelta, Delta: delta, ToolCall: &domain.ToolCallDelta{Index: index, ID: partial.ID, Name: partial.Name}})
}

func consumeCodexArgumentDone(event map[string]any, state *codexStreamState, sink ports.ModelEventSink) error {
	key := codexCallKey(event, nil)
	index := state.callIndex(event, key)
	return appendCodexArguments(index, key, stringValue(event["arguments"]), state, sink)
}

func appendCodexArguments(index int, key, complete string, state *codexStreamState, sink ports.ModelEventSink) error {
	if complete == "" {
		return nil
	}
	emitted := state.arguments[key]
	if emitted == complete {
		return nil
	}
	remaining := strings.TrimPrefix(complete, emitted)
	if emitted != "" && remaining == complete {
		return nil
	}
	state.arguments[key] = emitted + remaining
	state.partial(index).Arguments.WriteString(remaining)
	if sink == nil || remaining == "" {
		return nil
	}
	partial := state.partial(index)
	return sink(domain.ModelEvent{Type: domain.ModelEventToolCallDelta, Delta: remaining, ToolCall: &domain.ToolCallDelta{Index: index, ID: partial.ID, Name: partial.Name}})
}

func consumeCodexCompleted(completed codexCompletedResponse, content *strings.Builder, state *codexStreamState, sink ports.ModelEventSink) error {
	var completedText strings.Builder
	for _, output := range completed.Output {
		switch strings.ToLower(strings.TrimSpace(output.Type)) {
		case "message":
			for _, part := range output.Content {
				if part.Type == "output_text" {
					completedText.WriteString(part.Text)
				}
			}
		case "function_call":
			event := map[string]any{"item_id": output.ID, "call_id": output.CallID, "output_index": len(state.partials)}
			item := map[string]any{"id": output.ID, "call_id": output.CallID, "name": output.Name, "type": output.Type, "arguments": output.Arguments}
			if err := consumeCodexFunctionItem(event, item, state, sink); err != nil {
				return err
			}
		}
	}
	complete := completedText.String()
	current := content.String()
	remaining := strings.TrimPrefix(complete, current)
	if current != "" && remaining == complete {
		return nil
	}
	content.WriteString(remaining)
	return emitModelDelta(sink, domain.ModelEventTextDelta, remaining)
}

func (usage codexUsage) domainUsage() domain.Usage {
	return domain.Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens}
}

func emitModelDelta(sink ports.ModelEventSink, eventType, delta string) error {
	if sink == nil || delta == "" {
		return nil
	}
	return sink(domain.ModelEvent{Type: eventType, Delta: delta})
}

func emitUsage(sink ports.ModelEventSink, usage domain.Usage) error {
	if sink == nil || (usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0) {
		return nil
	}
	return sink(domain.ModelEvent{Type: domain.ModelEventUsage, Usage: &usage})
}

func codexStreamError(eventType string, event map[string]any, requestID, clientRequestID string) error {
	message := strings.TrimSpace(stringValue(event["message"]))
	code := strings.TrimSpace(stringValue(event["code"]))
	if nested, ok := event["error"].(map[string]any); ok {
		message = firstText(message, stringValue(nested["message"]))
		code = firstText(code, stringValue(nested["code"]), stringValue(nested["type"]))
	}
	if message == "" {
		encoded, _ := json.Marshal(event)
		message = string(encoded)
	}
	return &ProviderError{Operation: "Codex stream", Code: firstText(code, eventType), Message: message, RequestID: requestID, ClientRequestID: clientRequestID, Retryable: eventType == "response.incomplete"}
}

func codexCallKey(event, item map[string]any) string {
	if item != nil {
		if value := firstText(stringValue(item["id"]), stringValue(item["call_id"])); value != "" {
			return value
		}
	}
	return firstText(stringValue(event["item_id"]), stringValue(event["call_id"]), fmt.Sprintf("output_%d", intValue(event["output_index"])))
}

func (state *codexStreamState) callIndex(event map[string]any, key string) int {
	if index, exists := state.indexes[key]; exists {
		return index
	}
	index := intValue(event["output_index"])
	state.indexes[key] = index
	return index
}

func (state *codexStreamState) partial(index int) *partialCall {
	partial := state.partials[index]
	if partial == nil {
		partial = &partialCall{}
		state.partials[index] = partial
	}
	return partial
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}
