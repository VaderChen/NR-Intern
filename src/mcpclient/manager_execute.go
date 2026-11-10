package mcpclient

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/schemaargs"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Manager) Execute(ctx context.Context, owner domain.Session, call domain.ToolCall, sink ports.ToolUpdateSink) (execution domain.ToolExecution, executeErr error) {
	dispatched := false
	resumedFrom := ""
	defer func() {
		if execution.Details == nil {
			execution.Details = map[string]any{}
		}
		if resumedFrom != "" {
			execution.Details["mcp_resumed_tool_call_id"] = resumedFrom
		}
		if domain.ToolExecutionState(execution.Details) == "" && execution.IsError {
			if dispatched {
				execution.Details["execution_state"] = domain.ToolOutcomeUnknown
			} else {
				execution.Details["execution_state"] = domain.ToolNotDispatched
			}
		}
	}()
	state, tool := m.route(call.Name)
	if state == nil {
		return failed(call, "MCP 工具不存在或尚未連線"), nil
	}
	if err := m.ensureConnected(ctx, state, false); err != nil {
		return failed(call, err.Error()), nil
	}
	state.mu.RLock()
	tool = state.tools[call.Name]
	session := state.session
	config := state.config
	state.mu.RUnlock()
	if session == nil || tool.remoteName == "" {
		return failed(call, "MCP 工具已不可用，請重新整理 MCP 連線"), nil
	}
	if call.ExpectedContractID != "" && call.ExpectedContractID != domain.ToolContractID(tool.definition) {
		return failed(call, "MCP 工具契約已變更，未派送；請重新取得工具定義與核准。"), nil
	}
	if _, supplied := call.Arguments[mcpRequestStateArgument]; supplied {
		return failed(call, "MCP request_state 由 Runtime 管理，請使用 _mcp_continuation_id 續接。"), nil
	}
	resumed, parentCallID, resumeErr := m.resumeToolCall(owner.ID, config.ID, call, tool)
	if resumeErr != nil {
		return failed(call, resumeErr.Error()), nil
	}
	call = resumed
	resumedFrom = parentCallID

	// CallTimeoutSeconds 是「沒有任何回應或進度更新」的容忍時間，不是硬性的
	// 總時長上限：長時間執行的 MCP 工作只要持續回報進度就能繼續，整體時間仍由
	// Run 的 wall-clock 預算控制。沒有進度回報的工具行為與原本一致。
	inactivity := time.Duration(config.CallTimeoutSeconds) * time.Second
	// 不直接把 timer callback 綁在 SDK 呼叫上：部分 MCP transport 在取消後
	// 仍可能卡在讀取或關閉流程，Manager 必須自己保證 Execute 能返回。
	ceiling := time.Duration(mcpMaxCallMultiplier) * inactivity
	if ceiling > mcpMaxCallDuration {
		ceiling = mcpMaxCallDuration
	}
	progressResets := make(chan struct{}, 1)
	progressCount := &atomic.Int64{}
	m.progressMu.Lock()
	m.progress[call.ID] = progressListener{sink: sink, extend: func() {
		progressCount.Add(1)
		select {
		case progressResets <- struct{}{}:
		default:
		}
	}}
	m.progressMu.Unlock()
	defer func() {
		m.progressMu.Lock()
		delete(m.progress, call.ID)
		m.progressMu.Unlock()
	}()
	startedAt := time.Now()
	stopHeartbeat := m.startWaitHeartbeat(ctx, call, config, sink, startedAt, progressCount)
	defer stopHeartbeat()
	for attempt := 1; attempt <= MCPCallRetryAttempts; attempt++ {
		if m.state(config.ID) != state {
			return failed(call, "MCP 設定來源已替換，已停止派送；請重新取得工具目錄。"), nil
		}
		params, paramsErr := m.callToolParams(call, tool)
		if paramsErr != nil {
			return failed(call, fmt.Sprintf("MCP %s 輸入無效：%v", config.DisplayName, paramsErr)), nil
		}
		dispatched = true
		result, err, outcome := callToolWithTimeout(ctx, inactivity, ceiling, progressResets, func(callCtx context.Context) (*mcp.CallToolResult, error) {
			return session.CallTool(callCtx, params)
		})
		if err == nil {
			state.mu.Lock()
			state.lastUsedAt = time.Now()
			state.mu.Unlock()
			return m.executionFromResult(call, config, result, owner.ID), nil
		}
		// SDK 明確指出派送前已關閉，或本機 dial 失敗，才視為未派送。
		// 不以一般錯誤文字中的 session 失效推定遠端沒有執行。
		// 其他錯誤有可能是「已執行、但回應遺失」，維持只有 idempotent 工具才重試。
		if outcome == mcpCallStalled {
			message := fmt.Sprintf("MCP %s 呼叫已中止：%d 秒內沒有回應或進度更新（已等待 %s）",
				config.DisplayName, config.CallTimeoutSeconds, time.Since(startedAt).Round(time.Second))
			m.disconnectWithError(state, errors.New(message))
			return failed(call, message), nil
		}
		if outcome == mcpCallExceeded {
			message := fmt.Sprintf("MCP %s 呼叫已中止：單次呼叫超過上限 %s，期間收到 %d 次進度更新。請縮小工作範圍，或調整這個 Server 的呼叫逾時設定",
				config.DisplayName, ceiling.Round(time.Second), progressCount.Load())
			m.disconnectWithError(state, errors.New(message))
			return failed(call, message), nil
		}
		if ctx.Err() != nil {
			// Run 被取消時不拆掉健康的 MCP session，避免下一個 Run 又要重新
			// initialize；這次呼叫的 context 已由 callToolWithTimeout 收束。
			return failed(call, fmt.Sprintf("MCP %s 呼叫已取消", config.DisplayName)), nil
		}
		rejected := deliveryRejectedMCPError(err)
		if attempt == MCPCallRetryAttempts || (!rejected && (!tool.idempotent || !retryableMCPCallError(ctx, err))) {
			m.disconnectWithError(state, err)
			return failed(call, fmt.Sprintf("MCP %s 呼叫失敗：%v", config.DisplayName, err)), nil
		}

		m.logger.Warn("reconnecting and retrying MCP tool call",
			"server_id", config.ID,
			"tool_name", call.Name,
			"attempt", attempt+1,
			"max_attempts", MCPCallRetryAttempts,
			"server_rejected_delivery", rejected,
			"idempotent", tool.idempotent,
			"error", err,
		)
		m.disconnectWithError(state, err)
		if reconnectErr := m.ensureConnected(ctx, state, true); reconnectErr != nil {
			return failed(call, fmt.Sprintf("MCP %s 連線中斷且重新連線失敗：%v", config.DisplayName, reconnectErr)), nil
		}
		state.mu.RLock()
		tool = state.tools[call.Name]
		session = state.session
		state.mu.RUnlock()
		if session == nil || tool.remoteName == "" {
			return failed(call, "MCP 工具在重新連線後已不存在，請重新整理工具清單"), nil
		}
		if call.ExpectedContractID != "" && call.ExpectedContractID != domain.ToolContractID(tool.definition) {
			return failed(call, "MCP 重連後契約已變更，已停止自動重送；請確認先前結果並重新核准。"), nil
		}
	}
	return failed(call, fmt.Sprintf("MCP %s 呼叫失敗", config.DisplayName)), nil
}

