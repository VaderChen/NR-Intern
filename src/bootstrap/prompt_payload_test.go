package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/domain"
)

// capturedRequest 是模型端實際收到的請求。工具目錄有沒有被過濾，只有這裡看得到準。
type capturedRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Tools    []map[string]any `json:"tools"`
}

type fakeProvider struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
	replies  []string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	provider := &fakeProvider{}
	provider.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// 啟動時的模型限制探測走 /v1/models，不是對話請求，不列入紀錄。
		// 這台刻意不回報 context_length，與實測的 mlx-server 行為一致。
		if strings.HasSuffix(request.URL.Path, "/models") {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"object":"list","data":[{"id":"test-model","object":"model"}]}`)
			return
		}
		var captured capturedRequest
		_ = json.NewDecoder(request.Body).Decode(&captured)
		provider.mu.Lock()
		index := len(provider.requests)
		provider.requests = append(provider.requests, captured)
		reply := `{"model":"test","choices":[{"message":{"role":"assistant","content":"好的。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
		if index < len(provider.replies) {
			reply = provider.replies[index]
		}
		provider.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, reply)
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *fakeProvider) first(t *testing.T) capturedRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("the model was never called")
	}
	return p.requests[0]
}

func promptPayloadSize(request capturedRequest) int {
	size := 0
	for _, message := range request.Messages {
		encoded, _ := json.Marshal(message)
		size += len([]rune(string(encoded)))
	}
	for _, tool := range request.Tools {
		encoded, _ := json.Marshal(tool)
		size += len([]rune(string(encoded)))
	}
	return size
}

