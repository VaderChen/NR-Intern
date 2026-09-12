package openaicompat

import (
	"encoding/json"
	"errors"
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

// JSON 在事件中途截斷可以重試；完整但格式錯誤的事件不因重送而恢復。
func providerStreamDecodeError(operation string, cause error, requestID, clientRequestID string) error {
	var syntaxError *json.SyntaxError
	truncated := errors.Is(cause, io.ErrUnexpectedEOF) ||
		(errors.As(cause, &syntaxError) && syntaxError.Error() == "unexpected end of JSON input")
	return &ProviderError{Operation: operation, Message: cause.Error(), RequestID: requestID,
		ClientRequestID: clientRequestID, Retryable: truncated, Cause: cause}
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

// 僅對錯誤封包使用文字判斷，不能把正常回答中的「過載」當成失敗。
func retryableStreamError(code, message string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "server_error", "internal_server_error", "overloaded_error", "rate_limit_exceeded", "rate_limit_error", "service_unavailable":
		return true
	case "invalid_api_key", "authentication_error", "permission_denied", "insufficient_quota", "invalid_request_error":
		return false
	}
	text := strings.ToLower(message)
	// 上游自己說可以重試就照做——那是它對這次失敗性質的宣告，比我們從訊息
	// 猜測可靠。OpenAI 的通用暫時性錯誤就是這個措辭，而金鑰錯誤、額度用盡
	// 這類永久性失敗不會這樣寫。
	for _, pattern := range []string{
		"servers are currently overloaded",
		"temporarily unavailable",
		"you can retry your request",
		"an error occurred while processing your request",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

// 舊代理把失敗偽裝為成功訊息；以代理模型與保留 ID 辨識封包，
// 不掃描一般模型文字，以免引用錯誤訊息的正常回答被誤判。
func proxyTerminalError(model, id, message, requestID, clientRequestID string) error {
	if model != "load-balance-provider" || (!strings.HasPrefix(id, "chatcmpl-refusal-") && !strings.HasPrefix(id, "resp_refusal_")) {
		return nil
	}
	return &ProviderError{Operation: "upstream rejection", Message: message, RequestID: requestID, ClientRequestID: clientRequestID, Retryable: retryableStreamError("", message)}
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

// 上游重啟或暫時離線需要恢復時間，毫秒級重試會在服務恢復前耗盡次數。
// 兩種模型協定共用退避規則，等待仍由呼叫端的 context 控制，不延長 Run 預算。
func providerRetryDelay(attempt int, retryAfter time.Duration) time.Duration {
	const maximum = 30 * time.Second
	if retryAfter > 0 {
		return min(retryAfter, maximum)
	}
	delay := 10 * time.Second
	for i := 1; i < attempt && delay < maximum; i++ {
		delay *= 2
	}
	return min(delay, maximum)
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
