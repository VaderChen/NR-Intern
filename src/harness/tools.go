package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxParallelTools 限制單一群組同時執行的工具數量，避免模型一次要求大量讀取時
// 產生無上限的 goroutine 與檔案描述子壓力。
const maxParallelTools = 8

const (
	systemShellToolName = "shell_exec"
	// askUserToolName 用字串而不是 import：harness 不依賴具體的工具實作，
	// 與 systemShellToolName 同一個理由。
	askUserToolName          = "ask_user"
	waitToolName             = "wait_for"
	sshWaitToolName          = "ssh_wait"
	toolStageSystemShell     = "system_shell"
	toolStageBuiltinFallback = "builtin_fallback"
)

type toolOutcome struct {
	call      domain.ToolCall
	result    domain.ToolExecution
	startedAt time.Time
	duration  time.Duration
	err       error
}

type runApprovalState struct {
	permanent atomic.Bool
}

func newRunApprovalState(permanent bool) *runApprovalState {
	state := &runApprovalState{}
	state.permanent.Store(permanent)
	return state
}

func (s *runApprovalState) approved() bool {
	return s != nil && s.permanent.Load()
}

func (s *runApprovalState) approvePermanently() {
	if s != nil {
		s.permanent.Store(true)
	}
}

// serializedSink 保護事件輸出。並行執行時多個 goroutine 會同時送出
// tool.execution.update，而下游的 durable event log 以遞增 sequence 寫入，
// 不可併發呼叫。
type serializedSink struct {
	mu   sync.Mutex
	emit EventSink
}

func (s *serializedSink) emitEvent(eventType string, payload map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return emitEvent(s.emit, eventType, payload)
}

// parallelizableToolNames 是可以放進同一個並行群組的工具。
//
// 條件是 read-only **而且**不需要人工核准。第二個條件不是保守而已：
// ApprovalCoordinator 每個 run 只允許一筆 pending approval，同一群組內兩個
// 需要核准的工具會讓第二個 Begin 得到 ErrConflict，進而讓整個 run 失敗。
// 目前需要核准的集合（RequiresPermission）與 read-only 集合剛好不相交，
// 但那是巧合而非保證——核准清單一旦可設定就會相交，所以在這裡明確排除。
func parallelizableToolNames(definitions []domain.ToolDefinition, approvals ports.ApprovalCoordinator) map[string]bool {
	result := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if !definition.ReadOnly {
			continue
		}
		if approvals != nil && approvals.Required(definition.Name) {
			continue
		}
		// 問答選單同理：使用者一次只看得到一個對話框。兩個並行的問題會有一個
		// 完全沒有機會被回答，最後靜靜地逾時。
		if definition.Name == askUserToolName {
			continue
		}
		result[definition.Name] = true
	}
	return result
}

// approvalExemptToolNames 回傳本輪不需要人工核准的工具。
//
// 唯讀工具沒有副作用，每次呼叫都要人按一次核准只是把使用者訓練成無條件點「同意」，
// 反而讓真正有副作用的操作更容易被順手放行。MCP 工具的唯讀屬性來自 Server 自己宣告的
// readOnlyHint，只有在管理者對該 Server 開啟 trust_annotations 後才會被採信；沒有開啟時
// 這些工具不算唯讀，仍然逐次核准。
// ephemeralProjectSession 判斷這個 Session 是否屬於記憶體隔離專案。
//
// 旗標由 application 在組裝 Run metadata 時寫入，與 sandbox_roots 同屬後端保留欄位；
// Client 夾帶的同名值會先被清掉，因此這裡讀到的一定是後端自己判定的結果。
func ephemeralProjectSession(session domain.Session) bool {
	if session.Metadata == nil {
		return false
	}
	value, _ := session.Metadata["ephemeral_project"].(bool)
	return value
}

func approvalExemptToolNames(definitions []domain.ToolDefinition, sessions ...domain.Session) map[string]bool {
	result := make(map[string]bool, len(definitions))
	isolatedWorkspace := false
	if len(sessions) > 0 && ephemeralProjectSession(sessions[0]) {
		// 額外掛入排程等持久目錄時，不能再以 RAM 工作區為由免審核。
		switch roots := sessions[0].Metadata["sandbox_roots"].(type) {
		case []string:
			isolatedWorkspace = len(roots) == 1 && strings.TrimSpace(roots[0]) != ""
		case []any:
			root, _ := firstRoot(roots).(string)
			isolatedWorkspace = len(roots) == 1 && strings.TrimSpace(root) != ""
		}
	}
	for _, definition := range definitions {
		if definition.ReadOnly || (isolatedWorkspace && hasCapability(definition.Capabilities, "workspace-contained")) {
			result[strings.TrimSpace(definition.Name)] = true
		}
	}
	return result
}

