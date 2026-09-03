package harness

import (
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/mcpclient"
	"AgenticService/src/tools"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 這一組是 MCP 的 smoke test：不使用 fake 工具，而是把真的 MCP Server、
// 真的 mcpclient.Manager、真的 tools.Runtime 與真的 Harness Runner 串起來，
// 驗證「模型看得到工具 → 呼叫得到 → 結果回得來」這條路徑整條是通的。
// 只有模型是腳本化的，因為這裡要驗的是 Harness 與 MCP，不是模型品質。

type smokeMCPServer struct {
	http     *httptest.Server
	mu       sync.Mutex
	handler  http.Handler
	calls    atomic.Int64
	lastArgs atomic.Pointer[string]
}

type workOrderQuery struct {
	Status string `json:"status"`
}

type workOrderResult struct {
	Count int `json:"count"`
}

func newSmokeMCPServer(t *testing.T) *smokeMCPServer {
	t.Helper()
	harness := &smokeMCPServer{}
	harness.reload()
	harness.http = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		harness.mu.Lock()
		handler := harness.handler
		harness.mu.Unlock()
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(harness.http.Close)
	return harness
}

// reload 換掉背後的 MCP Server，模擬遠端服務重啟（舊 session id 失效）。
func (s *smokeMCPServer) reload() {
	server := mcp.NewServer(&mcp.Implementation{Name: "mars-mes", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "query_work_orders", Description: "查詢目前製令"},
		func(_ context.Context, _ *mcp.CallToolRequest, input workOrderQuery) (*mcp.CallToolResult, workOrderResult, error) {
			s.calls.Add(1)
			value := input.Status
			s.lastArgs.Store(&value)
			return nil, workOrderResult{Count: 42}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
}

func newSmokeToolRuntime(t *testing.T, server *smokeMCPServer) *tools.Runtime {
	t.Helper()
	manager, err := mcpclient.New([]mcpclient.ServerConfig{{
		ID: "mars-mes", DisplayName: "Mars 智慧工廠", Enabled: true,
		Transport: mcpclient.TransportStreamableHTTP, URL: server.http.URL,
		StartupTimeoutSeconds: 10, CallTimeoutSeconds: 10,
	}}, "nr-intern-smoke", "test", 64*1024, logging.Discard())
	if err != nil {
		t.Fatalf("mcpclient.New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	native, err := tools.NewRegistry(tools.RegistryConfig{
		AllowElevated: true,
		Permissions:   domain.PermissionPolicy{DefaultProfile: domain.DefaultPermissionProfile, ElevatedProfiles: []string{domain.DefaultPermissionProfile}},
		Logger:        logging.Discard(),
	})
	if err != nil {
		t.Fatalf("tools.NewRegistry: %v", err)
	}
	return &tools.Runtime{Native: native, MCP: manager}
}

func smokeSession(t *testing.T) domain.Session {
	t.Helper()
	session := testSession()
	session.Metadata = map[string]any{"workspace_root": t.TempDir()}
	return session
}

func smokeRunner(sessions *memorySessions, model *scriptedModel, runtime *tools.Runtime) *Runner {
	return &Runner{
		Model:        model,
		Tools:        runtime,
		Sessions:     sessions,
		Context:      &ContextManager{Model: model, Sessions: sessions},
		Budget:       domain.RunBudget{MaxTurns: 6},
		SystemPrompt: "system",
		ToolCallMode: ToolCallModeInstruction,
		Logger:       logging.Discard(),
	}
}

func instruction(tool string, input map[string]any) string {
	payload, _ := json.Marshal(map[string]any{"type": "tool_use", "tool": tool, "input": input, "reason": "查詢製令"})
	return string(payload)
}

// mcpToolName 從實際的工具目錄取得公開名稱，確保測試用的是 Harness 真的公開給
// 模型的名字，而不是測試自己猜的字串。
func mcpToolName(t *testing.T, runtime *tools.Runtime, session domain.Session) string {
	t.Helper()
	definitions, err := runtime.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Name, "mcp__") {
			return definition.Name
		}
	}
	t.Fatalf("no MCP tool was offered: %+v", definitions)
	return ""
}

func TestSmokeMCPToolIsDiscoveredAndCallable(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)

	name := mcpToolName(t, runtime, session)
	if name != "mcp__mars-mes__query_work_orders" {
		t.Fatalf("public tool name = %q", name)
	}

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "目前製令共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_smoke", Session: session, UserInput: "給我當前的製令數量"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server was called %d times, want 1", server.calls.Load())
	}
	if value := server.lastArgs.Load(); value == nil || *value != "released" {
		t.Fatalf("tool arguments did not reach the MCP server: %v", value)
	}
	if result.Message.Content != "目前製令共 42 筆。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
	// 第二輪的 history 必須帶著工具結果，否則模型是在沒有資料的情況下回答。
	if len(model.requests) < 2 {
		t.Fatalf("model was called %d times", len(model.requests))
	}
	var sawResult bool
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, "42") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("tool result never reached the model: %+v", model.requests[1].History)
	}
}

