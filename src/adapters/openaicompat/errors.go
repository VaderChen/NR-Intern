package openaicompat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProviderError struct {
	Operation       string
	StatusCode      int
	Code            string
	Message         string
	RequestID       string
	ClientRequestID string
	Retryable       bool
	Cause           error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "OpenAI-compatible provider error"
	}
	parts := []string{"OpenAI-compatible provider " + firstText(e.Operation, "request") + " failed"}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if e.RequestID != "" {
		parts = append(parts, "request_id="+e.RequestID)
	} else if e.ClientRequestID != "" {
		parts = append(parts, "client_request_id="+e.ClientRequestID)
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Cause != nil {
		message = e.Cause.Error()
	}
	if message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, ": ")
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type apiErrorEnvelope struct {
	Error struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
		Param   string          `json:"param"`
	} `json:"error"`
}

func providerHTTPError(response *http.Response, clientRequestID string) (*ProviderError, time.Duration) {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	envelope := apiErrorEnvelope{}
	_ = json.Unmarshal(data, &envelope)
	code := strings.Trim(string(envelope.Error.Code), `"`)
	if code == "null" {
		code = ""
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = strings.TrimSpace(string(data))
		if len(message) > 4_000 {
			message = message[:4_000] + "…"
		}
	}
	if envelope.Error.Param != "" {
		message = strings.TrimSpace(message + " (param: " + envelope.Error.Param + ")")
	}
	return &ProviderError{
		Operation:       "HTTP request",
		StatusCode:      response.StatusCode,
		Code:            firstText(code, envelope.Error.Type),
		Message:         message,
		RequestID:       strings.TrimSpace(response.Header.Get("X-Request-ID")),
		ClientRequestID: clientRequestID,
		Retryable:       retryableStatus(response.StatusCode),
	}, parseRetryAfter(response.Header.Get("Retry-After"))
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if duration := time.Until(when); duration > 0 {
			return duration
		}
	}
	return 0
}

func firstText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// firstRawText is used for streamed text fragments. Trimming an individual
// fragment destroys spaces between reasoning deltas and produces merged words.
func firstRawText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
