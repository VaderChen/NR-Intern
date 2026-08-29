package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func decodeStream(reader io.Reader, fallbackModel, requestID, clientRequestID string, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	result := domain.ModelResponse{ProviderID: "openai-compatible", ProviderRequestID: requestID, Model: fallbackModel}
	var content strings.Builder
	var reasoning strings.Builder
	var taggedStream taggedThinkingStream
	partials := map[int]*partialCall{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	dataLines := []string{}
	sawChunk := false
	sawTerminal := false

	consume := func(data string) error {
		data = strings.TrimSpace(data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			sawTerminal = true
			return nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode chat completion stream event: %w", err)
		}
		sawChunk = true
		if chunk.Error != nil {
			return &ProviderError{
				Operation:       "stream",
				Code:            firstText(chunk.Error.Code, chunk.Error.Type),
				Message:         chunk.Error.Message,
				RequestID:       requestID,
				ClientRequestID: clientRequestID,
				Retryable:       chunk.Error.Type == "server_error",
			}
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		result.Usage = chunk.Usage.domainUsage(result.Usage)
		for _, choice := range chunk.Choices {
			delta := contentText(choice.Delta.Content)
			if delta == "" {
				delta = choice.Delta.Refusal
			}
			if delta != "" {
				content.WriteString(delta)
				if sink != nil {
					for _, fragment := range taggedStream.Push(delta, false) {
						eventType := domain.ModelEventTextDelta
						if fragment.Thinking {
							eventType = domain.ModelEventThinkingDelta
						}
						if err := sink(domain.ModelEvent{Type: eventType, Delta: fragment.Text}); err != nil {
							return err
						}
					}
				} else {
					taggedStream.Push(delta, false)
				}
			}
			if thinking := firstRawText(choice.Delta.ReasoningContent, choice.Delta.Reasoning); thinking != "" {
				reasoning.WriteString(thinking)
				if sink != nil {
					if err := sink(domain.ModelEvent{Type: domain.ModelEventThinkingDelta, Delta: thinking}); err != nil {
						return err
					}
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				partial := partials[call.Index]
				if partial == nil {
					partial = &partialCall{}
					partials[call.Index] = partial
				}
				if call.ID != "" {
					partial.ID = call.ID
				}
				partial.Name = mergeName(partial.Name, call.Function.Name)
				partial.Arguments.WriteString(call.Function.Arguments)
				if sink != nil {
					if err := sink(domain.ModelEvent{
						Type:     domain.ModelEventToolCallDelta,
						Delta:    call.Function.Arguments,
						ToolCall: &domain.ToolCallDelta{Index: call.Index, ID: partial.ID, Name: partial.Name},
					}); err != nil {
						return err
					}
				}
			}
			if choice.FinishReason != nil {
				result.StopReason = *choice.FinishReason
				sawTerminal = true
			}
		}
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := consume(strings.Join(dataLines, "\n")); err != nil {
				return domain.ModelResponse{}, err
			}
			dataLines = dataLines[:0]
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		// 部分相容服務回傳 newline-delimited JSON，而不是完整 SSE 欄位。
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			if err := consume(strings.TrimSpace(line)); err != nil {
				return domain.ModelResponse{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.ModelResponse{}, &ProviderError{Operation: "stream read", Message: err.Error(), RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true, Cause: err}
	}
	if len(dataLines) > 0 {
		if err := consume(strings.Join(dataLines, "\n")); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	if !sawChunk {
		return domain.ModelResponse{}, &ProviderError{Operation: "stream read", Message: "stream ended without a completion chunk", RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true}
	}
	if !sawTerminal {
		return domain.ModelResponse{}, &ProviderError{Operation: "stream read", Message: "stream ended before [DONE] or finish_reason", RequestID: requestID, ClientRequestID: clientRequestID, Retryable: true}
	}
	if sink != nil {
		for _, fragment := range taggedStream.Push("", true) {
			eventType := domain.ModelEventTextDelta
			if fragment.Thinking {
				eventType = domain.ModelEventThinkingDelta
			}
			if err := sink(domain.ModelEvent{Type: eventType, Delta: fragment.Text}); err != nil {
				return domain.ModelResponse{}, err
			}
		}
	} else {
		taggedStream.Push("", true)
	}
	if sink != nil && (result.Usage.TotalTokens > 0 || result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
		usage := result.Usage
		if err := sink(domain.ModelEvent{Type: domain.ModelEventUsage, Usage: &usage}); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	taggedContent, taggedReasoning, _ := splitTaggedThinking(content.String())
	result.Content = taggedContent
	result.Reasoning = mergeReasoning(reasoning.String(), taggedReasoning)
	calls, err := finalizeCalls(partials)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	result.ToolCalls = calls
	if len(calls) > 0 && result.StopReason == "" {
		result.StopReason = "tool_calls"
	}
	if result.StopReason == "" {
		result.StopReason = "stop"
	}
	return result, nil
}

func decodeJSONResponse(reader io.Reader, fallbackModel, requestID, clientRequestID string, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	var response jsonResponse
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		return domain.ModelResponse{}, &ProviderError{Operation: "JSON decode", Message: err.Error(), RequestID: requestID, ClientRequestID: clientRequestID, Retryable: errors.Is(err, io.ErrUnexpectedEOF), Cause: err}
	}
	if response.Error != nil {
		return domain.ModelResponse{}, &ProviderError{
			Operation:       "JSON response",
			Code:            firstText(response.Error.Code, response.Error.Type),
			Message:         response.Error.Message,
			RequestID:       requestID,
			ClientRequestID: clientRequestID,
			Retryable:       response.Error.Type == "server_error",
		}
	}
	if len(response.Choices) == 0 {
		return domain.ModelResponse{}, fmt.Errorf("chat completion response contains no choices")
	}
	choice := response.Choices[0]
	rawContent := contentText(choice.Message.Content)
	if rawContent == "" {
		rawContent = choice.Message.Refusal
	}
	content, taggedReasoning, _ := splitTaggedThinking(rawContent)
	reasoning := mergeReasoning(firstText(choice.Message.ReasoningContent, choice.Message.Reasoning), taggedReasoning)
	if sink != nil && reasoning != "" {
		if err := sink(domain.ModelEvent{Type: domain.ModelEventThinkingDelta, Delta: reasoning}); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	if sink != nil && content != "" {
		if err := sink(domain.ModelEvent{Type: domain.ModelEventTextDelta, Delta: content}); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	calls, err := decodeToolCalls(choice.Message.ToolCalls)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	if sink != nil {
		for index, call := range calls {
			encoded, _ := json.Marshal(call.Arguments)
			if err := sink(domain.ModelEvent{
				Type:     domain.ModelEventToolCallDelta,
				Delta:    string(encoded),
				ToolCall: &domain.ToolCallDelta{Index: index, ID: call.ID, Name: call.Name},
			}); err != nil {
				return domain.ModelResponse{}, err
			}
		}
	}
	modelName := response.Model
	if modelName == "" {
		modelName = fallbackModel
	}
	stopReason := choice.FinishReason
	if stopReason == "" && len(calls) > 0 {
		stopReason = "tool_calls"
	}
	if stopReason == "" {
		stopReason = "stop"
	}
	usage := response.Usage.domainUsage(domain.Usage{})
	if sink != nil && (usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0) {
		value := usage
		if err := sink(domain.ModelEvent{Type: domain.ModelEventUsage, Usage: &value}); err != nil {
			return domain.ModelResponse{}, err
		}
	}
	return domain.ModelResponse{
		ProviderID:        "openai-compatible",
		ProviderRequestID: requestID,
		Model:             modelName,
		Content:           strings.TrimSpace(content),
		Reasoning:         strings.TrimSpace(reasoning),
		ToolCalls:         calls,
		StopReason:        stopReason,
		Usage:             usage,
	}, nil
}