// 模型很容易輸出別的產品的 MCP 命名（例如 mcp__Server__tool 但 Server 名稱不同）。
// 這種指令必須進入協定修正流程，不能被當成最終回答直接顯示給使用者。
func TestSmokeUnknownMCPToolTriggersProtocolRepair(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction("mcp__IntegTERM__execute_single_ssh_command", map[string]any{"command": "ls"})},
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "目前製令共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	repairs := 0
	result, err := runner.Run(context.Background(), Input{RunID: "run_repair", Session: session, UserInput: "給我當前的製令數量"}, func(event domain.EngineEvent) error {
		if event.Type == "run.tool_protocol_repair" {
			repairs++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if repairs == 0 {
		t.Fatal("an unknown MCP tool name did not trigger the protocol repair path")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server calls = %d, want 1 after the repair", server.calls.Load())
	}
	if strings.Contains(result.Message.Content, "IntegTERM") {
		t.Fatalf("the invalid instruction leaked into the answer: %q", result.Message.Content)
	}
}

// MCP Server 重啟後，同一個 Run 內的下一次呼叫必須自動重連，而不是整段失敗。
func TestSmokeRunRecoversWhenMCPServerRestartsMidRun(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: instruction(name, map[string]any{"status": "closed"})},
		{Content: "兩次查詢都完成了。"},
	}}
	model.onStream = func(turn int) {
		if turn == 1 {
			// 第一次工具結果回來之後、第二次呼叫送出之前，遠端服務重啟。
			server.reload()
		}
	}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_restart", Session: session, UserInput: "查兩次"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if server.calls.Load() != 2 {
		t.Fatalf("MCP server calls = %d, want 2 across the restart", server.calls.Load())
	}
	if result.Message.Content != "兩次查詢都完成了。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
}

// MCP 工具一律需要人工核准；核准後必須真的執行，拒絕則不得執行。
func TestSmokeMCPToolRequiresApproval(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "目前製令共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	coordinator := approval.NewCoordinator([]string{name})
	runner.Approvals = coordinator

	requests := make(chan domain.ToolApprovalRequest, 1)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), Input{RunID: "run_approval", Session: session, UserInput: "查製令"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				requests <- request
			}
			return nil
		})
		done <- err
	}()

	var request domain.ToolApprovalRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP tool did not ask for approval")
	}
	if request.ToolName != name {
		t.Fatalf("approval is for %q, want %q", request.ToolName, name)
	}
	if server.calls.Load() != 0 {
		t.Fatal("MCP tool ran before approval")
	}
	if err := coordinator.Decide("run_approval", domain.ToolApprovalDecisionInput{ApprovalID: request.ID, Decision: domain.ToolApprovalApprove}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not continue after approval")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server calls = %d, want 1 after approval", server.calls.Load())
	}
}

// MCP Server 掛掉時，Run 必須以清楚的失敗訊息收尾，而不是卡住或無聲結束。
func TestSmokeUnreachableMCPServerFailsClearly(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)
	server.http.Close()

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "查詢失敗，MCP 無法連線。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	started := time.Now()
	result, err := runner.Run(context.Background(), Input{RunID: "run_down", Session: session, UserInput: "查製令"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("run took %s before reporting an unreachable MCP server", elapsed)
	}
	if len(model.requests) < 2 {
		t.Fatalf("model was called %d times; the failure never reached it", len(model.requests))
	}
	var sawFailure bool
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, "MCP") && strings.Contains(message.Content, "失敗") {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Fatalf("the MCP failure was not reported to the model: %+v", model.requests[1].History)
	}
	if result.Message.Content == "" {
		t.Fatal("run finished without an answer")
	}
}

func TestSmokeMCPToolCatalogReportsConnectionState(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	_ = mcpToolName(t, runtime, session)

	entries := runtime.Catalog(&session)
	var found bool
	for _, entry := range entries {
		if strings.HasPrefix(entry.Definition.Name, "mcp__") {
			found = true
			if !entry.Allowed || !entry.Available {
				t.Fatalf("connected MCP tool is not available: %+v", entry)
			}
			if !entry.Definition.RequiresPermission {
				t.Fatalf("MCP tool must require permission: %+v", entry.Definition)
			}
		}
	}
	if !found {
		t.Fatalf("MCP tool missing from the catalog: %+v", entries)
	}
}