const (
	mcpInputResponsesArgument = "_mcp_input_responses"
	mcpRequestStateArgument   = "_mcp_request_state"
	mcpContinuationArgument   = "_mcp_continuation_id"
)

// callToolParams 將 MCP 多輪輸入的控制欄位與實際工具參數分開。一般工具
// 不會看到底線開頭的控制欄位；模型只需在收到 input_required 結果後，沿用
// 同一工具補上這兩個欄位即可繼續完成 MCP 呼叫。
// startWaitHeartbeat 在等待 MCP 回應期間定期回報已等待時間與收到的進度次數。
//
// 遠端工具沒有回應時，畫面上原本只有一個沒有說明的轉圈圈；把等待對象與秒數寫進
// Run 的事件流之後，卡住時可以直接看出是哪一個 MCP、等了多久、有沒有進度。
func (m *Manager) startWaitHeartbeat(
	ctx context.Context,
	call domain.ToolCall,
	config ServerConfig,
	sink ports.ToolUpdateSink,
	startedAt time.Time,
	progressCount *atomic.Int64,
) func() {
	if sink == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(mcpWaitHeartbeatIntervalForTest)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(startedAt).Round(time.Second)
				_ = sink(domain.ToolExecution{
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    fmt.Sprintf("等待 MCP %s 回應中（已 %s）", config.DisplayName, elapsed),
					Details: map[string]any{
						"phase":            "mcp_waiting",
						"mcp_server_id":    config.ID,
						"elapsed_seconds":  int(elapsed.Seconds()),
						"progress_updates": progressCount.Load(),
					},
				})
			}
		}
	}()
	return func() { close(done) }
}

