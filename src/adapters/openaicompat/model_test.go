package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestStreamSendsRequiredNativeToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.ToolChoice != "required" {
			t.Errorf("tool_choice = %q, want required", payload.ToolChoice)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].Function.Name != "provider_connection_test" {
			t.Errorf("tools = %+v, want provider_connection_test", payload.Tools)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"model":"test-model","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_probe","type":"function","function":{"name":"provider_connection_test","arguments":"{\"token\":\"ping\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()
	model, err := New(Config{BaseURL: server.URL + "/v1", Model: "test-model", DisableStreaming: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	response, err := model.Stream(context.Background(), domain.ModelRequest{
		ToolChoice: "required",
		Tools: []domain.ToolDefinition{{
			Name:        "provider_connection_test",
			Description: "tool-call probe",
			InputSchema: map[string]any{"type": "object"},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "provider_connection_test" {
		t.Fatalf("tool calls = %+v, want provider_connection_test", response.ToolCalls)
	}
}

func TestMessagesKeepSystemHostToolsPhaseContextHistoryAndUserSeparate(t *testing.T) {
	model := &Model{instructionRole: "system"}
	messages := model.messages(domain.ModelRequest{
		SystemPrompt:  "SYSTEM",
		HostPrompt:    "HOST",
		ToolPrompt:    "TOOLS",
		PhasePrompt:   "FINALIZATION",
		ContextPrompt: "CONTEXT",
		History: []domain.Message{
			{Role: "user", Content: "OLD USER"},
			{Role: "assistant", Content: "OLD ASSISTANT"},
		},
		UserPrompt: "CURRENT USER",
	})
	if len(messages) != 8 {
		t.Fatalf("messages = %+v", messages)
	}
	wantRoles := []string{"system", "system", "system", "system", "user", "user", "assistant", "user"}
	wantContent := []string{"SYSTEM", "HOST", "TOOLS", "FINALIZATION", "CONTEXT", "OLD USER", "OLD ASSISTANT", "CURRENT USER"}
	for index := range messages {
		if messages[index].Role != wantRoles[index] || messages[index].Content != wantContent[index] {
			t.Fatalf("message[%d] = %+v, want role=%s content=%s", index, messages[index], wantRoles[index], wantContent[index])
		}
	}
}

// TestStreamDoesNotRetryAfterObservableStructuredDelta 防止 thinking 或 tool-call
// 已送到前端後又重試，否則 durable event log 會出現無法去重的重複片段。
func TestStreamDoesNotRetryAfterObservableStructuredDelta(t *testing.T) {
	tests := []struct {
		name  string
		chunk string
		type_ string
	}{
		{
			name:  "thinking",
			chunk: `{"choices":[{"delta":{"reasoning_content":"思考中"}}]}`,
			type_: domain.ModelEventThinkingDelta,
		},
		{
			name:  "tool call",
			chunk: `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"file_read","arguments":"{\\\"path\\\":"}}]}}]}`,
			type_: domain.ModelEventToolCallDelta,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(writer, "data: %s\n\n", test.chunk)
				// 刻意不送 finish_reason / [DONE]，模擬串流在可見輸出後斷線。
			}))
			defer server.Close()
			model, err := New(Config{BaseURL: server.URL + "/v1", Model: "test-model", MaxAttempts: 2})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			sawOutput := false
			_, err = model.Stream(context.Background(), domain.ModelRequest{}, func(event domain.ModelEvent) error {
				if event.Type == test.type_ {
					sawOutput = true
				}
				return nil
			})
			if err == nil {
				t.Fatal("incomplete stream should fail")
			}
			if !sawOutput {
				t.Fatalf("did not receive %s before disconnect", test.type_)
			}
			if got := attempts.Load(); got != 1 {
				t.Fatalf("provider attempts = %d, want 1 after observable output", got)
			}
		})
	}
}
