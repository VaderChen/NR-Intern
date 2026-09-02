package harness

import (
	"AgenticService/src/approval"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/mcpclient"
	"AgenticService/src/tools"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 這個 smoke test 重現使用者實際遇到的情境：同一個 Session 先問製令數量，再換題目
// 問部門數量。除了驗證兩題都真的走到 MCP，也把每一輪實際送出的提示大小量出來——
// 「換個題目就想很久」要先知道第二輪到底比第一輪多背了多少東西。

type mesQuery struct {
	Status string `json:"status"`
}

type mesCount struct {
	Total int    `json:"total"`
	Note  string `json:"note,omitempty"`
}

func newMESServer(t *testing.T, payloadPadding int) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "mars-mes", Version: "1.0.0"}, nil)
	padding := strings.Repeat("製令明細資料。", payloadPadding)
	mcp.AddTool(server, &mcp.Tool{Name: "query_work_orders", Description: "查詢製令"},
		func(context.Context, *mcp.CallToolRequest, mesQuery) (*mcp.CallToolResult, mesCount, error) {
			return nil, mesCount{Total: 264, Note: padding}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "query_departments", Description: "查詢部門"},
		func(context.Context, *mcp.CallToolRequest, mesQuery) (*mcp.CallToolResult, mesCount, error) {
			return nil, mesCount{Total: 18}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "query_operators", Description: "查詢人員"},
		func(context.Context, *mcp.CallToolRequest, mesQuery) (*mcp.CallToolResult, mesCount, error) {
			return nil, mesCount{Total: 264}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	value := httptest.NewServer(handler)
	t.Cleanup(value.Close)
	return value
}

func newMESRuntime(t *testing.T, endpoint string) *tools.Runtime {
	t.Helper()
	manager, err := mcpclient.New([]mcpclient.ServerConfig{{
		ID: "mars-mes", DisplayName: "Mars 智慧工廠", Enabled: true,
		Transport: mcpclient.TransportStreamableHTTP, URL: endpoint,
		StartupTimeoutSeconds: 10, CallTimeoutSeconds: 10,
	}}, "nr-intern-smoke", "test", 512*1024, logging.Discard())
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

func requestSize(request domain.ModelRequest) (steering int, history int) {
	for _, value := range []string{request.SystemPrompt, request.HostPrompt, request.ToolPrompt, request.PhasePrompt, request.ContextPrompt} {
		steering += len([]rune(value))
	}
	for _, message := range request.History {
		history += len([]rune(message.Content))
	}
	history += len([]rune(request.UserPrompt))
	return steering, history
}

// 使用者不會每次都指名「使用 mars-mes 這個 MCP」。提示必須讓模型光看工具說明就能
// 把「製令數量」對上 query_work_orders，否則就會浪費回合去規劃或反問。
func TestSmokeMCPToolsAreDescribedForUnnamedRequests(t *testing.T) {
	server := newMESServer(t, 0)
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)

	definitions, err := runtime.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	staged := stagedToolDefinitions(definitions, false)
	prompt := toolSelectionPhasePrompt(false, staged)

	for _, expected := range []string{
		"mcp__mars-mes__query_work_orders：查詢製令",
		"mcp__mars-mes__query_departments：查詢部門",
		"直接呼叫語意最接近的工具",
		"不要因為使用者沒有指名是哪個服務就改成詢問或先描述計畫",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("phase prompt is missing %q: %s", expected, prompt)
		}
	}

	// 工具目錄本身也要帶上顯示名稱，模型才不必只靠英文工具名猜語意。
	catalog := toolInstructionPrompt(staged)
	if !strings.Contains(catalog, `"label"`) || !strings.Contains(catalog, "查詢製令") {
		t.Fatalf("tool catalog does not carry the human readable label: %s", catalog)
	}
}

