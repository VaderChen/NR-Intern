package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeThinkingContinuationSmoke(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		body := `{"type":"message","content":[{"type":"thinking","thinking":"摘要","signature":"synthetic-signature"},{"type":"redacted_thinking","data":"synthetic-data"},{"type":"tool_use","id":"call","name":"lookup","input":{"token":"ping"}}],"stop_reason":"tool_use"}`
		if streaming {
			body = claudeSmokeStream(`{"type":"message_start","message":{}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"摘要"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"synthetic-"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signature"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"synthetic-data"}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{"token":"ping"}}}`,
				`{"type":"content_block_stop","index":2}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if streaming {
				w.Header().Set("Content-Type", "text/event-stream")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			io.WriteString(w, body)
		}))
		m, err := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", MaxAttempts: 1, DisableStreaming: !streaming})
		if err != nil {
			t.Fatal(err)
		}
		result, err := m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "probe"}, nil)
		server.Close()
		if err != nil || result.Reasoning != "摘要" || len(result.ToolCalls) != 1 || result.Continuation == nil || len(result.Continuation.Items) != 2 {
			t.Fatalf("思考／工具解析失敗：%v %+v", err, result)
		}
		history := []domain.Message{{Role: "user", Content: "probe"}, {Role: "assistant", ToolCalls: result.ToolCalls, Continuation: result.Continuation}, {Role: "tool", ToolCallID: "call", Content: "done"}}
		data, err := m.claudeRequest(domain.ModelRequest{History: history}, "synthetic", true)
		if err != nil {
			t.Fatal(err)
		}
		var request struct {
			Messages []claudeMessage `json:"messages"`
		}
		if err = json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
		blocks := request.Messages[1].Content
		if len(blocks) != 3 || blocks[0].Signature != "synthetic-signature" || blocks[1].Data != "synthetic-data" || blocks[2].Type != "tool_use" {
			t.Fatal("工具回合未保留原始思考簽章")
		}
		result.Continuation.Scope = "another-provider"
		data, err = m.claudeRequest(domain.ModelRequest{History: history}, "synthetic", true)
		if err != nil || strings.Contains(string(data), "synthetic-signature") {
			t.Fatal("跨 Provider 回送不透明思考狀態")
		}
	}
}

func TestClaudeThinkingFailureNoReplaySmoke(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, claudeSmokeStream(`{"type":"message_start","message":{}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, `{"type":"error","error":{"type":"overloaded_error","message":"synthetic"}}`))
	}))
	defer server.Close()
	m, _ := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", MaxAttempts: 3})
	result, err := m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "probe"}, func(domain.ModelEvent) error { return nil })
	if err == nil || count != 1 || result.Continuation != nil || len(result.ToolCalls) != 0 {
		t.Fatal("思考輸出後重送或失敗仍回傳狀態")
	}
}
