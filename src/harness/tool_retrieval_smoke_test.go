package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/mcpclient"
	"AgenticService/src/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 這一組驗的是「大型 MCP Server」這個實際案例：外掛型 Server 公開數十上百個工具，
// 工具定義每一次請求都整份送出，實測一個「HELLO」就送出 111,172 tokens。
// 這裡用同樣形狀的目錄跑真的 Runner，確認檢索過後模型收到的目錄變小，
// 而且沒被列出的工具仍然拿得到、呼叫得到。

type largeMCPServer struct {
	http     *httptest.Server
	mu       sync.Mutex
	handler  http.Handler
	calls    atomic.Int64
	lastTool atomic.Pointer[string]
}

type pluginQuery struct {
	Keyword string `json:"keyword"`
}

type pluginResult struct {
	Count int `json:"count"`
}

// largeMCPTools 模擬 mars-mes 的外掛命名與中文說明。
var largeMCPTools = []struct {
	name        string
	description string
}{
	{"plugin_mo__mo_query", "查詢製令主檔與目前進度"},
	{"plugin_dispatchstatus__dispatchstatus_query", "查詢派工單狀態"},
	{"plugin_department__department_list", "列出所有部門"},
	{"plugin_employee__employee_list", "列出部門人員名冊"},
	{"plugin_equipment__equipment_utilization", "查詢設備稼動與嫁動率"},
	{"plugin_quality__quality_defect_query", "查詢不良品與缺陷代碼"},
	{"plugin_warehouse__warehouse_stock_query", "查詢倉庫庫存數量"},
}

func newLargeMCPServer(t *testing.T, filler int) *largeMCPServer {
	t.Helper()
	instance := &largeMCPServer{}
	server := mcp.NewServer(&mcp.Implementation{Name: "mars-mes", Version: "1.0.0"}, nil)
	register := func(name, description string) {
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: description},
			func(_ context.Context, request *mcp.CallToolRequest, _ pluginQuery) (*mcp.CallToolResult, pluginResult, error) {
				instance.calls.Add(1)
				called := request.Params.Name
				instance.lastTool.Store(&called)
				return nil, pluginResult{Count: 42}, nil
			})
	}
	for _, tool := range largeMCPTools {
		register(tool.name, tool.description)
	}
	// filler 是這個案例的重點：真正壓垮提示的是那幾十個這一輪根本用不到的工具。
	for index := 0; index < filler; index++ {
		register(
			fmt.Sprintf("plugin_report%02d__report%02d_query", index, index),
			fmt.Sprintf("查詢第 %d 號報表的統計資料，支援日期區間與群組欄位", index),
		)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	instance.mu.Lock()
	instance.handler = handler
	instance.mu.Unlock()
	instance.http = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		instance.mu.Lock()
		current := instance.handler
		instance.mu.Unlock()
		current.ServeHTTP(writer, request)
	}))
	t.Cleanup(instance.http.Close)
	return instance
}