// 沒有指名 MCP 的問法必須照樣走到工具，不能因此多花回合。
func TestSmokeUnnamedQuestionStillCallsMCP(t *testing.T) {
	server := newMESServer(t, 0)
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction("mcp__mars-mes__query_work_orders", map[string]any{"status": ""})},
		{Content: "目前共有 264 筆製令。查詢來源：mars-mes 的 query_work_orders。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.MaxCompletionChecks = 1

	result, err := runner.Run(context.Background(), Input{RunID: "run_unnamed", Session: session, UserInput: "給我當前的製令數量"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Message.Content, "264") {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model calls = %d, want 2 (一輪工具、一輪回答)", len(model.requests))
	}
}

// 複合問題（「有多少部門跟人員」）必須在同一輪送出兩個工具指令並一起執行，
// 而不是花一輪去規劃怎麼分次查。
func TestSmokeCompoundQuestionRunsBothToolsInOneTurn(t *testing.T) {
	server := newMESServer(t, 0)
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: `[{"type":"tool_use","tool":"mcp__mars-mes__query_departments","input":{"status":""}},
{"type":"tool_use","tool":"mcp__mars-mes__query_operators","input":{"status":""}}]`},
		{Content: "公司目前有 18 個部門、264 位人員。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_compound", Session: session, UserInput: "公司裡有多少部門跟人員"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model calls = %d, want 2（一輪送出兩個工具、一輪回答）", len(model.requests))
	}
	if !strings.Contains(result.Message.Content, "18") || !strings.Contains(result.Message.Content, "264") {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	results := 0
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, "tool_result") {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("history carries %d tool results, want 2", results)
	}
}

