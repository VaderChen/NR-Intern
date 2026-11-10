package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 快取讀寫量必須各自保留，否則無法從事件判斷 prompt caching 是否命中；
// 兩者已包含在 InputTokens 內，總量不可重複計算。
func TestClaudeCacheUsageSmoke(t *testing.T) {
	for name, streaming := range map[string]bool{"串流": true, "非串流": false} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !streaming {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"type":"message","model":"synthetic-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":300,"cache_creation_input_tokens":40}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, claudeSmokeStream(
					`{"type":"message_start","message":{"model":"synthetic-model","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":300,"cache_creation_input_tokens":40}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
					`{"type":"message_stop"}`,
				))
			}))
			defer server.Close()
			model, err := New(Config{UseClaude: true, BaseURL: server.URL + "/v1", APIKey: "synthetic", Model: "synthetic-model", DisableStreaming: !streaming, MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			var last domain.Usage
			result, err := model.Stream(context.Background(), domain.ModelRequest{UserPrompt: "query"}, func(event domain.ModelEvent) error {
				if event.Type == domain.ModelEventUsage && event.Usage != nil {
					last = *event.Usage
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := domain.Usage{InputTokens: 350, OutputTokens: 5, TotalTokens: 355, CacheReadTokens: 300, CacheWriteTokens: 40}
			if result.Usage != want {
				t.Fatalf("用量不符：%+v", result.Usage)
			}
			if last != want {
				t.Fatalf("最後一次用量事件不符：%+v", last)
			}
		})
	}
}

func TestUsageAddKeepsCacheTokensOutOfTotal(t *testing.T) {
	var total domain.Usage
	total.Add(domain.Usage{InputTokens: 350, OutputTokens: 5, CacheReadTokens: 300, CacheWriteTokens: 40})
	total.Add(domain.Usage{InputTokens: 100, OutputTokens: 1, TotalTokens: 101, CacheReadTokens: 90})
	want := domain.Usage{InputTokens: 450, OutputTokens: 6, TotalTokens: 456, CacheReadTokens: 390, CacheWriteTokens: 40}
	if total != want {
		t.Fatalf("累加不符：%+v", total)
	}
}
