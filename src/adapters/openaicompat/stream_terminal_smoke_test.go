package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func terminalSmokeStream(events ...string) string {
	return "data: " + strings.Join(events, "\r\n\r\ndata: ") + "\r\n\r\n"
}

func TestProxySplitRefusalSmoke(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, identified := range []bool{false, true} {
			t.Run(fmt.Sprintf("responses=%v/identified=%v", responses, identified), func(t *testing.T) {
				id := "ordinary-response"
				if identified {
					id = "chatcmpl-refusal-synthetic"
					if responses {
						id = "resp_refusal_synthetic"
					}
				}
				prefix, suffix := "⚠️ 上游失敗：Our servers are currently ", "overloaded. Please try again later."
				var events []string
				decode := decodeStream
				if responses {
					decode = decodeCodexStream
					events = append(events, fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"load-balance-provider"}}`, id))
					for _, delta := range []string{prefix, suffix} {
						data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": delta})
						events = append(events, string(data))
					}
					// 實際代理的 completed 也不一定帶 model，必須沿用 created 的身分。
					events = append(events, fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"output":[]}}`, id))
				} else {
					events = append(events, fmt.Sprintf(`{"model":"load-balance-provider","id":%q,"choices":[{"delta":{"role":"assistant"}}]}`, id))
					for _, delta := range []string{prefix, suffix} {
						data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": delta}}}})
						events = append(events, string(data))
					}
					events = append(events, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]")
				}
				var output strings.Builder
				result, err := decode(fragmentedReader{strings.NewReader(terminalSmokeStream(events...))}, "fallback", "", "", func(event domain.ModelEvent) error {
					output.WriteString(event.Delta)
					return nil
				})
				if identified {
					var providerErr *ProviderError
					if !errors.As(err, &providerErr) || !providerErr.Retryable || providerErr.Message != prefix+suffix || output.Len() != 0 {
						t.Fatalf("split refusal: err=%v output=%q", err, output.String())
					}
				} else if err != nil || result.Content != prefix+suffix || output.String() != result.Content {
					t.Fatalf("normal quoted error misclassified: result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestResponsesRequiresCompletionSmoke(t *testing.T) {
	for _, terminal := range []string{"[DONE]", `{"type":"response.completed"}`, `{"type":"response.completed","response":null}`} {
		t.Run(terminal, func(t *testing.T) {
			stream := terminalSmokeStream(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"file_write","arguments":"{}"}}`, terminal)
			result, err := decodeCodexStream(fragmentedReader{strings.NewReader(stream)}, "model", "", "", nil)
			if err == nil || len(result.ToolCalls) != 0 {
				t.Fatalf("incomplete terminal exposed executable tools: %+v, %v", result, err)
			}
		})
	}
}

func terminalSmokeModel(t *testing.T, responses bool, attempts int, handler http.HandlerFunc) (*Model, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	model, err := New(Config{BaseURL: server.URL + "/responses", UseResponses: responses, Model: "synthetic", MaxAttempts: attempts})
	if err != nil {
		t.Fatal(err)
	}
	return model, count
}

func TestStreamRetryBudgetAndCancellationSmoke(t *testing.T) {
	if providerRetryDelay(1, 0) != 10*time.Second || providerRetryDelay(2, 0) != 20*time.Second {
		t.Fatal("default retry delay changed")
	}
	for _, responses := range []bool{false, true} {
		t.Run(fmt.Sprintf("responses=%v", responses), func(t *testing.T) {
			t.Run("attempts-exhausted", func(t *testing.T) {
				model, count := terminalSmokeModel(t, responses, 3, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"error":{"code":"server_error","message":"synthetic overload"}}`)
				})
				_, err := model.Stream(context.Background(), domain.ModelRequest{}, nil)
				var providerErr *ProviderError
				if !errors.As(err, &providerErr) || count.Load() != 3 {
					t.Fatalf("attempts=%d err=%v", count.Load(), err)
				}
			})
			t.Run("cancel-backoff", func(t *testing.T) {
				model, count := terminalSmokeModel(t, responses, 3, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusServiceUnavailable)
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				_, err := model.Stream(ctx, domain.ModelRequest{}, func(event domain.ModelEvent) error {
					if event.Type == domain.ModelEventProgress {
						cancel()
					}
					return nil
				})
				if !errors.Is(err, context.Canceled) || count.Load() != 1 {
					t.Fatalf("backoff cancellation: attempts=%d err=%v", count.Load(), err)
				}
			})
		})
	}
}

func TestStreamNeverReplaysObservableDeltaSmoke(t *testing.T) {
	for _, responses := range []bool{false, true} {
		chunks := []string{
			`{"choices":[{"delta":{"content":"visible"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"visible"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"file_write","arguments":"{}"}}]}}]}`,
		}
		if responses {
			chunks = []string{
				`{"type":"response.output_text.delta","delta":"visible"}`,
				`{"type":"response.reasoning_text.delta","delta":"visible"}`,
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"file_write","arguments":"{}"}}`,
			}
		}
		for i, chunk := range chunks {
			t.Run(fmt.Sprintf("responses=%v/kind=%d", responses, i), func(t *testing.T) {
				model, count := terminalSmokeModel(t, responses, 3, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, terminalSmokeStream(chunk)) // 派送可見片段後斷線。
				})
				sawOutput := false
				result, err := model.Stream(context.Background(), domain.ModelRequest{}, func(event domain.ModelEvent) error {
					sawOutput = sawOutput || observableModelOutput(event.Type)
					return nil
				})
				if err == nil || !sawOutput || count.Load() != 1 || len(result.ToolCalls) != 0 {
					t.Fatalf("unsafe replay: attempts=%d output=%v calls=%d err=%v", count.Load(), sawOutput, len(result.ToolCalls), err)
				}
			})
		}
	}
}