func buildTestRuntime(t *testing.T, provider *fakeProvider, extendedTools, retrieval bool) *Runtime {
	t.Helper()
	config := DefaultConfig()
	config.RAMDisk.Enabled = false
	config.DataDir = t.TempDir()
	config.ExtendedTools = extendedTools
	config.ToolRetrieval = retrieval
	config.DefaultProviderID = "fake"
	config.Providers = map[string]ProviderConfig{"fake": {
		Type:        "openai-compatible",
		DisplayName: "Fake",
		OpenAICompatible: &openaicompat.Config{
			BaseURL: provider.server.URL, APIKey: "test", Model: "test-model", DisableStreaming: true,
		},
	}}
	runtime, err := Build(config)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func runOnce(t *testing.T, extendedTools, retrieval bool, prompt string) capturedRequest {
	t.Helper()
	provider := newFakeProvider(t)
	runtime := buildTestRuntime(t, provider, extendedTools, retrieval)

	ctx := context.Background()
	workspace, err := runtime.Application.CreateWorkspace(ctx, domain.CreateWorkspaceInput{
		Name: "測試", ProviderIDs: []string{"fake"}, DefaultProviderID: "fake",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	session, err := runtime.Application.CreateSession(ctx, "general-agent", domain.CreateSessionInput{Title: "測試", WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := runtime.Application.StartRun(ctx, domain.RunInput{SessionID: session.ID, UserInput: prompt}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		done := len(provider.requests) > 0
		provider.mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return provider.first(t)
}

// 「開啟擴充工具集就卡住」的第一個問題是：送出去的請求到底有沒有被過濾。
// 這個測試量的是模型端實際收到的 body，不是我們以為送了什麼。
func TestExtendedToolsStayFilteredBeforeTheRequestIsSent(t *testing.T) {
	prompt := "幫我看一下這個目錄有哪些檔案"

	lean := runOnce(t, false, true, prompt)
	extended := runOnce(t, true, true, prompt)
	unfiltered := runOnce(t, true, false, prompt)

	names := func(request capturedRequest) []string {
		result := make([]string, 0, len(request.Tools))
		for _, tool := range request.Tools {
			function, _ := tool["function"].(map[string]any)
			if function == nil {
				function = tool
			}
			result = append(result, fmt.Sprint(function["name"]))
		}
		return result
	}
	t.Logf("精簡工具集工具：%v", names(lean))
	t.Logf("擴充＋檢索工具：%v", names(extended))
	t.Logf("擴充未檢索工具：%v", names(unfiltered))
	t.Logf("精簡工具集：%d 個工具／%d 字", len(lean.Tools), promptPayloadSize(lean))
	t.Logf("擴充工具集（檢索開）：%d 個工具／%d 字", len(extended.Tools), promptPayloadSize(extended))
	t.Logf("擴充工具集（檢索關）：%d 個工具／%d 字", len(unfiltered.Tools), promptPayloadSize(unfiltered))

	// wait_for 會實際阻塞（單次最長 30 分鐘）且不需要人工核准。與等待無關的需求
	// 不該看到它——它一度被列為「核心工具」永不過濾，結果是小模型隨手呼叫一次，
	// 整個 Run 就安靜停住，畫面上跟當機沒兩樣。
	for _, name := range names(extended) {
		if name == "wait_for" || name == "ssh_wait" {
			t.Fatalf("a blocking tool was offered for an unrelated request: %v", names(extended))
		}
	}
	// 檢索必須真的拿掉東西，而不是原封不動再加一個 find_tools。
	if len(extended.Tools) >= len(unfiltered.Tools) {
		t.Fatalf("retrieval did not filter the extended catalog: %d vs %d tools", len(extended.Tools), len(unfiltered.Tools))
	}
	if !containsName(names(extended), "find_tools") {
		t.Fatalf("find_tools must stay available so filtered-out tools remain reachable: %v", names(extended))
	}
}

// 「請把上述結果轉成 EXCEL 檔案」的實際路徑：模型呼叫 document_create。
// 這個工具需要人工核准，而核准在沒人回應時是無限等待——使用者看到的就是卡住。
// 這個測試確認 Harness 至少會發出核准請求，而不是靜靜停在那裡什麼都不說。
func TestDocumentCreateRequestsApprovalInsteadOfStallingSilently(t *testing.T) {
	provider := newFakeProvider(t)
	provider.replies = []string{
		`{"model":"test","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"document_create","arguments":"{\"path\":\"out.xlsx\",\"format\":\"xlsx\",\"sheets\":[{\"name\":\"S\",\"rows\":[[\"a\"]]}]}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"model":"test","choices":[{"message":{"role":"assistant","content":"完成"},"finish_reason":"stop"}]}`,
	}
	runtime := buildTestRuntime(t, provider, true, true)
	ctx := context.Background()
	workspace, err := runtime.Application.CreateWorkspace(ctx, domain.CreateWorkspaceInput{
		Name: "測試", ProviderIDs: []string{"fake"}, DefaultProviderID: "fake",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	session, err := runtime.Application.CreateSession(ctx, "general-agent", domain.CreateSessionInput{Title: "測試", WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	run, err := runtime.Application.StartRun(ctx, domain.RunInput{SessionID: session.ID, UserInput: "請把上述結果轉成 EXCEL 檔案"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var pending *domain.ToolApprovalRequest
	for time.Now().Before(deadline) {
		current, err := runtime.Application.GetRun(ctx, run.ID)
		if err == nil && current.PendingApproval != nil {
			pending = current.PendingApproval
			break
		}
		if err == nil && (current.Status == "completed" || current.Status == "failed") {
			t.Fatalf("run finished as %q without asking for approval", current.Status)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if pending == nil {
		t.Fatal("document_create never surfaced an approval request; the run just sat there")
	}
	t.Logf("核准請求：tool=%s", pending.ToolName)
}

// 「請把上述結果轉成 EXCEL 檔案」必須在第一輪就看得到能產 XLSX 的工具。
//
// 實測這條路徑壞在兩個地方：辦公文件家族整個被延後到「shell 失敗之後」，
// 而且 document_create 的說明只寫 XLSX、沒有「Excel」這個字，檢索一個字都對不上。
// 結果是模型花四分四十秒用 shell 找 python 的 openpyxl，失敗後退成 CSV。
func TestExcelRequestReachesTheDocumentToolOnTheFirstTurn(t *testing.T) {
	request := runOnce(t, true, true, "請把上述結果轉成 EXCEL 檔案")
	offered := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		function, _ := tool["function"].(map[string]any)
		if function == nil {
			function = tool
		}
		offered = append(offered, fmt.Sprint(function["name"]))
	}
	t.Logf("第一輪工具：%v", offered)
	if !containsName(offered, "document_create") {
		t.Fatalf("document_create was not offered for an explicit Excel request: %v", offered)
	}
}

func containsName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