func firstRoot(roots []any) any {
	if len(roots) == 0 {
		return nil
	}
	return roots[0]
}

func availableToolNames(definitions []domain.ToolDefinition) map[string]bool {
	result := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		result[definition.Name] = true
	}
	return result
}

func readOnlyToolDefinitions(definitions []domain.ToolDefinition) []domain.ToolDefinition {
	values := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if definition.ReadOnly && definition.Name != askUserToolName {
			values = append(values, definition)
		}
	}
	return values
}

func availableToolNamesSorted(definitions []domain.ToolDefinition) []string {
	result := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if name := strings.TrimSpace(definition.Name); name != "" {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

// stagedToolDefinitions 決定「系統工具優先階段」本輪公開哪些工具。
//
// 兩階段策略的原意是讓 LLM 優先使用目前 OS 的系統程式，Shell 真正執行失敗後才
// 公開完整原生工具目錄；部署根本沒有 Shell 時直接回到完整目錄，避免把 Agent 變成
// 沒有外部能力的文字模型。
//
// 唯讀工具一律直接公開：先前它們要等 shell_exec 實際失敗過一次才解鎖，等於每個
// 「讀檔案／盤點目錄」的需求都固定多花一輪跑一個註定失敗的命令，卻沒有任何產出。
// 唯讀工具沒有副作用，提前公開不會放寬任何權限邊界（elevated 與 Approval 仍照舊）。
// 原始碼寫入／編輯與文件產出直接公開，但不改變原有權限閘門。
// 其餘寫入型內建工具維持 Shell 優先策略，失敗後才由備援階段公開。
func stagedToolDefinitions(definitions []domain.ToolDefinition, builtinFallback bool) []domain.ToolDefinition {
	if builtinFallback || !definitionNamed(definitions, systemShellToolName) {
		return definitions
	}
	result := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		name := strings.ToLower(strings.TrimSpace(definition.Name))
		staged := definition.ReadOnly ||
			name == systemShellToolName || name == waitToolName || name == sshWaitToolName ||
			name == "file_write" || name == "file_edit" ||
			strings.HasPrefix(name, "plan_") || strings.HasPrefix(name, "mcp__") ||
			documentAuthoringTool(name)
		if staged {
			result = append(result, definition)
		}
	}
	return result
}

// documentAuthoringTool 是「產出辦公文件」的工具，不受第一階段的 Shell 優先政策限制。
//
// Shell 優先的用意是讓副作用交給 OS 既有程式處理，但產生 XLSX／DOCX 正是 Shell 做不到
// 的事：實測使用者說「把結果轉成 Excel」，模型照政策先用 shell_exec 找 python 的
// openpyxl，四分四十秒後失敗，最後退而求其次寫了一個 CSV——而機器上一直有能直接
// 產出 XLSX 的內建工具，只是那一輪的目錄裡沒有它。
//
// 辦公文件家族原本整個被延後到備援階段，理由是 schema 很長、會撐大第一階段的目錄。
// 那個理由已經由工具檢索處理（目錄只帶進這一輪相關的工具），不需要再用「拿掉能力」來換。
func documentAuthoringTool(name string) bool {
	switch name {
	case "document_create", "document_convert", "document_edit":
		return true
	default:
		return false
	}
}

func containsNonPlanningTool(calls []domain.ToolCall) bool {
	for _, call := range calls {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(call.Name)), "plan_") {
			return true
		}
	}
	return false
}

// shouldEnableBuiltinFallback 只把「工具已獲准且確實執行，但系統命令失敗」
// 視為解鎖訊號。人工拒絕、Approval 中斷、Hook 政策拒絕與 Harness loop guard
// 都不是系統工具不好用，不能藉由這些結果繞過原本的權限決策。
func shouldEnableBuiltinFallback(call domain.ToolCall, result domain.ToolExecution) bool {
	if !strings.EqualFold(strings.TrimSpace(call.Name), systemShellToolName) || !result.IsError || result.Terminate {
		return false
	}
	for _, key := range []string{"approval_decision", "approval_interrupted", "refused", "loop_guard", "skipped"} {
		if _, exists := result.Details[key]; exists {
			return false
		}
	}
	return true
}

