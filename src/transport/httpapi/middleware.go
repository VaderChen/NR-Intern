package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (h *Handler) middleware(next http.Handler) http.Handler {
	return h.recoverer(h.securityHeaders(h.requestID(h.cors(h.authenticate(next)))))
}

func (h *Handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(writer, request)
	})
}

func (h *Handler) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				writeProblem(writer, request, fmt.Errorf("unexpected panic: %v", recovered))
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func (h *Handler) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if !validRequestID(requestID) {
			requestID = fmt.Sprintf("req_%d", time.Now().UnixNano())
			request.Header.Set("X-Request-ID", requestID)
		}
		writer.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(writer, request)
	})
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if h.apiToken == "" || request.Method == http.MethodOptions || request.URL.Path == "/healthz" || request.URL.Path == "/readyz" {
			next.ServeHTTP(writer, request)
			return
		}
		scheme, provided, found := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
		provided = strings.TrimSpace(provided)
		if !found || !strings.EqualFold(scheme, "Bearer") {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(writer, request, fmt.Errorf("%w: bearer token is required", errUnauthorized))
			return
		}
		if len(provided) != len(h.apiToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.apiToken)) != 1 {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(writer, request, fmt.Errorf("%w: invalid bearer token", errUnauthorized))
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (h *Handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin != "" && h.originAllowed(origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID, X-Request-ID")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			writer.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-Run-ID, Location")
			writer.Header().Set("Vary", "Origin")
		}
		if request.Method == http.MethodOptions {
			if origin != "" && !h.originAllowed(origin) {
				writeProblem(writer, request, fmt.Errorf("%w: origin is not allowed", errUnauthorized))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (h *Handler) originAllowed(origin string) bool {
	if len(h.allowedOrigins) == 0 {
		return isLoopbackOrigin(origin)
	}
	for _, allowed := range h.allowedOrigins {
		if allowed == "*" || strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func isLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}
