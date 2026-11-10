package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/internal/logging"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeConnectionProbeSmoke(t *testing.T) {
	for _, token := range []string{"ping", "wrong"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				io.WriteString(w, `{"data":[{"id":"synthetic"}]}`)
				return
			}
			var request struct {
				ToolChoice struct {
					Type string `json:"type"`
				} `json:"tool_choice"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if r.URL.Path != "/v1/messages" || request.ToolChoice.Type != "auto" {
				t.Error("測試送出不相容的強制工具選擇")
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"type":"message","content":[{"type":"thinking","thinking":"","signature":"synthetic"},{"type":"tool_use","id":"call","name":"provider_connection_test","input":{"token":"`+token+`"}}],"stop_reason":"tool_use"}`)
		}))
		r := &Runtime{Config: Config{Providers: map[string]ProviderConfig{"claude": {Type: "openai-compatible", OpenAICompatible: &openaicompat.Config{UseClaude: true, BaseURL: server.URL + "/v1", Model: "synthetic", MaxAttempts: 1}}}}, logger: logging.Discard()}
		result, err := r.TestProvider(context.Background(), "claude")
		server.Close()
		if token == "ping" && (err != nil || !result.ToolCalling || len(result.Models) != 1) {
			t.Fatalf("相容測試失敗：%v", err)
		}
		if token == "wrong" && err == nil {
			t.Fatal("錯誤工具參數被判定成功")
		}
	}
}