func newLargeToolRuntime(t *testing.T, server *largeMCPServer) *tools.Runtime {
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

func mcpToolCount(definitions []domain.ToolDefinition) int {
	count := 0
	for _, definition := range definitions {
		if isMCPToolName(definition.Name) {
			count++
		}
	}
	return count
}

// offeredMCPTools 回報這次請求真的送給模型的 MCP 工具。instruction 協定把目錄
// 放在 ToolPrompt，native 協定放在 Tools 欄位，兩種都要看得懂。
func offeredMCPTools(request domain.ModelRequest, catalog []domain.ToolDefinition) []string {
	names := []string{}
	if len(request.Tools) > 0 {
		for _, definition := range request.Tools {
			if isMCPToolName(definition.Name) {
				names = append(names, definition.Name)
			}
		}
		return names
	}
	for _, definition := range catalog {
		if isMCPToolName(definition.Name) && strings.Contains(request.ToolPrompt, definition.Name) {
			names = append(names, definition.Name)
		}
	}
	return names
}

func offersTool(request domain.ModelRequest, name string) bool {
	if definitionNamed(request.Tools, name) {
		return true
	}
	return strings.Contains(request.ToolPrompt, name)
}

func toolPayloadSize(request domain.ModelRequest) int {
	size := len([]rune(request.ToolPrompt))
	for _, definition := range request.Tools {
		encoded, _ := json.Marshal(definition)
		size += len([]rune(string(encoded)))
	}
	return size
}

func TestSmokeLargeMCPCatalogIsRetrievedBeforeItReachesTheModel(t *testing.T) {
	server := newLargeMCPServer(t, 60)
	runtime := newLargeToolRuntime(t, server)
	session := smokeSession(t)

	full, err := runtime.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if got := mcpToolCount(full); got != len(largeMCPTools)+60 {
		t.Fatalf("MCP catalog = %d tools, want %d", got, len(largeMCPTools)+60)
	}

	target := "mcp__mars-mes__plugin_mo__mo_query"
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(target, map[string]any{"keyword": "MO-A123"})},
		{Content: "製令 MO-A123 目前共 42 筆紀錄。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_retrieval", Session: session, UserInput: "查一下製令 MO-A123 的進度"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.requests) == 0 {
		t.Fatal("model was never called")
	}
	offered := offeredMCPTools(model.requests[0], full)
	if len(offered) > mcpRetrievalLimit {
		t.Fatalf("first request offered %d MCP tools, want at most %d", len(offered), mcpRetrievalLimit)
	}
	if !offersTool(model.requests[0], target) {
		t.Fatalf("retrieval missed the relevant tool; offered %v", offered)
	}
	if !offersTool(model.requests[0], findToolsToolName) {
		t.Fatalf("%s was not offered; offered %v", findToolsToolName, offered)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server was called %d times, want 1", server.calls.Load())
	}
	if result.Message.Content != "製令 MO-A123 目前共 42 筆紀錄。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}

	// 對照組：關掉檢索就是原本的行為，整份目錄進入請求。
	baselineSessions := newMemorySessions(smokeSession(t))
	baselineModel := &scriptedModel{responses: []domain.ModelResponse{{Content: "好的。"}}}
	baseline := smokeRunner(baselineSessions, baselineModel, runtime)
	baseline.SetToolRetrieval(false)
	if _, err := baseline.Run(context.Background(), Input{RunID: "run_baseline", Session: session, UserInput: "查一下製令 MO-A123 的進度"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("baseline Run: %v", err)
	}
	before := toolPayloadSize(baselineModel.requests[0])
	after := toolPayloadSize(model.requests[0])
	if after >= before/2 {
		t.Fatalf("retrieval did not shrink the tool payload: %d -> %d characters", before, after)
	}
	t.Logf("tool payload: %d -> %d characters (%d -> %d MCP tools)",
		before, after, len(offeredMCPTools(baselineModel.requests[0], full)), len(offered))
}

// 檢索一定會有失準的時候。這時模型必須能自己把工具找回來，而不是回答「做不到」。
func TestSmokeUnretrievedToolIsReachableThroughFindTools(t *testing.T) {
	server := newLargeMCPServer(t, 60)
	runtime := newLargeToolRuntime(t, server)
	session := smokeSession(t)

	target := "mcp__mars-mes__plugin_equipment__equipment_utilization"
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(findToolsToolName, map[string]any{"query": "稼動"})},
		{Content: instruction(target, map[string]any{"keyword": "ALL"})},
		{Content: "設備稼動資料共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_find", Session: session, UserInput: "幫我看一下產線的情況"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.requests) < 2 {
		t.Fatalf("model was called %d times, want at least 2", len(model.requests))
	}
	// 第一輪目錄不該已經有這個工具，否則這個測試沒有驗到 find_tools。
	if offersTool(model.requests[0], target) {
		t.Fatalf("target tool was already offered in the first request")
	}
	found := false
	for _, message := range model.requests[1].History {
		if strings.Contains(message.Content, target) {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s did not return the tool to the model: %+v", findToolsToolName, model.requests[1].History)
	}
	// 取回之後就進入目錄，模型下一輪看得到，也真的呼叫得到。
	if !offersTool(model.requests[1], target) {
		t.Fatalf("tool was not added to the catalog after discovery")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("MCP server was called %d times, want 1", server.calls.Load())
	}
	if value := server.lastTool.Load(); value == nil || *value != "plugin_equipment__equipment_utilization" {
		t.Fatalf("wrong remote tool was called: %v", value)
	}
	if result.Message.Content != "設備稼動資料共 42 筆。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
}

// 檢索失準時，模型如果已經知道工具名稱（來自 history 或使用者指名），
// 應該直接呼叫得到，而不是被目錄擋下來多花一輪。
func TestSmokeUnlistedMCPToolStaysCallable(t *testing.T) {
	server := newLargeMCPServer(t, 60)
	runtime := newLargeToolRuntime(t, server)
	session := smokeSession(t)

	target := "mcp__mars-mes__plugin_warehouse__warehouse_stock_query"
	sessions := newMemorySessions(session)
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(target, map[string]any{"keyword": "A100"})},
		{Content: "庫存共 42 筆。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	result, err := runner.Run(context.Background(), Input{RunID: "run_direct", Session: session, UserInput: "幫我看一下產線的情況"}, func(domain.EngineEvent) error { return nil })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if offersTool(model.requests[0], target) {
		t.Skip("retrieval already offered the tool; this case needs an unlisted tool")
	}
	if server.calls.Load() != 1 {
		t.Fatalf("unlisted MCP tool was not executed: %d calls", server.calls.Load())
	}
	if result.Message.Content != "庫存共 42 筆。" {
		t.Fatalf("final answer = %q", result.Message.Content)
	}
}