// groupToolCalls 把一輪的 tool call 切成可並行的連續區段，其餘呼叫各自成為單獨的群組。
//
// 只有「連續」的 read-only 呼叫可以並行：[read, write, read] 之中兩個 read
// 隔著一個 write，併發執行會讓第二個 read 讀到不確定的狀態。
// 未出現在工具定義裡的名稱一律視為非 read-only，也就是照舊依序執行。
func groupToolCalls(calls []domain.ToolCall, parallelizable map[string]bool) [][]domain.ToolCall {
	groups := [][]domain.ToolCall{}
	current := []domain.ToolCall{}
	flush := func() {
		if len(current) > 0 {
			groups = append(groups, current)
			current = nil
		}
	}
	for _, call := range calls {
		if !parallelizable[call.Name] {
			flush()
			groups = append(groups, []domain.ToolCall{call})
			continue
		}
		current = append(current, call)
		if len(current) == maxParallelTools {
			flush()
		}
	}
	flush()
	return groups
}

// runToolGroup 執行一個群組並依原順序回傳結果。
// 單一元素的群組不開 goroutine，讓依序執行的路徑與過去完全一致。
func (r *Runner) runToolGroup(ctx context.Context, session domain.Session, group []domain.ToolCall, sink *serializedSink, runID string, available, approvalExempt map[string]bool, approvals *runApprovalState, loopGuard *toolLoopGuard, retriever *toolRetriever) []toolOutcome {
	outcomes := make([]toolOutcome, len(group))
	if len(group) == 1 {
		outcomes[0] = r.executeToolCall(ctx, session, group[0], sink, runID, available[group[0].Name], approvalExempt[group[0].Name], approvals, loopGuard, retriever)
		return outcomes
	}
	var waiter sync.WaitGroup
	for index, call := range group {
		waiter.Add(1)
		go func(index int, call domain.ToolCall) {
			defer waiter.Done()
			outcomes[index] = r.executeToolCall(ctx, session, call, sink, runID, available[call.Name], approvalExempt[call.Name], approvals, loopGuard, retriever)
		}(index, call)
	}
	waiter.Wait()
	return outcomes
}

