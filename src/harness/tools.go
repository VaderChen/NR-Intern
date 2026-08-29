package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
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
	systemShellToolName      = "shell_exec"
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
		result[definition.Name] = true
	}
	return result
}

func availableToolNames(definitions []domain.ToolDefinition) map[string]bool {
	result := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		result[definition.Name] = true
	}
	return result
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

// stagedToolDefinitions 實作每個 Run 的兩階段工具供應策略：只要 Session
// 有 shell_exec，第一階段就只公開 Shell，讓 LLM 優先使用目前 OS 的系統程式。
// Shell 真正執行失敗後才把完整原生工具目錄公開。若部署根本沒有 Shell，則
// 直接回到完整目錄，避免把 Agent 變成完全沒有外部能力的文字模型。
func stagedToolDefinitions(definitions []domain.ToolDefinition, builtinFallback bool) []domain.ToolDefinition {
	if builtinFallback || !definitionNamed(definitions, systemShellToolName) {
		return definitions
	}
	result := make([]domain.ToolDefinition, 0, 4)
	for _, definition := range definitions {
		name := strings.ToLower(strings.TrimSpace(definition.Name))
		if name == systemShellToolName || strings.HasPrefix(name, "plan_") || strings.HasPrefix(name, "mcp__") {
			result = append(result, definition)
		}
	}
	return result
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
func (r *Runner) runToolGroup(ctx context.Context, session domain.Session, group []domain.ToolCall, sink *serializedSink, runID string, available map[string]bool, approvals *runApprovalState, loopGuard *toolLoopGuard) []toolOutcome {
	outcomes := make([]toolOutcome, len(group))
	if len(group) == 1 {
		outcomes[0] = r.executeToolCall(ctx, session, group[0], sink, runID, available[group[0].Name], approvals, loopGuard)
		return outcomes
	}
	var waiter sync.WaitGroup
	for index, call := range group {
		waiter.Add(1)
		go func(index int, call domain.ToolCall) {
			defer waiter.Done()
			outcomes[index] = r.executeToolCall(ctx, session, call, sink, runID, available[call.Name], approvals, loopGuard)
		}(index, call)
	}
	waiter.Wait()
	return outcomes
}

// executeToolCall 執行單一工具呼叫並永遠回傳結果，不回傳 error：
// 工具失敗是要交回模型的觀察，不是 run 的失敗。
//
// 並行群組會同時呼叫這個方法，因此 BeforeTool 與 AfterTool hook 必須是併發安全的。
func (r *Runner) executeToolCall(ctx context.Context, session domain.Session, call domain.ToolCall, sink *serializedSink, runID string, available bool, approvals *runApprovalState, loopGuard *toolLoopGuard) toolOutcome {
	startedAt := time.Now().UTC()
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
				},
			},
			startedAt: startedAt,
			duration:  time.Since(startedAt),
		}
	}
	if guarded, skip := loopGuard.before(call); skip {
		return toolOutcome{call: call, result: guarded, startedAt: startedAt, duration: time.Since(startedAt)}
	}
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
	if !refused && r.Approvals != nil && !approvals.approved() && r.Approvals.Required(call.Name) {
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
		if err := r.Approvals.Begin(request); err != nil {
			return toolOutcome{call: call, startedAt: startedAt, duration: time.Since(startedAt), err: err}
		}
		if err := sink.emitEvent("run.approval_required", map[string]any{"approval": request}); err != nil {
			r.Approvals.Cancel(request.ID)
			return toolOutcome{call: call, startedAt: startedAt, duration: time.Since(startedAt), err: err}
		}
		decision, err := r.Approvals.Wait(ctx, request.ID)
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			result.Details = map[string]any{"approval_interrupted": true, "approval_id": request.ID}
			refused = true
		} else {
			if decision.Decision == domain.ToolApprovalApprove && decision.Permanent {
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
		executed, err := r.Tools.Execute(ctx, session, call, func(update domain.ToolExecution) error {
			return sink.emitEvent("tool.execution.update", map[string]any{
				"tool_call_id": call.ID,
				"tool_name":    call.Name,
				"update":       update,
			})
		})
		if err != nil {
			executed = domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: err.Error(), IsError: true}
		}
		result = executed
	}
	if r.AfterTool != nil {
		result = r.AfterTool(ctx, session, call, result)
	}
	result.ToolCallID = call.ID
	result.ToolName = call.Name
	loopGuard.observe(call, result)
	return toolOutcome{call: call, result: result, startedAt: startedAt, duration: time.Since(startedAt)}
}

var hiddenApprovalArgumentNames = map[string]bool{
	"content":     true,
	"new_content": true,
	"new_text":    true,
	"old_content": true,
	"old_text":    true,
	"patch":       true,
	"replacement": true,
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
