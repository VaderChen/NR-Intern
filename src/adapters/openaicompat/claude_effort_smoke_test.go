package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeEffortSmoke(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for mode, want := range map[string]string{"": "", "auto": "", "none": "low", "minimal": "low", "low": "low", "medium": "medium", " HIGH ": "high", "xhigh": "xhigh"} {
			t.Run(mode+map[bool]string{true: "/stream", false: "/json"}[streaming], func(t *testing.T) {
				calls, progress := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					var output map[string]string
					if raw := body["output_config"]; len(raw) > 0 {
						if err := json.Unmarshal(raw, &output); err != nil {
							t.Error(err)
						}
					}
					if output["effort"] != want || (want == "" && body["output_config"] != nil) {
						t.Errorf("effort=%q want=%q", output["effort"], want)
					}
					if body["thinking"] != nil || body["reasoning_effort"] != nil {
						t.Error("不應混入停用思考或 OpenAI effort 欄位")
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, claudeSmokeStream(`{"type":"message_start","message":{}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`))
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
					}
				}))
				defer server.Close()
				m, err := New(Config{UseClaude: true, BaseURL: server.URL, Model: "synthetic", DisableStreaming: !streaming, MaxAttempts: 1})
				if err != nil {
					t.Fatal(err)
				}
				result, err := m.Stream(context.Background(), domain.ModelRequest{UserPrompt: "test", ThinkingMode: mode}, func(e domain.ModelEvent) error {
					if e.Type == domain.ModelEventProgress {
						progress++
					}
					return nil
				})
				if err != nil || result.Content != "OK" || calls != 1 {
					t.Fatalf("程度設定造成請求失敗：%v", err)
				}
				if (mode == "none") != (progress == 1) {
					t.Fatal("未明確說明關閉思考的對應行為")
				}
			})
		}
	}
}

func TestClaudeInvalidEffortSmoke(t *testing.T) {
	m, _ := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	_, err := m.claudeRequest(domain.ModelRequest{ThinkingMode: "invalid"}, "synthetic", true)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("未知程度應明確拒絕")
	}
}