// 這是使用者實際遇到的情況：模型只輸出「我會先確認⋯⋯再讀取⋯⋯」的計畫，
// 一個工具都沒呼叫。Harness 不能把這種承諾當成最終回答交出去。
func TestSmokePromiseWithoutToolCallIsChallenged(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "我會先確認 mars-mes 可用的製令查詢能力，再讀取目前製令資料並統計筆數。"},
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "目前製令共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.MaxCompletionChecks = 1

	checks := 0
	result, err := runner.Run(context.Background(), Input{RunID: "run_promise", Session: session, UserInput: "使用 mars-mes 這個 MCP，給我當前的製令數量"}, func(event domain.EngineEvent) error {
		if event.Type == "run.completion_check" && event.Payload["reason"] == "no_tool_executed" {
			checks++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checks != 1 {
		t.Fatalf("completion checks = %d, want exactly one push-back", checks)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server calls = %d; the promise never turned into an actual call", server.calls.Load())
	}
	if result.Message.Content != "目前製令共 42 筆。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
}

// 追問額度必須有界：模型堅持不呼叫工具時，Harness 只追問一次就收尾，
// 不能變成無限來回。
func TestSmokeToollessChallengeHappensOnlyOnce(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "我會先確認可用的查詢能力。"},
		{Content: "我還是先說明一下計畫。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.MaxCompletionChecks = 1

	checks := 0
	result, err := runner.Run(context.Background(), Input{RunID: "run_promise_twice", Session: session, UserInput: "查一下"}, func(event domain.EngineEvent) error {
		if event.Type == "run.completion_check" {
			checks++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checks != 1 {
		t.Fatalf("completion checks = %d, want 1", checks)
	}
	if result.Message.Content != "我還是先說明一下計畫。" {
		t.Fatalf("run did not settle after the single check: %q", result.Message.Content)
	}
	if server.calls.Load() != 0 {
		t.Fatalf("MCP server calls = %d, want 0", server.calls.Load())
	}
}

// 系統工具優先階段只公開 shell_exec 等少數內建工具，但 MCP 工具不受這個分段限制。
// 提示必須明講這件事，否則模型會以為 MCP 還不能用，先花一輪描述計畫。
func TestSmokeToolPhasePromptSaysMCPIsCallableNow(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	definitions, err := runtime.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	staged := stagedToolDefinitions(definitions, false)
	prompt := toolSelectionPhasePrompt(false, staged)

	if !strings.Contains(prompt, name) {
		t.Fatalf("tool phase prompt does not name the callable MCP tool: %s", prompt)
	}
	if !strings.Contains(prompt, "不必先用 shell_exec 試探") {
		t.Fatalf("tool phase prompt does not say MCP can be called directly: %s", prompt)
	}
	if strings.Contains(toolSelectionPhasePrompt(false, nil), "MCP") {
		t.Fatal("the MCP note must not appear when no MCP tool is connected")
	}
}

// 唯讀工具第一輪就要能用：讀檔案不該先付一輪註定失敗的 shell 命令。
func TestSmokeReadOnlyToolsAreAvailableOnTheFirstTurn(t *testing.T) {
	definitions := []domain.ToolDefinition{
		{Name: "file_read", ReadOnly: true, Category: "files"},
		{Name: "directory_list", ReadOnly: true, Category: "files"},
		{Name: "document_read", ReadOnly: true, Category: "documents"},
		{Name: "memory_search", ReadOnly: true, Category: "memory"},
		{Name: "file_write", RequiresPermission: true},
		{Name: "directory_create", RequiresPermission: true},
		{Name: "ssh_exec", RequiresPermission: true},
		{Name: "shell_exec"},
		{Name: "wait_for", ReadOnly: true},
		{Name: "plan_get", ReadOnly: true},
		{Name: "document_create", RequiresPermission: true, Category: "documents"},
		{Name: "mcp__mars-mes__query_work_orders", RequiresPermission: true},
	}

	staged := availableToolNamesSorted(stagedToolDefinitions(definitions, false))
	got := strings.Join(staged, ",")
	// 唯讀工具與辦公文件產出都在第一階段就公開：使用者說「轉成 Excel」時，
	// 沒有 document_create 就只能用 shell 去找 python，失敗後才退成 CSV。
	// 目錄大小改由工具檢索控制，不再靠延後整個文件家族來省提示。
	want := "directory_list,document_create,document_read,file_read,mcp__mars-mes__query_work_orders,memory_search,plan_get,shell_exec,wait_for"
	if got != want {
		t.Fatalf("system-first stage exposes %s, want %s", got, want)
	}

	full := availableToolNamesSorted(stagedToolDefinitions(definitions, true))
	if len(full) != len(definitions) {
		t.Fatalf("builtin fallback stage exposes %d tools, want the full catalog", len(full))
	}
}

// 唯讀 MCP 工具不再逐次要求核准；有副作用的 MCP 工具仍然要核准。
// MCP 的唯讀屬性來自 Server 宣告的 readOnlyHint，只有開啟 trust_annotations 才採信。
func TestSmokeReadOnlyMCPToolSkipsApproval(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "目前製令共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.Approvals = approval.NewCoordinator([]string{"mcp__*"})
	// 模擬管理者已對這個 Server 開啟 trust_annotations：工具目錄標記為唯讀。
	runner.Tools = readOnlyToolRuntime{Runtime: runtime}

	skipped := 0
	approvalRequests := 0
	result, err := runner.Run(context.Background(), Input{RunID: "run_read_only", Session: session, UserInput: "查製令"}, func(event domain.EngineEvent) error {
		switch event.Type {
		case "run.approval_skipped":
			if event.Payload["reason"] == "read_only_tool" {
				skipped++
			}
		case "run.approval_required":
			approvalRequests++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if approvalRequests != 0 {
		t.Fatalf("read-only MCP tool asked for approval %d times", approvalRequests)
	}
	if skipped != 1 {
		t.Fatalf("approval skip was not recorded: %d", skipped)
	}
	if server.calls.Load() != 1 || result.Message.Content != "目前製令共 42 筆。" {
		t.Fatalf("calls=%d answer=%q", server.calls.Load(), result.Message.Content)
	}
}

// readOnlyToolRuntime 把 MCP 工具目錄標成唯讀，等同於管理者開啟 trust_annotations
// 且 Server 宣告了 readOnlyHint 的狀態。
type readOnlyToolRuntime struct {
	*tools.Runtime
}

func (r readOnlyToolRuntime) Definitions(ctx context.Context, session domain.Session) ([]domain.ToolDefinition, error) {
	definitions, err := r.Runtime.Definitions(ctx, session)
	if err != nil {
		return nil, err
	}
	for index := range definitions {
		if strings.HasPrefix(definitions[index].Name, "mcp__") {
			definitions[index].ReadOnly = true
		}
	}
	return definitions, nil
}

// 沒有開啟 trust_annotations 時，MCP 工具不算唯讀，仍然逐次核准。
func TestSmokeUntrustedMCPToolStillRequiresApproval(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "完成"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	coordinator := approval.NewCoordinator([]string{"mcp__*"})
	runner.Approvals = coordinator

	requests := make(chan domain.ToolApprovalRequest, 1)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), Input{RunID: "run_untrusted", Session: session, UserInput: "查製令"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				requests <- request
			}
			return nil
		})
		done <- err
	}()
	select {
	case request := <-requests:
		if err := coordinator.Decide("run_untrusted", domain.ToolApprovalDecisionInput{ApprovalID: request.ID, Decision: domain.ToolApprovalApprove}); err != nil {
			t.Fatalf("Decide: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP tool without trusted annotations must still ask for approval")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish")
	}
}

// 被完成度閘門追問的那一段只是中間產物。過去它照一般回答寫進 transcript，
// 使用者就會在對話裡看到兩份幾乎一樣的答案。
func TestSmokeChallengedAnswerIsNotShownTwice(t *testing.T) {
	server := newSmokeMCPServer(t)
	runtime := newSmokeToolRuntime(t, server)
	session := smokeSession(t)
	name := mcpToolName(t, runtime, session)

	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: "「報工單回報」是指現場人員把生產結果回報到系統。"},
		{Content: instruction(name, map[string]any{"status": "released"})},
		{Content: "「報工單回報」是指現場人員把生產結果回報到系統，共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.MaxCompletionChecks = 1

	result, err := runner.Run(context.Background(), Input{RunID: "run_challenge", Session: session, UserInput: "什麼是報工單回報"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	messages, err := sessions.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	visible := 0
	for _, message := range messages {
		if message.Role != "assistant" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		if internal, _ := message.Metadata["internal"].(bool); internal {
			continue
		}
		visible++
	}
	if visible != 1 {
		t.Fatalf("使用者看得到 %d 則回答，只應該有最後一則", visible)
	}
	if !strings.Contains(result.Message.Content, "42") {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
}