// 跟進提問常常只剩代名詞。檢索若只看目前這一句，第二題就會什麼都撈不到，
// 使用者要多等一輪讓模型自己去 find_tools。
func TestSmokeRetrievalUsesRecentTurnsForFollowUpQuestions(t *testing.T) {
	server := newLargeMCPServer(t, 60)
	runtime := newLargeToolRuntime(t, server)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	target := "mcp__mars-mes__plugin_department__department_list"
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(target, map[string]any{"keyword": "ALL"})},
		{Content: "共有 42 個部門。"},
		{Content: instruction(target, map[string]any{"keyword": "ALL"})},
		{Content: "重新查詢後仍是 42 個部門。"},
	}}
	runner := smokeRunner(sessions, model, runtime)

	if _, err := runner.Run(context.Background(), Input{RunID: "run_1", Session: session, UserInput: "公司有多少部門？"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	firstTurnCount := len(model.requests)

	if _, err := runner.Run(context.Background(), Input{RunID: "run_2", Session: session, UserInput: "再查一次"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(model.requests) <= firstTurnCount {
		t.Fatal("the second run never reached the model")
	}
	followUp := model.requests[firstTurnCount]
	if !offersTool(followUp, target) {
		t.Fatalf("the follow-up question lost the tool it needs; offered %v",
			offeredMCPTools(followUp, nil))
	}
}

// 收斂輪過去把完整目錄倒回提示裡，等於把前面省下來的 token 全部還回去。
func TestSmokeFinalizationCatalogStaysRetrieved(t *testing.T) {
	server := newLargeMCPServer(t, 60)
	runtime := newLargeToolRuntime(t, server)
	session := smokeSession(t)
	sessions := newMemorySessions(session)

	target := "mcp__mars-mes__plugin_mo__mo_query"
	model := &scriptedModel{responses: []domain.ModelResponse{
		{Content: instruction(target, map[string]any{"keyword": "MO-1"})},
		{Content: instruction(target, map[string]any{"keyword": "MO-2"})},
		{Content: instruction(target, map[string]any{"keyword": "MO-3"})},
		{Content: "製令查詢完成。"},
	}}
	runner := smokeRunner(sessions, model, runtime)
	// 收斂輪由自主工具回合上限觸發。
	runner.MaxAutonomousToolTurns = 2

	if _, err := runner.Run(context.Background(), Input{RunID: "run_final", Session: session, UserInput: "查一下製令進度"}, func(domain.EngineEvent) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := model.requests[len(model.requests)-1]
	if !strings.Contains(last.ToolPrompt, "Session 工具能力目錄") {
		t.Skip("the run did not reach a finalization turn")
	}
	unrelated := "mcp__mars-mes__plugin_report42__report42_query"
	if strings.Contains(last.ToolPrompt, unrelated) {
		t.Fatalf("the finalization catalog listed the whole MCP catalog (%d characters)", len([]rune(last.ToolPrompt)))
	}
	t.Logf("finalization tool prompt: %d characters", len([]rune(last.ToolPrompt)))
}
