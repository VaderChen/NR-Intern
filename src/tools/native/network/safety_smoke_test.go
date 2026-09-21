package network

import (
	"AgenticService/src/domain"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchRevokesWarmPrivateConnectionSmoke(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "ok") }))
	defer server.Close()
	tool := New(Options{}, localSettings())
	if result := execute(t, tool, map[string]any{"url": server.URL}); result.IsError {
		t.Fatal(result.Content)
	}
	tool.ApplySettings(domain.HTTPFetchSettings{Enabled: true, AllowPrivateNetworks: false})
	if result := execute(t, tool, map[string]any{"url": server.URL}); !result.IsError {
		t.Fatal("revoked connection reused")
	}
	if calls.Load() != 1 {
		t.Fatal("request reached revoked endpoint")
	}
}

func TestFetchLostPostResponseIsUnknownSmoke(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	result := execute(t, New(Options{}, localSettings()), map[string]any{"url": server.URL, "method": "POST", "body": "{}"})
	if !result.IsError || domain.ToolExecutionState(result.Details) != domain.ToolOutcomeUnknown || calls.Load() != 1 {
		t.Fatalf("lost response: %+v calls=%d", result, calls.Load())
	}
}