func (m *Manager) callToolParams(call domain.ToolCall, tool resolvedTool) (*mcp.CallToolParams, error) {
	arguments := make(map[string]any, len(call.Arguments))
	for key, value := range call.Arguments {
		if key == mcpInputResponsesArgument || key == mcpRequestStateArgument || key == mcpContinuationArgument {
			continue
		}
		arguments[key] = value
	}
	// 小型模型常把巢狀參數整個 JSON 字串化，遠端 Server 會直接以 schema 驗證失敗。
	// 依工具自己宣告的 schema 把該是 array／object 的字串解回結構；解不開就原樣送出。
	arguments = schemaargs.Normalize(arguments, tool.definition.InputSchema)
	params := &mcp.CallToolParams{Name: tool.remoteName, Arguments: arguments}
	if value, exists := call.Arguments[mcpInputResponsesArgument]; exists {
		responses, err := decodeMCPInputResponses(value)
		if err != nil {
			return nil, err
		}
		params.InputResponses = responses
	}
	if value, exists := call.Arguments[mcpRequestStateArgument]; exists {
		requestState, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s 必須是字串", mcpRequestStateArgument)
		}
		params.RequestState = requestState
	}
	params.SetProgressToken(call.ID)
	return params, nil
}

func decodeMCPInputResponses(value any) (mcp.InputResponseMap, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s 無法編碼：%v", mcpInputResponsesArgument, err)
	}
	var rawValues map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &rawValues); err != nil || rawValues == nil {
		return nil, fmt.Errorf("%s 必須是 JSON object", mcpInputResponsesArgument)
	}
	responses := make(mcp.InputResponseMap, len(rawValues))
	for id, raw := range rawValues {
		var probe struct {
			Action json.RawMessage `json:"action"`
			Role   json.RawMessage `json:"role"`
			Roots  json.RawMessage `json:"roots"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("%s[%q] 必須是 JSON object：%v", mcpInputResponsesArgument, id, err)
		}
		switch {
		case len(probe.Action) > 0:
			var response mcp.ElicitResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode elicitation response %q: %v", id, err)
			}
			responses[id] = &response
		case len(probe.Role) > 0:
			var response mcp.CreateMessageResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode sampling response %q: %v", id, err)
			}
			responses[id] = &response
		case len(probe.Roots) > 0:
			var response mcp.ListRootsResult
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, fmt.Errorf("decode roots response %q: %v", id, err)
			}
			responses[id] = &response
		default:
			return nil, fmt.Errorf("%s[%q] 缺少 action、role 或 roots", mcpInputResponsesArgument, id)
		}
	}
	return responses, nil
}

func retryableMCPCallError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"broken pipe", "connection reset", "connection refused", "connection aborted",
		"unexpected eof", "eof", "use of closed network connection", "transport is closing",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// deliveryRejectedMCPError 判斷「伺服器沒有收下這次呼叫」的錯誤。
//
// MCP session 過期或伺服器重啟後，Streamable HTTP 會以 session not found 拒絕
// 請求；連線根本沒建立起來也一樣。這兩種情況下遠端不可能執行過工具，因此重送
// 是安全的，不受 idempotent 宣告限制。
func deliveryRejectedMCPError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	// 遠端內部錯誤也可能含 connection refused／session expired 等字樣，
	// 不能據此推定這次工具尚未執行。只接受 SDK 的未派送錯誤或本機 dial 失敗。
	var operation *net.OpError
	return errors.As(err, &operation) && operation.Op == "dial"
}

func isUnsupportedMCPMethod(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"method not found", "method not implemented", "unknown method", "not supported"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) handleProgress(_ context.Context, request *mcp.ProgressNotificationClientRequest) {
	if request == nil || request.Params == nil {
		return
	}
	token := fmt.Sprint(request.Params.ProgressToken)
	m.progressMu.RLock()
	listener := m.progress[token]
	m.progressMu.RUnlock()
	if listener.extend != nil {
		listener.extend()
	}
	sink := listener.sink
	if sink == nil {
		return
	}
	_ = sink(domain.ToolExecution{Content: request.Params.Message, Details: map[string]any{
		"mcp_progress": request.Params.Progress,
		"mcp_total":    request.Params.Total,
	}})
}

type mcpCallResult struct {
	result *mcp.CallToolResult
	err    error
}

// mcpWaitHeartbeatIntervalForTest 讓測試縮短等待回報間隔；正式執行時等於
// mcpWaitHeartbeatInterval。
var mcpWaitHeartbeatIntervalForTest = mcpWaitHeartbeatInterval

type mcpCallOutcome int

const (
	mcpCallCompleted mcpCallOutcome = iota
	// mcpCallStalled 是「閒置視窗內完全沒有回應或進度」。
	mcpCallStalled
	// mcpCallExceeded 是「有進度但總時間超過單次呼叫上限」。
	mcpCallExceeded
	mcpCallCanceled
)

// callToolWithTimeout 將底層 MCP 呼叫與等待邏輯分開。SDK 通常會遵守 context，
// 但第三方 transport 或自訂 RoundTripper 可能在取消後仍不返回；用獨立 goroutine
// 與有緩衝的結果 channel，才能讓上層 Run 在逾時時立即收到失敗結果。
func callToolWithTimeout(
	parent context.Context,
	inactivity time.Duration,
	ceiling time.Duration,
	progressResets <-chan struct{},
	invoke func(context.Context) (*mcp.CallToolResult, error),
) (*mcp.CallToolResult, error, mcpCallOutcome) {
	callCtx, cancel := context.WithCancel(parent)
	defer cancel()

	resultCh := make(chan mcpCallResult, 1)
	go func() {
		result, err := invoke(callCtx)
		resultCh <- mcpCallResult{result: result, err: err}
	}()

	timer := time.NewTimer(inactivity)
	defer stopTimer(timer)
	// limit 是不受進度通知影響的絕對上限，確保單次呼叫一定會結束。
	limit := time.NewTimer(ceiling)
	defer stopTimer(limit)
	for {
		select {
		case response := <-resultCh:
			return response.result, response.err, mcpCallCompleted
		case <-timer.C:
			// 先取消底層呼叫，讓遵守 context 的實作儘快收束；不等待它，
			// 因為不遵守取消的實作正是這個保護邊界要處理的對象。
			cancel()
			return nil, context.DeadlineExceeded, mcpCallStalled
		case <-limit.C:
			cancel()
			return nil, context.DeadlineExceeded, mcpCallExceeded
		case <-parent.Done():
			cancel()
			return nil, parent.Err(), mcpCallCanceled
		case <-progressResets:
			stopTimer(timer)
			timer.Reset(inactivity)
		}
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
