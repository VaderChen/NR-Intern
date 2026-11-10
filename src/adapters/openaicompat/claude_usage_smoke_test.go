package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeUsageHeadersSmoke(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("anthropic-ratelimit-requests-limit", "100")
		w.Header().Set("anthropic-ratelimit-requests-remaining", "25")
		w.Header().Set("anthropic-ratelimit-requests-reset", "2030-01-01T01:00:00Z")
		w.WriteHeader(status)
		if status == 200 {
			io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
		} else {
			io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"synthetic"}}`)
		}
	}))
	defer server.Close()
	m, err := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", DisableStreaming: true, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	value := m.ProviderUsage()
	if value.Kind != "rate_limit" || value.Model != "synthetic" || len(value.RateLimits) != 1 || value.RateLimits[0].Remaining != 25 || value.RateLimits[0].Limit != 100 || value.UpdatedAt == "" {
		t.Fatalf("標頭快照不正確：%+v", value)
	}
	value.RateLimits[0].Remaining = 99
	if m.ProviderUsage().RateLimits[0].Remaining != 25 {
		t.Fatal("快照未隔離")
	}
	status = 401
	_, _ = m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "test"}, nil)
	if len(m.ProviderUsage().RateLimits) != 0 {
		t.Fatal("授權失效仍保留舊快照")
	}
}

func TestClaudeUsageUnknownLimitSmoke(t *testing.T) {
	m, _ := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	for _, limit := range []string{"", "invalid", "0", "-1"} {
		h := http.Header{}
		h.Set("anthropic-ratelimit-requests-remaining", "0")
		h.Set("anthropic-ratelimit-requests-limit", limit)
		m.captureClaudeRateLimits(h, "synthetic", 429)
		value := m.ProviderUsage()
		if len(value.RateLimits) != 1 || value.RateLimits[0].Remaining != 0 || value.RateLimits[0].Limit != 0 {
			t.Fatal("未知上限不應遺失明確剩餘數量")
		}
	}
	for _, remaining := range []string{"", "NaN", "-1", "101"} {
		h := http.Header{}
		h.Set("anthropic-ratelimit-requests-limit", "100")
		h.Set("anthropic-ratelimit-requests-remaining", remaining)
		m.captureClaudeRateLimits(h, "other", 200)
		if len(m.ProviderUsage().RateLimits) != 0 || m.ProviderUsage().UpdatedAt != "" {
			t.Fatal("非法值不應冒充快照或保留舊模型資料")
		}
	}
}
