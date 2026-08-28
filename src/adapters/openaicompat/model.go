package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (m *Model) Stream(ctx context.Context, request domain.ModelRequest, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	if m == nil || m.client == nil {
		return domain.ModelResponse{}, fmt.Errorf("OpenAI-compatible model is unavailable")
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		modelName = m.defaultModel
	}
	streaming := !m.disableStreaming
	payload := chatRequest{
		Model:           modelName,
		Messages:        m.messages(request),
		Tools:           functionTools(request.Tools),
		Stream:          streaming,
		ReasoningEffort: strings.TrimSpace(request.ThinkingMode),
	}
	toolChoice := strings.ToLower(strings.TrimSpace(request.ToolChoice))
	if toolChoice != "" && toolChoice != "auto" && toolChoice != "required" && toolChoice != "none" {
		return domain.ModelResponse{}, fmt.Errorf("invalid tool choice %q: expected auto, required, or none", request.ToolChoice)
	}
	if len(payload.Tools) == 0 && toolChoice != "" && toolChoice != "none" {
		return domain.ModelResponse{}, fmt.Errorf("tool choice %q requires at least one tool definition", toolChoice)
	}
	if len(payload.Tools) > 0 && !m.omitToolChoice {
		if toolChoice == "" {
			toolChoice = "auto"
		}
		payload.ToolChoice = toolChoice
	}
	if streaming && m.streamIncludeUsage {
		payload.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("encode chat completion request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= m.maxAttempts; attempt++ {
		emittedModelOutput := false
		attemptSink := sink
		if sink != nil {
			attemptSink = func(event domain.ModelEvent) error {
				if err := sink(event); err != nil {
					return err
				}
				if observableModelOutput(event.Type) {
					emittedModelOutput = true
				}
				return nil
			}
		}
		result, retryAfter, attemptErr := m.executeAttempt(ctx, body, modelName, streaming, attemptSink)
		if attemptErr == nil {
			return result, nil
		}
		lastErr = attemptErr
		if ctx.Err() != nil {
			return domain.ModelResponse{}, ctx.Err()
		}
		providerErr := &ProviderError{}
		if !errors.As(attemptErr, &providerErr) || !providerErr.Retryable || emittedModelOutput || attempt == m.maxAttempts {
			return domain.ModelResponse{}, attemptErr
		}
		if sink != nil {
			if err := sink(domain.ModelEvent{Type: domain.ModelEventProgress, Delta: fmt.Sprintf("LLM 連線暫時失敗，準備第 %d/%d 次嘗試", attempt+1, m.maxAttempts)}); err != nil {
				return domain.ModelResponse{}, err
			}
		}
		if retryAfter <= 0 {
			retryAfter = time.Duration(400*(1<<(attempt-1))) * time.Millisecond
		}
		if retryAfter > 30*time.Second {
			retryAfter = 30 * time.Second
		}
		m.logger.Warn("retrying provider request",
			"attempt", attempt,
			"max_attempts", m.maxAttempts,
			"retry_after_ms", retryAfter.Milliseconds(),
			"status", providerErr.StatusCode,
			"code", providerErr.Code,
			"provider_request_id", providerErr.RequestID,
		)
		timer := time.NewTimer(retryAfter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.ModelResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	return domain.ModelResponse{}, lastErr
}

func observableModelOutput(eventType string) bool {
	switch eventType {
	case domain.ModelEventTextDelta, domain.ModelEventThinkingDelta, domain.ModelEventToolCallDelta:
		return true
	default:
		return false
	}
}

func (m *Model) executeAttempt(ctx context.Context, body []byte, modelName string, streaming bool, sink ports.ModelEventSink) (domain.ModelResponse, time.Duration, error) {
	clientRequestID := domain.NewID("llmreq")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.ModelResponse{}, 0, err
	}
	for name, value := range m.extraHeaders {
		httpRequest.Header.Set(name, value)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream, application/json")
	httpRequest.Header.Set("User-Agent", "AgenticService/openai-compatible")
	httpRequest.Header.Set("X-Client-Request-Id", clientRequestID)
	if m.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return domain.ModelResponse{}, 0, &ProviderError{
			Operation:       "connection",
			Message:         err.Error(),
			ClientRequestID: clientRequestID,
			Retryable:       ctx.Err() == nil,
			Cause:           err,
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr, retryAfter := providerHTTPError(response, clientRequestID)
		return domain.ModelResponse{}, retryAfter, providerErr
	}
	requestID := strings.TrimSpace(response.Header.Get("X-Request-ID"))
	reader := bufio.NewReader(response.Body)
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if responseIsJSON(reader, contentType, streaming) {
		result, err := decodeJSONResponse(reader, modelName, requestID, clientRequestID, sink)
		return result, 0, err
	}
	result, err := decodeStream(reader, modelName, requestID, clientRequestID, sink)
	return result, 0, err
}

func responseIsJSON(reader *bufio.Reader, contentType string, streaming bool) bool {
	if !streaming {
		return true
	}
	data, err := reader.Peek(64)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		return false
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		text := string(trimmed)
		if strings.Contains(text, `"delta"`) || strings.Contains(text, `chat.completion.chunk`) {
			return false
		}
		return true
	}
	return strings.Contains(contentType, "application/json")
}