// executeToolCall 執行單一工具呼叫並永遠回傳結果，不回傳 error：
// 工具失敗是要交回模型的觀察，不是 run 的失敗。
//
// 並行群組會同時呼叫這個方法，因此 BeforeTool 與 AfterTool hook 必須是併發安全的。
func (r *Runner) executeToolCall(ctx context.Context, session domain.Session, call domain.ToolCall, sink *serializedSink, runID string, available, approvalExempt bool, approvals *runApprovalState, loopGuard *toolLoopGuard, retriever *toolRetriever) toolOutcome {
	startedAt := time.Now().UTC()
	// find_tools 是 Harness 自己的工具目錄檢索，不下放到任何 Runtime，
	// 也不需要核准：它只讀取本 run 已經取得的工具定義。
	if retriever.enabled() && strings.EqualFold(strings.TrimSpace(call.Name), findToolsToolName) {
		result := retriever.execute(call)
		return toolOutcome{call: call, result: result, startedAt: startedAt, duration: time.Since(startedAt)}
	}
	if !available {
		return toolOutcome{
			call: call,
			result: domain.ToolExecution{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    "工具未執行：此工具未在目前的工具供應階段公開。",
				IsError:    true,
				Details: map[string]any{
					"refused":              true,
					"unavailable_in_stage": true,
					"execution_state":      domain.ToolNotDispatched,
				},
			},
			startedAt: startedAt,
			duration:  time.Since(startedAt),
		}
	}
	// 呼叫過的工具留在目錄裡：同一個工具往往要連續用好幾輪（換參數、翻頁、
	// 用第一次的結果再查一次），每輪都要重新檢索一次是白費回合。
	if retriever.enabled() && !retrievalExempt(call.Name) {
		retriever.reveal(call.Name)
	}
	if resolver, ok := r.Tools.(ports.ToolContractResolver); ok {
		definition, err := resolver.ResolveDefinition(ctx, session, call.Name)
		if err != nil || call.ExpectedContractID == "" || call.ExpectedContractID != domain.ToolContractID(definition) {
			return toolOutcome{call: call, startedAt: startedAt, result: domain.ToolExecution{
				ToolCallID: call.ID, ToolName: call.Name, IsError: true,
				Content: "工具契約已變更或不可用，本次未派送。下一輪將更新目錄；請重新確認工具參數與授權範圍。",
				Details: map[string]any{"execution_state": domain.ToolNotDispatched, "contract_changed": true},
			}}
		}
		approvalExempt = approvalExemptToolNames([]domain.ToolDefinition{definition}, session)[call.Name]
	}
	uncertainRetry := false
	if guarded, skip := loopGuard.before(call); skip {
		unknown, _ := guarded.Details["outcome_unknown"].(bool)
		if !unknown || r.Approvals == nil {
			return toolOutcome{call: call, result: guarded, startedAt: startedAt, duration: time.Since(startedAt)}
		}
		uncertainRetry = true
	}
	unknownRetryApproved := false
	result := domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name}
	refused := false
	if r.BeforeTool != nil {
		if hookErr := r.BeforeTool(ctx, session, call); hookErr != nil {
			result.Content = hookErr.Error()
			result.IsError = true
			result.Details = map[string]any{"refused": true}
			refused = true
		}
	}
	// 豁免只來自本輪工具契約：RAM 儲存不能豁免 Shell、SSH 或遠端副作用。
	exemptReason := "read_only_or_isolated_workspace"
	if !refused && !uncertainRetry && approvalExempt && r.Approvals != nil && r.Approvals.Required(call.Name) {
		// 留下紀錄：使用者要能看出這次呼叫為什麼沒有跳核准。
		_ = sink.emitEvent("run.approval_skipped", map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Name,
			"reason":       exemptReason,
		})
	}
	if !refused && r.Approvals != nil && (uncertainRetry || (!approvalExempt && !approvals.approved() && r.Approvals.Required(call.Name))) {
		request := domain.ToolApprovalRequest{
			ID:          domain.NewID("approval"),
			RunID:       strings.TrimSpace(runID),
			SessionID:   session.ID,
			ToolCallID:  call.ID,
			ToolName:    call.Name,
			Arguments:   visibleApprovalArguments(call.Arguments),
			Reason:      "此工具可能產生外部副作用，需要人工核准。",
			RequestedAt: time.Now().UTC(),
		}
		if uncertainRetry {
			request.OneTimeOnly = true
			request.Reason = "先前相同操作的執行結果未知，可能已產生副作用。請先查證外部狀態；核准僅代表接受本次重新執行可能造成重複副作用的風險，不代表先前未執行。"
		}
		if err := r.Approvals.Begin(request); err != nil {
			return toolOutcome{call: call, startedAt: startedAt, duration: time.Since(startedAt), err: err}
		}
		if err := sink.emitEvent("run.approval_required", map[string]any{"approval": request}); err != nil {
			r.Approvals.Cancel(request.ID)
			return toolOutcome{call: call, startedAt: startedAt, duration: time.Since(startedAt), err: err}
		}
		stopApprovalHeartbeat := startApprovalHeartbeat(ctx, sink, call, request)
		decision, err := r.Approvals.Wait(ctx, request.ID)
		stopApprovalHeartbeat()
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			result.Details = map[string]any{"approval_interrupted": true, "approval_id": request.ID}
			refused = true
		} else {
			unknownRetryApproved = uncertainRetry && decision.Decision == domain.ToolApprovalApprove
			if decision.Decision == domain.ToolApprovalApprove && decision.Permanent && !request.OneTimeOnly {
				approvals.approvePermanently()
			}
			if err := sink.emitEvent("run.approval_resolved", map[string]any{"approval": request, "decision": decision}); err != nil {
				return toolOutcome{call: call, startedAt: startedAt, duration: time.Since(startedAt), err: err}
			}
			if decision.Decision == domain.ToolApprovalDeny {
				result.Content = "工具未執行：人工審核拒絕。"
				if decision.Reason != "" {
					result.Content += " 原因：" + decision.Reason
				}
				result.IsError = true
				result.Details = map[string]any{
					"approval_id":       request.ID,
					"approval_decision": decision.Decision,
				}
				refused = true
			}
		}
	}
	if !refused {
		// 派送意圖必須先持久化；此紀錄不聲稱遠端已收到。結果遺失時一律保守為未知。
		if err := ctx.Err(); err != nil {
			return toolOutcome{call: call, startedAt: startedAt, err: err}
		}
		if r.Sessions != nil {
			if _, err := appendRecord(ctx, r.Sessions, session.ID, domain.SessionEntryToolDispatched, map[string]any{
				"operation_id": runID, "tool_call_id": call.ID, "tool_name": call.Name,
				"call_signature": toolCallSignature(call),
			}); err != nil {
				return toolOutcome{call: call, startedAt: startedAt, err: err}
			}
		}
		executed, err := r.Tools.Execute(ctx, session, call, func(update domain.ToolExecution) error {
			return sink.emitEvent("tool.execution.update", map[string]any{
				"tool_call_id": call.ID,
				"tool_name":    call.Name,
				"update":       update,
			})
		})
		if err != nil {
			executed.IsError = true
			executed.Content = strings.TrimSpace(executed.Content + "\n" + err.Error())
			if executed.Details == nil {
				executed.Details = map[string]any{}
			}
			if domain.ToolExecutionState(executed.Details) == "" {
				executed.Details["execution_state"] = domain.ToolOutcomeUnknown
			}
		}
		result = executed
	}
	if r.AfterTool != nil {
		result = r.AfterTool(ctx, session, call, result)
	}
	result.ToolCallID = call.ID
	result.ToolName = call.Name
	if result.Details == nil {
		result.Details = map[string]any{}
	}
	if unknownRetryApproved {
		// 新呼叫成功也不抹除舊呼叫的未知紀錄，無法據此宣稱恰好執行一次。
		result.Details["unknown_retry_approved"] = true
	}
	if domain.ToolExecutionState(result.Details) == "" {
		switch {
		case refused:
			result.Details["execution_state"] = domain.ToolNotDispatched
		case ctx.Err() != nil:
			result.Details["execution_state"] = domain.ToolOutcomeUnknown
		case result.IsError:
			result.Details["execution_state"] = domain.ToolFailed
		default:
			result.Details["execution_state"] = domain.ToolSucceeded
		}
	}
	if domain.ToolExecutionState(result.Details) == domain.ToolOutcomeUnknown {
		result.IsError = true
		result.Content += "\n[執行結果未知：可能已產生副作用，請先唯讀查證，不得直接重送。]"
	}
	loopGuard.observe(call, result)
	return toolOutcome{call: call, result: result, startedAt: startedAt, duration: time.Since(startedAt)}
}

