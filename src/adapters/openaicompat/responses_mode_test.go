package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponsesModeExactEndpointAndCatalog(t *testing.T) {
	posts, catalogs := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer smoke-placeholder" {
			t.Error("API Key authorization missing")
		}
		if r.Method == http.MethodGet {
			catalogs++
			if r.RequestURI != "/v1/models" {
				t.Errorf("catalog URI = %s", r.RequestURI)
			}
			fmt.Fprint(w, `{"data":[{"id":"MixedCase-Model"}]}`)
			return
		}
		posts++
		if r.Method != http.MethodPost || r.RequestURI != "/backend-api/codex/%72esponses/?route=smoke" {
			t.Errorf("inference URI = %s %s", r.Method, r.RequestURI)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["model"] != "MixedCase-Model" || payload["stream"] != true || payload["input"] == nil || payload["messages"] != nil {
			t.Error("unexpected Responses request schema")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"MixedCase-Model\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\n")
	}))
	defer server.Close()
	m, err := New(Config{BaseURL: server.URL + "/backend-api/codex/%72esponses/?route=smoke", UseResponses: true, APIKey: "smoke-placeholder", Model: "MixedCase-Model", DisableStreaming: true, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "smoke"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "OK" {
		t.Errorf("content = %q", result.Content)
	}
	models, err := m.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0] != "MixedCase-Model" {
		t.Fatalf("models = %v, error = %v", models, err)
	}
	if posts != 1 || catalogs != 1 {
		t.Fatalf("requests = %d/%d", posts, catalogs)
	}
}

func TestResponsesModeFailureDoesNotFallBack(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/custom" {
			t.Errorf("unexpected fallback path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n")
	}))
	defer server.Close()
	m, err := New(Config{BaseURL: server.URL + "/custom", UseResponses: true, Model: "test", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Stream(context.Background(), domain.ModelRequest{}, nil)
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Retryable || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestResponsesModeEndpointValidationAndDefault(t *testing.T) {
	for _, raw := range []string{"", "/responses", "file:///responses", "https://user:password@example.com/responses", "https://example.com/responses#fragment"} {
		if _, err := responsesEndpoint(raw); err == nil {
			t.Errorf("accepted invalid endpoint")
		}
	}
	m, err := New(Config{BaseURL: "https://example.com/v1", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if m.useResponses || m.endpoint != "https://example.com/v1/chat/completions" {
		t.Fatal("default Chat routing changed")
	}
}

func TestResponsesModeToolCallNotDuplicated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"smoke\",\"arguments\":\"\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"fc_1\",\"delta\":\"{}\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"smoke\",\"arguments\":\"{}\"}]}}\n\n")
	}))
	defer server.Close()
	m, err := New(Config{BaseURL: server.URL, UseResponses: true, Model: "test", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Stream(context.Background(), domain.ModelRequest{
		Tools: []domain.ToolDefinition{{Name: "smoke", InputSchema: map[string]any{"type": "object"}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "smoke" {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
}
