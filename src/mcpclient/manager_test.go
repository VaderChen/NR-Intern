package mcpclient

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

type testServer struct {
	http        *httptest.Server
	authHeaders atomic.Pointer[string]
	calls       atomic.Int64
}

func newTestMCPServer(t *testing.T, authorize func(*http.Request) bool) *testServer {
	t.Helper()
	harness := &testServer{}
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo back"},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			harness.calls.Add(1)
			return nil, echoOutput{Text: input.Text}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	harness.http = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := request.Header.Get("Authorization")
		harness.authHeaders.Store(&value)
		if authorize != nil && !authorize(request) {
			writer.Header().Set("WWW-Authenticate", `Basic realm="mcp"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(harness.http.Close)
	return harness
}

func newTestManager(t *testing.T, config ServerConfig) *Manager {
	t.Helper()
	manager, err := New([]ServerConfig{config}, "nr-intern-test", "test", 64*1024, logging.Discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestBasicAuthCredentialsReachTheServer(t *testing.T) {
	server := newTestMCPServer(t, func(request *http.Request) bool {
		username, password, ok := request.BasicAuth()
		return ok && username == "fixture-user" && password == "fixture-password"
	})
	manager := newTestManager(t, ServerConfig{
		ID: "mars", Enabled: true, Transport: TransportStreamableHTTP,
		URL: server.http.URL, Username: "fixture-user", Password: "fixture-password",
	})

	definitions, err := manager.Definitions(context.Background(), domain.Session{ID: "session_1"})
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("definitions = %d, want 1 (statuses: %+v)", len(definitions), manager.Statuses())
	}
	result, err := manager.Execute(context.Background(), domain.Session{ID: "session_1"},
		domain.ToolCall{ID: "call_1", Name: definitions[0].Name, Arguments: map[string]any{"text": "hello"}}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "hello") {
		t.Fatalf("call failed: %+v", result)
	}
}

func TestBearerCredentialsReachTheServer(t *testing.T) {
	server := newTestMCPServer(t, func(request *http.Request) bool {
		return request.Header.Get("Authorization") == "Bearer token-123"
	})
	manager := newTestManager(t, ServerConfig{
		ID: "remote", Enabled: true, Transport: TransportStreamableHTTP,
		URL: server.http.URL, APIKey: "token-123",
	})

	definitions, err := manager.Definitions(context.Background(), domain.Session{ID: "session_1"})
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("definitions = %d, want 1 (statuses: %+v)", len(definitions), manager.Statuses())
	}
}

func TestStreamableHTTPDoesNotRequireSubscriptionsListen(t *testing.T) {
	var subscriptionCalls atomic.Int64
	server := mcp.NewServer(&mcp.Implementation{Name: "compat-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo back"},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			return nil, echoOutput{Text: input.Text}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(writer, "cannot read request", http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			var message struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(body, &message) == nil && message.Method == "subscriptions/listen" {
				subscriptionCalls.Add(1)
				http.Error(writer, "subscriptions/listen is not supported", http.StatusNotFound)
				return
			}
		}
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(httpServer.Close)
	manager := newTestManager(t, ServerConfig{
		ID: "compat", Enabled: true, Transport: TransportStreamableHTTP, URL: httpServer.URL,
	})

	definitions, err := manager.Definitions(context.Background(), domain.Session{ID: "session_1"})
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 1 || definitions[0].Name != "mcp__compat__echo" {
		t.Fatalf("definitions = %+v, want one compatible MCP tool", definitions)
	}
	if calls := subscriptionCalls.Load(); calls != 0 {
		t.Fatalf("subscriptions/listen calls = %d, want 0", calls)
	}
}

func TestDefinitionsRetriesAfterToolCatalogRefreshFailure(t *testing.T) {
	type flakyCatalog struct {
		remainingFailures atomic.Int64
		listCalls         atomic.Int64
	}
	catalog := &flakyCatalog{}
	catalog.remainingFailures.Store(1)
	server := mcp.NewServer(&mcp.Implementation{Name: "flaky-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo back"},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			return nil, echoOutput{Text: input.Text}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(writer, "cannot read request", http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			var message struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(body, &message) == nil && message.Method == "tools/list" {
				catalog.listCalls.Add(1)
				if catalog.remainingFailures.Add(-1) >= 0 {
					http.Error(writer, "temporary tools/list failure", http.StatusServiceUnavailable)
					return
				}
			}
		}
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(httpServer.Close)
	manager := newTestManager(t, ServerConfig{
		ID: "flaky", Enabled: true, Transport: TransportStreamableHTTP, URL: httpServer.URL,
	})
	session := domain.Session{ID: "session_1"}

	first, err := manager.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("first Definitions: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("first definitions = %d, want empty after refresh failure", len(first))
	}

	second, err := manager.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("second Definitions: %v", err)
	}
	if len(second) != 1 || second[0].Name != "mcp__flaky__echo" {
		t.Fatalf("second definitions = %+v, want recovered MCP tool (statuses: %+v, list calls: %d, remaining failures: %d)", second, manager.Statuses(), catalog.listCalls.Load(), catalog.remainingFailures.Load())
	}
	if catalog.listCalls.Load() < 2 {
		t.Fatalf("tools/list calls = %d, want initial failed refresh and a retry", catalog.listCalls.Load())
	}
}

// restartableServer 以同一個 URL 換掉背後的 MCP Server，模擬遠端服務重啟後
// 舊 session id 失效的情況。
type restartableServer struct {
	http    *httptest.Server
	mu      sync.Mutex
	handler http.Handler
	calls   atomic.Int64
}

func newRestartableServer(t *testing.T) *restartableServer {
	t.Helper()
	harness := &restartableServer{}
	harness.restart()
	harness.http = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		harness.mu.Lock()
		handler := harness.handler
		harness.mu.Unlock()
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(harness.http.Close)
	return harness
}

func (s *restartableServer) restart() {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo back"},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			s.calls.Add(1)
			return nil, echoOutput{Text: input.Text}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
}

func TestCallRecoversAfterServerRestart(t *testing.T) {
	server := newRestartableServer(t)
	manager := newTestManager(t, ServerConfig{
		ID: "mars", Enabled: true, Transport: TransportStreamableHTTP, URL: server.http.URL,
	})
	session := domain.Session{ID: "session_1"}
	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Definitions: %v (%d)", err, len(definitions))
	}
	toolName := definitions[0].Name

	server.restart()

	result, err := manager.Execute(context.Background(), session,
		domain.ToolCall{ID: "call_1", Name: toolName, Arguments: map[string]any{"text": "hello"}}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("call after server restart failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Fatalf("unexpected content: %s", result.Content)
	}
}

func TestConnectReportsRejectedCredentials(t *testing.T) {
	server := newTestMCPServer(t, func(request *http.Request) bool {
		return request.Header.Get("Authorization") == "Bearer correct"
	})
	manager := newTestManager(t, ServerConfig{
		ID: "remote", Enabled: true, Transport: TransportStreamableHTTP, URL: server.http.URL, APIKey: "wrong",
	})

	_, _ = manager.Definitions(context.Background(), domain.Session{ID: "session_1"})
	statuses := manager.Statuses()
	t.Logf("status after rejected credentials: %+v", statuses)
}

func newSlowMCPServer(t *testing.T, step time.Duration, steps int, reportProgress bool) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "slow-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "long_job", Description: "long running job"},
		func(ctx context.Context, request *mcp.CallToolRequest, _ echoInput) (*mcp.CallToolResult, echoOutput, error) {
			token := request.Params.GetProgressToken()
			for index := 0; index < steps; index++ {
				select {
				case <-ctx.Done():
					return nil, echoOutput{}, ctx.Err()
				case <-time.After(step):
				}
				if reportProgress && token != nil {
					_ = request.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
						ProgressToken: token, Message: "working", Progress: float64(index + 1), Total: float64(steps),
					})
				}
			}
			return nil, echoOutput{Text: "done"}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	value := httptest.NewServer(handler)
	t.Cleanup(value.Close)
	return value
}

// call_timeout_seconds 是「沒有回應或進度更新」的容忍時間：MCP Server 持續回報
// 進度時，長時間工作必須能跑完，而不是在固定秒數被硬砍。
func TestLongRunningCallSurvivesWhileProgressArrives(t *testing.T) {
	server := newSlowMCPServer(t, 400*time.Millisecond, 4, true)
	manager := newTestManager(t, ServerConfig{
		ID: "slow", Enabled: true, Transport: TransportStreamableHTTP, URL: server.URL, CallTimeoutSeconds: 1,
	})
	session := domain.Session{ID: "session_1"}
	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Definitions: %v (%d)", err, len(definitions))
	}

	updates := 0
	result, err := manager.Execute(context.Background(), session,
		domain.ToolCall{ID: "call_1", Name: definitions[0].Name, Arguments: map[string]any{"text": "x"}},
		func(domain.ToolExecution) error { updates++; return nil })
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("long running call failed: %s", result.Content)
	}
	if updates == 0 {
		t.Fatal("progress updates were not forwarded to the sink")
	}
}

func TestStalledCallReportsInactivity(t *testing.T) {
	server := newSlowMCPServer(t, 2*time.Second, 1, false)
	manager := newTestManager(t, ServerConfig{
		ID: "stalled", Enabled: true, Transport: TransportStreamableHTTP, URL: server.URL, CallTimeoutSeconds: 1,
	})
	session := domain.Session{ID: "session_1"}
	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Definitions: %v (%d)", err, len(definitions))
	}

	result, err := manager.Execute(context.Background(), session,
		domain.ToolCall{ID: "call_1", Name: definitions[0].Name, Arguments: map[string]any{"text": "x"}}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "沒有回應或進度更新") {
		t.Fatalf("stalled call was not reported clearly: %+v", result)
	}
}

// 即使底層 transport 不理會 context，Manager 仍必須在逾時邊界返回，
// 否則單一 MCP Server 就能把整個 Run 永久卡住。
func TestCallToolTimeoutReturnsWhenInvokerIgnoresCancellation(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	goResult := func(context.Context) (*mcp.CallToolResult, error) {
		close(started)
		<-release
		close(finished)
		return nil, nil
	}

	begin := time.Now()
	type timeoutResult struct {
		err     error
		outcome mcpCallOutcome
	}
	resultCh := make(chan timeoutResult, 1)
	go func() {
		_, err, outcome := callToolWithTimeout(context.Background(), 30*time.Millisecond, time.Second, nil, goResult)
		resultCh <- timeoutResult{err: err, outcome: outcome}
	}()
	<-started
	result := <-resultCh
	err, outcome := result.err, result.outcome
	if outcome != mcpCallStalled || err != context.DeadlineExceeded {
		t.Fatalf("timeout result = (%v, %v), want deadline exceeded", err, outcome)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("timeout took %s; Manager waited for the ignored cancellation", elapsed)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("test invoker did not finish after release")
	}
}

func TestAuthModeReportsWhatIsSent(t *testing.T) {
	cases := map[string]struct {
		config ServerConfig
		want   string
	}{
		"bearer":  {ServerConfig{Transport: TransportStreamableHTTP, APIKey: "k"}, AuthModeBearer},
		"basic":   {ServerConfig{Transport: TransportStreamableHTTP, Username: "u", Password: "p"}, AuthModeBasic},
		"headers": {ServerConfig{Transport: TransportStreamableHTTP, Headers: map[string]string{"X-API-Key": "k"}}, AuthModeHeaders},
		"none":    {ServerConfig{Transport: TransportStreamableHTTP}, AuthModeNone},
		"stdio":   {ServerConfig{Transport: TransportStdio, APIKey: "ignored"}, AuthModeNone},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := test.config.AuthMode(); got != test.want {
				t.Fatalf("AuthMode = %q, want %q", got, test.want)
			}
		})
	}
}

// 只填主機、少了 /mcp 之類的端點路徑時，錯誤訊息必須指向網址而不是憑證，
// 否則使用者會一直去檢查金鑰。
func TestConnectExplainsMissingEndpointPath(t *testing.T) {
	value := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "not found", http.StatusNotFound)
	}))
	defer value.Close()
	manager := newTestManager(t, ServerConfig{
		ID: "mars", Enabled: true, Transport: TransportStreamableHTTP, URL: value.URL, Username: "fixture-user", Password: "fixture-password",
	})

	_, _ = manager.Definitions(context.Background(), domain.Session{ID: "session_1"})

	statuses := manager.Statuses()
	if len(statuses) != 1 || !strings.Contains(statuses[0].Error, "端點路徑") {
		t.Fatalf("missing endpoint path was not explained: %+v", statuses)
	}
	if statuses[0].AuthMode != AuthModeBasic {
		t.Fatalf("auth mode = %q", statuses[0].AuthMode)
	}
}

// 進度通知可以延長閒置視窗，但不能讓一次呼叫無限期執行；否則畫面上就是一個
// 永遠轉不完的圈，使用者只能自己重開。
func TestCallStopsAtTheAbsoluteCeilingDespiteProgress(t *testing.T) {
	resets := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case resets <- struct{}{}:
				case <-stop:
					return
				}
			}
		}
	}()

	begin := time.Now()
	_, err, outcome := callToolWithTimeout(context.Background(), 40*time.Millisecond, 150*time.Millisecond, resets,
		func(ctx context.Context) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})

	if outcome != mcpCallExceeded || err != context.DeadlineExceeded {
		t.Fatalf("outcome = (%v, %v), want the absolute ceiling to fire", err, outcome)
	}
	if elapsed := time.Since(begin); elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("ceiling fired after %s", elapsed)
	}
}

// 等待期間必須持續回報，否則使用者看不出是哪一個 MCP 在等、等了多久。
func TestWaitHeartbeatReportsElapsedTime(t *testing.T) {
	server := newSlowMCPServer(t, 400*time.Millisecond, 1, false)
	manager := newTestManager(t, ServerConfig{
		ID: "slow", Enabled: true, Transport: TransportStreamableHTTP, URL: server.URL, CallTimeoutSeconds: 5,
	})
	session := domain.Session{ID: "session_1"}
	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Definitions: %v (%d)", err, len(definitions))
	}

	updates := make(chan domain.ToolExecution, 8)
	original := mcpWaitHeartbeatIntervalForTest
	mcpWaitHeartbeatIntervalForTest = 50 * time.Millisecond
	defer func() { mcpWaitHeartbeatIntervalForTest = original }()

	if _, err := manager.Execute(context.Background(), session,
		domain.ToolCall{ID: "call_1", Name: definitions[0].Name, Arguments: map[string]any{"text": "x"}},
		func(update domain.ToolExecution) error {
			select {
			case updates <- update:
			default:
			}
			return nil
		}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	close(updates)
	for update := range updates {
		if update.Details["phase"] == "mcp_waiting" {
			if update.Details["elapsed_seconds"] == nil || !strings.Contains(update.Content, "等待 MCP") {
				t.Fatalf("heartbeat update is not usable: %+v", update)
			}
			return
		}
	}
	t.Fatal("no waiting heartbeat was emitted")
}

// 宣告 output schema 的 Server 會同時回傳 structuredContent 與內容相同的 text block。
// 兩份都收進工具結果，等於同一筆資料在 transcript 裡存兩次，之後每一輪都重讀兩次。
func TestStructuredResultIsNotDuplicated(t *testing.T) {
	server := newTestMCPServer(t, nil)
	manager := newTestManager(t, ServerConfig{
		ID: "mars", Enabled: true, Transport: TransportStreamableHTTP, URL: server.http.URL,
	})
	session := domain.Session{ID: "session_1"}
	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Definitions: %v (%d)", err, len(definitions))
	}

	result, err := manager.Execute(context.Background(), session,
		domain.ToolCall{ID: "call_1", Name: definitions[0].Name, Arguments: map[string]any{"text": "hello"}}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if count := strings.Count(result.Content, `"text"`); count != 1 {
		t.Fatalf("payload appears %d times in the tool result: %s", count, result.Content)
	}
	if result.Details["structured_content"] == nil {
		t.Fatal("structured content must still be available as metadata")
	}
}

// 只有 structuredContent、沒有對應文字區塊時，內容仍必須送到模型手上。
func TestStructuredOnlyResultIsStillReported(t *testing.T) {
	parts := []string{"其他說明"}
	if jsonAlreadyPresent(parts, []byte(`{"total":264}`)) {
		t.Fatal("unrelated text must not count as the structured payload")
	}
	if !jsonAlreadyPresent([]string{`{"total": 264}`}, []byte(`{"total":264}`)) {
		t.Fatal("the same JSON with different spacing must be detected as a duplicate")
	}
}

// 慢的 MCP Server ping 逾時是常態，不能因此拆掉可用連線：重建連線要重跑
// initialize 與 tools/list，只會讓每次呼叫更慢。
func TestSlowProbeKeepsTheSession(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	server := newTestMCPServer(t, nil)
	manager := newTestManager(t, ServerConfig{
		ID: "slow-probe", Enabled: true, Transport: TransportStreamableHTTP, URL: server.http.URL,
	})
	session := domain.Session{ID: "session_1"}
	if _, err := manager.Definitions(context.Background(), session); err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	state := manager.state("slow-probe")
	if state == nil || state.session == nil {
		t.Fatal("server did not connect")
	}
	before := state.session

	// 讓下一次使用一定會觸發 probe。
	state.mu.Lock()
	state.lastUsedAt = time.Now().Add(-time.Hour)
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if !manager.sessionUsable(ctx, state, before) {
		t.Fatal("a probe that could not complete must not tear down the session")
	}
	state.mu.RLock()
	current := state.session
	state.mu.RUnlock()
	if current != before {
		t.Fatal("session was replaced after a slow probe")
	}
}

// 外掛型 MCP Server 動輒數十上百個工具，定義每次請求都會整份送出。
// 只公開勾選的工具，其餘不進工具目錄，但仍要留在可選清單裡供管理介面挑選。
func TestEnabledToolsLimitsTheExposedCatalog(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "plugins", Version: "1.0.0"}, nil)
	for _, name := range []string{"alpha_query", "beta_query", "gamma_query"} {
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: name},
			func(context.Context, *mcp.CallToolRequest, echoInput) (*mcp.CallToolResult, echoOutput, error) {
				return nil, echoOutput{Text: "ok"}, nil
			})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	value := httptest.NewServer(handler)
	defer value.Close()

	manager := newTestManager(t, ServerConfig{
		ID: "plugins", Enabled: true, Transport: TransportStreamableHTTP, URL: value.URL,
		EnabledTools: []string{"beta_query"},
	})
	session := domain.Session{ID: "session_1"}

	definitions, err := manager.Definitions(context.Background(), session)
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(definitions) != 1 || !strings.HasSuffix(definitions[0].Name, "beta_query") {
		t.Fatalf("exposed tools = %+v, want only beta_query", definitions)
	}
	statuses := manager.Statuses()
	if len(statuses) != 1 || len(statuses[0].AvailableTools) != 3 {
		t.Fatalf("available tools = %+v, want all three for the settings UI", statuses)
	}
	if statuses[0].ToolCount != 1 {
		t.Fatalf("tool count = %d, want 1", statuses[0].ToolCount)
	}
}

func TestEmptyEnabledToolsExposesEverything(t *testing.T) {
	config := ServerConfig{Transport: TransportStreamableHTTP}
	if !config.ToolEnabled("anything") {
		t.Fatal("an empty list must expose every tool")
	}
	config.EnabledTools = []string{"alpha"}
	if config.ToolEnabled("beta") || !config.ToolEnabled("alpha") {
		t.Fatal("only listed tools may be exposed")
	}
}