// approvalHeartbeatInterval 決定等待人工核准時的狀態回報頻率。
// 核准對話框可能因為切換對話而沒被看到，這時 Run 會安靜地卡住直到 wall-clock
// 預算用完；定期回報讓「在等你按核准」直接出現在執行過程裡。
var approvalHeartbeatInterval = 30 * time.Second

func startApprovalHeartbeat(ctx context.Context, sink *serializedSink, call domain.ToolCall, request domain.ToolApprovalRequest) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(approvalHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(request.RequestedAt).Round(time.Second)
				_ = sink.emitEvent("tool.execution.update", map[string]any{
					"tool_call_id": call.ID,
					"tool_name":    call.Name,
					"update": domain.ToolExecution{
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Content:    fmt.Sprintf("等待人工核准 %s（已 %s）", call.Name, elapsed),
						Details: map[string]any{
							"phase":           "waiting_approval",
							"approval_id":     request.ID,
							"elapsed_seconds": int(elapsed.Seconds()),
						},
					},
				})
			}
		}
	}()
	return func() { close(done) }
}

var hiddenApprovalArgumentNames = map[string]bool{
	"annotations":  true,
	"blocks":       true,
	"cell_updates": true,
	"content":      true,
	"new_content":  true,
	"new_text":     true,
	"old_content":  true,
	"old_text":     true,
	"patch":        true,
	"replacement":  true,
	"replacements": true,
	"sheets":       true,
	"slides":       true,
}

// visibleApprovalArguments 只保留判斷副作用範圍所需的參數。檔案本文仍留在
// Harness 的內部 tool call，不能透過 approval event 或對話框揭露。
func visibleApprovalArguments(arguments map[string]any) map[string]any {
	result := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if hiddenApprovalArgumentNames[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		result[key] = visibleApprovalValue(value)
	}
	return result
}

func visibleApprovalValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return visibleApprovalArguments(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = visibleApprovalValue(item)
		}
		return result
	default:
		return value
	}
}

func toolExecutionStatus(result domain.ToolExecution) string {
	switch {
	case result.Terminate:
		return "terminated"
	case result.IsError:
		return "failed"
	default:
		return "completed"
	}
}
