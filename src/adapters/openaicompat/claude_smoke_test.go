package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func claudeSmokeStream(events ...string) string {
	return "data: " + strings.Join(events, "\r\n\r\ndata: ") + "\r\n\r\n"
}
func TestClaudeMessagesSmoke(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[streaming], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "synthetic" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
					t.Error("端點或驗證標頭不正確")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["max_tokens"]) != "4096" || !strings.Contains(string(body["tool_choice"]), `"any"`) {
					t.Error("工具選擇或輸出預算遺失")
				}
				if strings.Contains(string(body["system"]), "context-data") {
					t.Error("context 資料被提升為 system")
				}
				w.Header().Set("request-id", "synthetic-request")
				if !streaming {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"type":"message","model":"synthetic-model","content":[{"type":"tool_use","id":"t1","name":"lookup","input":{"id":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":3}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, claudeSmokeStream(`{"type":"message_start","message":{"model":"synthetic-model","usage":{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":3}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"lookup","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"id\":9007199254740993}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`, `{"type":"message_stop"}`))
			}))
			defer server.Close()
			model, err := New(Config{UseClaude: true, BaseURL: server.URL + "/v1", APIKey: "synthetic", Model: "synthetic-model", DisableStreaming: !streaming, MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.Stream(context.Background(), domain.ModelRequest{SystemPrompt: "system", ContextPrompt: "context-data", UserPrompt: "query", ToolChoice: "required", Tools: []domain.ToolDefinition{{Name: "lookup"}}}, func(domain.ModelEvent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(result.ToolCalls) != 1 || result.ToolCalls[0].Arguments["id"] != json.Number("9007199254740993") || result.Usage.TotalTokens != 20 || result.StopReason != "tool_calls" || result.ProviderRequestID != "synthetic-request" {
				t.Fatalf("結果不完整：%+v", result)
			}
		})
	}
}

func TestClaudeToolHistorySmoke(t *testing.T) {
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.claudeRequest(domain.ModelRequest{History: []domain.Message{{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "a", Name: "one"}, {ID: "b", Name: "two"}}}, {Role: "tool", ToolCallID: "a", Content: "result"}, {Role: "tool", ToolCallID: "b"}}}, "synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []claudeMessage `json:"messages"`
	}
	if err = json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 2 || len(body.Messages[1].Content) != 2 || body.Messages[1].Content[1].ToolUseID != "b" || body.Messages[1].Content[1].Content == "" {
		t.Fatal("並行工具結果未完整回傳")
	}
}

func TestClaudeTerminalAndNoReplaySmoke(t *testing.T) {
	start := `{"type":"message_start","message":{"model":"synthetic"}}`
	text := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"中文"}}`
	overload := `{"type":"error","error":{"type":"overloaded_error","message":"synthetic overloaded"}}`
	for _, body := range []string{claudeSmokeStream(start, text), claudeSmokeStream(start, text, overload)} {
		count := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count++
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, body)
		}))
		m, err := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", MaxAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		result, err := m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "test"}, func(domain.ModelEvent) error { return nil })
		server.Close()
		if err == nil || count != 1 || len(result.ToolCalls) != 0 {
			t.Fatal("可見輸出後重送或不完整終態被接受", err, count)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(529) }))
	defer server.Close()
	m, _ := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic"})
	_, err := m.Stream(ctx, domain.ModelRequest{}, func(e domain.ModelEvent) error {
		if e.Type == domain.ModelEventProgress {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal("退避未取消", err, count)
	}
	// 每 byte 分段仍能正確組合 UTF-8 與 CRLF。
	result, err := decodeClaude(fragmentedReader{strings.NewReader(claudeSmokeStream(start, text, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`))}, true, "synthetic", "", nil)
	if err != nil || result.Content != "中文" {
		t.Fatal("分段串流錯誤", err)
	}
}

func TestClaudeModelsAndRedirectSmoke(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Header.Get("x-api-key") != "synthetic" || r.URL.Path != "/v1/models" {
			t.Error("模型列表請求錯誤")
		}
		if r.URL.Query().Get("after_id") == "" {
			io.WriteString(w, `{"data":[{"id":"a"}],"last_id":"a","has_more":true}`)
		} else {
			io.WriteString(w, `{"data":[{"id":"b"}],"has_more":false}`)
		}
	}))
	defer server.Close()
	m, _ := New(Config{UseClaude: true, BaseURL: server.URL + "/v1/messages", APIKey: "synthetic", Model: "synthetic"})
	models, err := m.ListModels(context.Background())
	if err != nil || len(models) != 2 || count != 2 {
		t.Fatal("模型列表分頁錯誤", err)
	}
	if err := m.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("Claude key 不可隨重導向傳送")
	}
	if _, err := New(Config{UseClaude: true, UseResponses: true, Model: "synthetic"}); err == nil {
		t.Fatal("互斥協定應拒絕")
	}
}

func TestClaudeRetryExhaustionSmoke(t *testing.T) {
	for _, status := range []int{529, http.StatusUnauthorized} {
		count := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count++
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(status)
			io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"synthetic"}}`)
		}))
		m, err := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", MaxAttempts: 3})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, err = m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "test"}, nil)
		server.Close()
		want := 1
		if status == 529 {
			want = 3
		}
		if err == nil || count != want {
			t.Fatalf("HTTP %d 重試次數：got=%d want=%d err=%v", status, count, want, err)
		}
	}
}