func TestSmokeMarsMESFollowUpQuestion(t *testing.T) {
	server := newMESServer(t, 0)
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	workOrders := "mcp__mars-mes__query_work_orders"
	departments := "mcp__mars-mes__query_departments"
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(workOrders, map[string]any{"status": ""})},
		{Content: "目前共有 264 筆製令。查詢來源：mars-mes 的 query_work_orders，未套用篩選條件。"},
		{Content: instruction(departments, map[string]any{"status": ""})},
		{Content: "目前共有 18 個部門。查詢來源：mars-mes 的 query_departments。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	runner.MaxCompletionChecks = 1

	first, err := runner.Run(context.Background(), Input{RunID: "run_orders", Session: session, UserInput: "使用 mars-mes 這個MCP,給我當前的製令數量"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !strings.Contains(first.Message.Content, "264") {
		t.Fatalf("first answer = %q", first.Message.Content)
	}

	// 第二題換主題，但在同一個 Session：history 已經帶著第一題的問答與工具結果。
	second, err := runner.Run(context.Background(), Input{RunID: "run_departments", Session: session, UserInput: "目前有多少部門"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !strings.Contains(second.Message.Content, "18") {
		t.Fatalf("second answer = %q", second.Message.Content)
	}
	if len(model.requests) != 4 {
		t.Fatalf("model calls = %d, want 4 (每題各一輪工具、一輪回答)", len(model.requests))
	}

	for index, request := range model.requests {
		steering, history := requestSize(request)
		t.Logf("第 %d 次模型請求：steering %5d 字（system+host+tools+phase+context）／history %5d 字／合計 %5d 字",
			index+1, steering, history, steering+history)
	}

	// 換題目的第一輪，模型必須直接呼叫工具，不能又多花一輪描述計畫。
	thirdSteering, _ := requestSize(model.requests[2])
	firstSteering, _ := requestSize(model.requests[0])
	if thirdSteering > firstSteering+400 {
		t.Fatalf("換題目後每輪固定提示膨脹：%d → %d 字", firstSteering, thirdSteering)
	}
}

// MCP 回傳大量資料時，第二題的 history 會把它整包再讀一次。這個測試量出實際成本。
func TestSmokeLargeMCPResultCarriedIntoFollowUp(t *testing.T) {
	server := newMESServer(t, 900) // 約 6,300 字的回傳內容
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	workOrders := "mcp__mars-mes__query_work_orders"
	departments := "mcp__mars-mes__query_departments"
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(workOrders, map[string]any{"status": ""})},
		{Content: "目前共有 264 筆製令。"},
		{Content: instruction(departments, map[string]any{"status": ""})},
		{Content: "目前共有 18 個部門。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	if _, err := runner.Run(context.Background(), Input{RunID: "run_orders", Session: session, UserInput: "給我當前的製令數量"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := runner.Run(context.Background(), Input{RunID: "run_departments", Session: session, UserInput: "目前有多少部門"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	for index, request := range model.requests {
		steering, history := requestSize(request)
		t.Logf("第 %d 次模型請求：steering %5d 字／history %5d 字／合計 %5d 字", index+1, steering, history, steering+history)
	}
	for index, message := range model.requests[2].History {
		t.Logf("  history[%d] role=%s tool=%s %d 字", index, message.Role, message.ToolName, len([]rune(message.Content)))
		// 宣告 output schema 的 Server 會同時回傳 structuredContent 與相同內容的
		// text block；工具結果只能保留一份，否則之後每一輪都重讀兩次。
		if count := strings.Count(message.Content, `\"total\"`); count > 1 {
			t.Fatalf("history[%d] carries the MCP payload %d times", index, count)
		}
	}
	_, firstHistory := requestSize(model.requests[0])
	_, followUpHistory := requestSize(model.requests[2])
	t.Logf("換題目時 history 從 %d 字變成 %d 字（第一題的工具結果被整包帶進來）", firstHistory, followUpHistory)
	if followUpHistory <= firstHistory {
		t.Fatal("follow-up history did not grow; the probe is not measuring what it claims")
	}
}

// Server instructions 屬於整個 MCP Server，不該複製到它的每一個工具定義裡：
// 20 個工具就等於同一段文字在每一輪提示中出現 20 次。
func TestSmokeServerInstructionsAppearOnce(t *testing.T) {
	instructions := "本服務提供製令與部門查詢，請優先使用彙總欄位。"
	definitions := []domain.ToolDefinition{
		{Name: "mcp__mars-mes__query_work_orders", Label: "查詢製令", ServerInstructions: instructions, Capabilities: []string{"mcp", "mcp:mars-mes"}},
		{Name: "mcp__mars-mes__query_departments", Label: "查詢部門", ServerInstructions: instructions, Capabilities: []string{"mcp", "mcp:mars-mes"}},
		{Name: "mcp__mars-mes__query_lines", Label: "查詢線別", ServerInstructions: instructions, Capabilities: []string{"mcp", "mcp:mars-mes"}},
	}

	prompt := toolInstructionPrompt(definitions)

	if count := strings.Count(prompt, instructions); count != 1 {
		t.Fatalf("server instructions appear %d times in the tool catalog", count)
	}
	if !strings.Contains(prompt, "mars-mes："+instructions) {
		t.Fatalf("server instructions lost their server attribution: %s", prompt)
	}
}

// 「請列出現有的CNC機台」這類清單型查詢會回傳大量資料。這個 smoke test 用真的 MCP
// Server 回傳 500 筆機台，確認整個 Run 仍會收斂，並量出資料在每一輪的成本。
func TestSmokeLargeListQueryCompletes(t *testing.T) {
	machines := make([]string, 0, 500)
	for index := 0; index < 500; index++ {
		machines = append(machines, fmt.Sprintf("CNC-%03d 立式加工中心 狀態:運轉中 廠區:A", index))
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mars-mes", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list_machines", Description: "列出機台"},
		func(context.Context, *mcp.CallToolRequest, mesQuery) (*mcp.CallToolResult, machineList, error) {
			return nil, machineList{Machines: machines}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()

	runtime := newMESRuntime(t, endpoint.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction("mcp__mars-mes__list_machines", map[string]any{"status": ""})},
		{Content: "目前共有 500 台 CNC 機台。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	started := time.Now()
	result, err := runner.Run(context.Background(), Input{RunID: "run_machines", Session: session, UserInput: "請列出現有的CNC機台"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("large list query took %s", elapsed)
	}
	if !strings.Contains(result.Message.Content, "500") {
		t.Fatalf("answer = %q", result.Message.Content)
	}
	steering, history := requestSize(model.requests[1])
	t.Logf("清單查詢後的第二輪請求：steering %d 字／history %d 字", steering, history)
	if history < 1000 {
		t.Fatalf("tool result never reached the model: history %d 字", history)
	}
}

type machineList struct {
	Machines []string `json:"machines"`
}

// 同一輪送出兩個需要人工核准的 MCP 工具時，ApprovalCoordinator 每個 Run 只允許一筆
// pending approval：必須依序詢問並全部完成，不能因為第二筆 Begin 衝突就讓整個 Run 失敗。
func TestSmokeTwoApprovalRequiredCallsInOneTurn(t *testing.T) {
	server := newMESServer(t, 0)
	runtime := newMESRuntime(t, server.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: `[{"type":"tool_use","tool":"mcp__mars-mes__query_departments","input":{}},
{"type":"tool_use","tool":"mcp__mars-mes__query_operators","input":{}}]`},
		{Content: "18 個部門、264 位人員。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	coordinator := approval.NewCoordinator([]string{"mcp__*"})
	runner.Approvals = coordinator

	requests := make(chan domain.ToolApprovalRequest, 4)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), Input{RunID: "run_two_approvals", Session: session, UserInput: "部門跟人員各多少"}, func(event domain.EngineEvent) error {
			if event.Type == "run.approval_required" {
				request, _ := event.Payload["approval"].(domain.ToolApprovalRequest)
				requests <- request
			}
			return nil
		})
		done <- err
	}()

	approved := 0
	for approved < 2 {
		select {
		case request := <-requests:
			if err := coordinator.Decide("run_two_approvals", domain.ToolApprovalDecisionInput{
				ApprovalID: request.ID, Decision: domain.ToolApprovalApprove,
			}); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			approved++
		case err := <-done:
			t.Fatalf("run finished after %d approvals: %v", approved, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d approval requests arrived", approved)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after both approvals")
	}
}

// MCP 回傳超大結果時，進入模型的內容必須被截斷並說明如何縮小查詢，
// 否則每一輪都要重讀整包資料，正是「跑很久卻沒有結果」的來源。
func TestSmokeOversizedToolResultIsBoundedForTheModel(t *testing.T) {
	rows := make([]string, 0, 4000)
	for index := 0; index < 4000; index++ {
		rows = append(rows, fmt.Sprintf("CNC-%04d 立式加工中心 狀態:運轉中 廠區:A 保養日:2026-09-01", index))
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mars-mes", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list_machines", Description: "列出機台"},
		func(context.Context, *mcp.CallToolRequest, mesQuery) (*mcp.CallToolResult, machineList, error) {
			return nil, machineList{Machines: rows}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()

	runtime := newMESRuntime(t, endpoint.URL)
	session := smokeSession(t)
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction("mcp__mars-mes__list_machines", map[string]any{"status": ""})},
		{Content: "資料量過大，已回報需要縮小範圍。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	if _, err := runner.Run(context.Background(), Input{RunID: "run_big", Session: session, UserInput: "請列出現有的CNC機台"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, history := requestSize(model.requests[1])
	t.Logf("超大結果時第二輪 history = %d 字", history)
	if history > 30_000 {
		t.Fatalf("history %d 字：工具結果沒有被限制在 context 預算內", history)
	}
	var toolContent string
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, "CNC-0001") {
			toolContent = message.Content
		}
	}
	if toolContent == "" {
		t.Fatal("tool result never reached the model")
	}
	if !strings.Contains(toolContent, "縮小查詢範圍") {
		t.Fatalf("截斷後沒有告訴模型下一步該怎麼做：%s", toolContent[len(toolContent)-200:])
	}
}
