package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/providerauth"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (m *Model) streamCodex(ctx context.Context, request domain.ModelRequest, sink ports.ModelEventSink) (domain.ModelResponse, error) {
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		modelName = m.defaultModel
	}
	payload, err := buildCodexRequest(request, modelName)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.ModelResponse{}, fmt.Errorf("encode Codex Responses request: %w", err)
	}
	inputShape := DescribeCodexInput(payload.Input)

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
		result, retryAfter, attemptErr := m.executeCodexAttempt(ctx, body, modelName, request.SessionID, inputShape, attemptSink)
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
		m.logger.Warn("retrying Codex provider request",
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

// inputShape 只在 4xx 時用到：上游回報的是索引（input[40].output），
// 光看索引無從得知那一項是什麼。在組裝處算好一次帶下來，比在錯誤路徑上
// 重新解析 body 便宜也可靠。
func (m *Model) executeCodexAttempt(ctx context.Context, body []byte, modelName, sessionID, inputShape string, sink ports.ModelEventSink) (domain.ModelResponse, time.Duration, error) {
	clientRequestID := domain.NewID("llmreq")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.ModelResponse{}, 0, err
	}
	for name, value := range m.extraHeaders {
		httpRequest.Header.Set(name, value)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("User-Agent", "NR-Intern/codex")
	httpRequest.Header.Set("X-Client-Request-Id", clientRequestID)
	httpRequest.Header.Set("OpenAI-Beta", "responses=experimental")
	httpRequest.Header.Set("Originator", providerauth.DefaultOriginator)
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		httpRequest.Header.Set("session_id", sessionID)
	}
	if err := m.applyAuthorization(ctx, httpRequest); err != nil {
		return domain.ModelResponse{}, 0, &ProviderError{
			Operation:       "authorization",
			Message:         err.Error(),
			ClientRequestID: clientRequestID,
			Retryable:       false,
			Cause:           err,
		}
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
	m.recordProviderUsage(response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr, retryAfter := providerHTTPError(response, clientRequestID)
		// 4xx 多半是請求本體的結構問題，而上游只回報索引（input[40].output）。
		// 把結構摘要記下來，下次就不必靠推論還原第 N 項是什麼。
		if response.StatusCode >= 400 && response.StatusCode < 500 && m.logger != nil && inputShape != "" {
			m.logger.Warn("codex request rejected",
				"status", response.StatusCode,
				"client_request_id", clientRequestID,
				"input_shape", inputShape)
		}
		return domain.ModelResponse{}, retryAfter, providerErr
	}
	requestID := strings.TrimSpace(response.Header.Get("X-Request-ID"))
	result, err := decodeCodexStream(bufio.NewReader(response.Body), modelName, requestID, clientRequestID, sink)
	return result, 0, err
}
