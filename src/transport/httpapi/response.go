package httpapi

import (
	"AgenticService/src/domain"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

type dataEnvelope struct {
	Data any `json:"data"`
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

func writeData(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(dataEnvelope{Data: value})
}

func writeProblem(writer http.ResponseWriter, request *http.Request, err error) {
	status, code, title := classifyError(err)
	detail := err.Error()
	if status >= http.StatusInternalServerError {
		slog.Error("HTTP request failed",
			"request_id", request.Header.Get("X-Request-ID"),
			"method", request.Method,
			"path", request.URL.Path,
			"error", err,
		)
		detail = "internal server error; use request_id to inspect backend logs"
	}
	writer.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: request.Header.Get("X-Request-ID"),
	})
}

func classifyError(err error) (int, string, string) {
	switch {
	case errors.Is(err, errUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Unauthorized"
	case errors.Is(err, errUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "Service unavailable"
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_input", "Invalid input"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found", "Resource not found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict", "Resource conflict"
	case errors.Is(err, domain.ErrCanceled):
		return 499, "canceled", "Request canceled"
	default:
		return http.StatusInternalServerError, "internal_error", "Internal server error"
	}
}
