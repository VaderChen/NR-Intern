package harness_test

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/domain"
	"AgenticService/src/harness"
	"AgenticService/src/tools"
	nativefiles "AgenticService/src/tools/native/files"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestOpenAICompatibleHarnessExecutesInstructionToolLoop 驗證不依賴 Provider
// tool_calls：模型輸出 JSON 指令、Harness 執行原生工具、以文字結果送回模型，
// 最後才產生使用者答案。
func TestOpenAICompatibleHarnessExecutesInstructionToolLoop(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var payload struct {
			Messages   []map[string]any `json:"messages"`
			Tools      []map[string]any `json:"tools"`
			ToolChoice string           `json:"tool_choice"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		turn := requests.Add(1)
		if turn == 1 {
			if len(payload.Tools) != 0 || payload.ToolChoice != "" {
				http.Error(writer, "instruction mode must not advertise native tools", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(writer, "data: {\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"type\\\":\\\"tool_use\\\",\\\"tool\\\":\\\"directory_list\\\",\\\"input\\\":{\\\"path\\\":\\\".\\\"}}\"}}]}\n\n")
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
			return
		}

		toolResult := ""
		finalizationPrompt := ""
		for _, message := range payload.Messages {
			if message["role"] == "user" && strings.Contains(fmt.Sprint(message["content"]), `"type":"tool_result"`) {
				toolResult = fmt.Sprint(message["content"])
			}
			if message["role"] == "system" && strings.Contains(fmt.Sprint(message["content"]), "收斂與最終回答階段") {
				finalizationPrompt = fmt.Sprint(message["content"])
			}
		}
		if !strings.Contains(toolResult, "target.txt") {
			http.Error(writer, "tool result was not returned to the model", http.StatusBadRequest)
			return
		}
		if !strings.Contains(finalizationPrompt, "不得揭露") {
			http.Error(writer, "finalization phase was not separated from the tool loop", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(writer, "data: {\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"已找到 target.txt\"}}]}\n\n")
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer provider.Close()

	model, err := openaicompat.New(openaicompat.Config{BaseURL: provider.URL + "/v1", Model: "test-model", MaxAttempts: 1})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	sessions, err := filestore.NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("create session repository: %v", err)
	}
	session, err := sessions.Create(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_test"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspaceRoot, _ := session.Metadata["workspace_root"].(string)
	if err := os.WriteFile(filepath.Join(workspaceRoot, "target.txt"), []byte("tool loop"), 0o600); err != nil {
		t.Fatalf("create sandbox fixture: %v", err)
	}
	registry, err := tools.NewRegistry(tools.RegistryConfig{}, nativefiles.NewDirectoryListTool(64*1024, 100))
	if err != nil {
		t.Fatalf("create native tool registry: %v", err)
	}
	runner := &harness.Runner{Model: model, Tools: registry, Sessions: sessions, ToolCallMode: harness.ToolCallModeInstruction}

	result, err := runner.Run(context.Background(), harness.Input{RunID: "run_integration", Session: session, UserInput: "請列出目前目錄"}, nil)
	if err != nil {
		t.Fatalf("run harness: %v", err)
	}
	if result.Message.Content != "已找到 target.txt" {
		t.Fatalf("answer = %q, want final answer after tool result", result.Message.Content)
	}
	if requests.Load() != 2 {
		t.Fatalf("provider requests = %d, want tool turn plus final-answer turn", requests.Load())
	}
	messages, err := sessions.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	foundToolResult := false
	for _, message := range messages {
		if message.Role == "tool" && strings.Contains(message.Content, "target.txt") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatal("instruction tool execution result was not persisted in the transcript")
	}
}
