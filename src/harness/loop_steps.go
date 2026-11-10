package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/memory"
	"AgenticService/src/ports"
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// 本檔收納從 Runner.Run 抽出的區段。每個函式只負責一段原本內嵌在 Run 的
// 記錄與事件發送，事件順序與 payload 必須與抽出前完全一致。

// turnReference 是回合收尾與稽核記錄共用的識別資料。
type turnReference struct {
	sessionID   string
	operationID string
	id          string
	number      int
	startedAt   time.Time
}

// operationFinish 是 Run 離開時收尾所需的狀態快照。
type operationFinish struct {
	input          Input
	operationID    string
	providerID     string
	modelID        string
	startedAt      time.Time
	budget         *runBudgetTracker
	turn           *turnLifecycle
	logger         *slog.Logger
	finalMessageID string
	pendingTurnID  string
	undispatched   []domain.ToolCall
}

// finishOperation 是 Run 所有退出路徑共用的收尾：補寫未派送的工具結果、彙總
// 用量、補齊未結束的回合，再保存 operation_finished 並發送 agent.end。
// recover 必須留在 Run 的 defer 內，這裡只處理 recover 之後的部分。
func (r *Runner) finishOperation(ctx context.Context, state operationFinish, emit EventSink, output *domain.RunResult, runErr error) error {
	sessionID := state.input.Session.ID
	if len(state.undispatched) > 0 {
		_, persistErr := r.persistUndispatched(ctx, sessionID, state.operationID, state.pendingTurnID, state.undispatched, "工具未執行：Run 已在派送前停止。", "")
		runErr = errors.Join(runErr, persistErr)
	}
	usage := state.budget.usageSnapshot()
	output.Usage = &domain.RunUsage{
		ProviderID:   state.providerID,
		Model:        state.modelID,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.Total(),
		ByModel:      state.budget.modelUsage(),
	}
	status := operationStatus(ctx, runErr)
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if turn := state.turn; turn.id != "" && !turn.finished {
		turnErr := r.finishTurn(recordContext, sessionID, state.operationID, turn.id, turn.number, turn.startedAt, status, turn.toolCount, turn.response, runErr)
		runErr = errors.Join(runErr, turnErr)
		status = operationStatus(ctx, runErr)
	}
	data := map[string]any{
		"operation_id":     state.operationID,
		"run_id":           strings.TrimSpace(state.input.RunID),
		"status":           status,
		"final_message_id": state.finalMessageID,
		"duration_ms":      time.Since(state.startedAt).Milliseconds(),
	}
	if runErr != nil {
		data["error"] = runErr.Error()
	}
	if _, recordErr := appendRecord(recordContext, r.Sessions, sessionID, domain.SessionEntryOperationFinished, data); recordErr != nil {
		runErr = errors.Join(runErr, recordErr)
		status = operationStatus(ctx, runErr)
	}
	endPayload := map[string]any{
		"outcome": status, "operation_id": state.operationID, "final_message_id": state.finalMessageID,
	}
	if runErr != nil {
		endPayload["error"] = runErr.Error()
	}
	if emitErr := emitEvent(emit, "agent.end", endPayload); emitErr != nil {
		runErr = errors.Join(runErr, emitErr)
		status = operationStatus(ctx, runErr)
		data["status"], data["error"] = status, runErr.Error()
		_, recordErr := appendRecord(recordContext, r.Sessions, sessionID, domain.SessionEntryOperationFinished, data)
		runErr = errors.Join(runErr, recordErr)
	}
	if runErr != nil {
		state.logger.Error("harness run finished", "outcome", status, "duration_ms", time.Since(state.startedAt).Milliseconds(), "error", runErr)
	} else {
		state.logger.Info("harness run finished", "outcome", status, "duration_ms", time.Since(state.startedAt).Milliseconds())
	}
	return runErr
}

// recordUserMessage 把這一輪的使用者訊息寫入 transcript 並發送訊息生命週期事件。
func (r *Runner) recordUserMessage(ctx context.Context, input Input, emit EventSink) (domain.Message, error) {
	userMessage := domain.Message{
		ID:        domain.NewID("msg"),
		SessionID: input.Session.ID,
		Role:      "user",
		Content:   strings.TrimSpace(input.UserInput),
		Metadata:  valueutil.CloneMap(input.Metadata),
		CreatedAt: time.Now().UTC(),
	}
	if _, err := appendMessage(ctx, r.Sessions, input.Session.ID, userMessage); err != nil {
		return domain.Message{}, err
	}
	if err := emitMessageLifecycle(emit, "message.start", userMessage); err != nil {
		return domain.Message{}, err
	}
	if err := emitMessageLifecycle(emit, "message.end", userMessage); err != nil {
		return domain.Message{}, err
	}
	return userMessage, nil
}

// reportCompactionStarted 記錄並通知 Context 壓縮開始。
func reportCompactionStarted(logger *slog.Logger, emit EventSink, turn int, status ContextCompactionStatus) error {
	logger.Info("session context compaction started",
		"turn", turn,
		"trigger", status.Trigger,
		"estimated_tokens", status.EstimatedTokens,
		"reported_input_tokens", status.ReportedInputTokens,
		"trigger_tokens", status.TriggerTokens,
		"budget_tokens", status.Budget,
		"trigger_ratio", status.TriggerRatio,
		"history_characters", status.HistoryCharacters,
		"max_history_characters", status.MaxHistoryCharacters,
	)
	// trigger 讓介面說得出「為什麼壓縮」。少了它，使用者看到的就只是
	// 一個轉圈圈，而兩道閘門用的數字完全不同，猜不出是哪一道。
	return emitEvent(emit, "context.compaction.started", map[string]any{
		"trigger":                status.Trigger,
		"estimated_tokens":       status.EstimatedTokens,
		"reported_input_tokens":  status.ReportedInputTokens,
		"trigger_tokens":         status.TriggerTokens,
		"budget_tokens":          status.Budget,
		"trigger_ratio":          status.TriggerRatio,
		"history_characters":     status.HistoryCharacters,
		"max_history_characters": status.MaxHistoryCharacters,
	})
}

// modelEventForwarder 把 Provider 的串流事件轉成 Harness 事件。
func modelEventForwarder(emit EventSink, assistantID, turnID string, instructionStream *instructionTextStream, emitAssistantDelta func(string) error) ports.ModelEventSink {
	return func(event domain.ModelEvent) error {
		switch event.Type {
		case domain.ModelEventTextDelta:
			if instructionStream != nil {
				return instructionStream.Push(event.Delta, emitAssistantDelta)
			}
			return emitAssistantDelta(event.Delta)
		case domain.ModelEventThinkingDelta:
			return emitEvent(emit, "message.thinking_delta", map[string]any{"message_id": assistantID, "delta": event.Delta})
		case domain.ModelEventToolCallDelta:
			payload := map[string]any{"message_id": assistantID, "delta": event.Delta}
			if event.ToolCall != nil {
				payload["index"] = event.ToolCall.Index
				payload["tool_call_id"] = event.ToolCall.ID
				payload["tool_name"] = event.ToolCall.Name
			}
			return emitEvent(emit, "tool_call.delta", payload)
		case domain.ModelEventUsage:
			return emitEvent(emit, "turn.usage", map[string]any{"turn_id": turnID, "usage": event.Usage})
		case domain.ModelEventProgress:
			return emitEvent(emit, "agent.progress", map[string]any{"message": event.Delta})
		default:
			return nil
		}
	}
}

// internalPhaseAssistant 建立只供稽核、不顯示給使用者的 assistant 訊息。
// response 以指標傳入，讓 Usage 與抽出前一樣指向 Run 內的同一份回覆。
func internalPhaseAssistant(assistantID, sessionID string, response *domain.ModelResponse, phase string) domain.Message {
	return domain.Message{
		ID:                assistantID,
		SessionID:         sessionID,
		Role:              "assistant",
		Content:           strings.TrimSpace(response.Content),
		Reasoning:         strings.TrimSpace(response.Reasoning),
		ProviderID:        response.ProviderID,
		ProviderRequestID: response.ProviderRequestID,
		Model:             response.Model,
		StopReason:        response.StopReason,
		Usage:             &response.Usage,
		Metadata: map[string]any{
			"internal": true,
			"phase":    phase,
		},
		CreatedAt: time.Now().UTC(),
	}
}

// closeToollessTurn 結束一個沒有執行工具的回合：先保存 turn_finished，再發送 turn.end。
func (r *Runner) closeToollessTurn(ctx context.Context, emit EventSink, ref turnReference, status string, response domain.ModelResponse) error {
	if err := r.finishTurn(ctx, ref.sessionID, ref.operationID, ref.id, ref.number, ref.startedAt, status, 0, response, nil); err != nil {
		return err
	}
	return emitEvent(emit, "turn.end", map[string]any{"turn": ref.number, "turn_id": ref.id, "tool_result_count": 0})
}

// recordPlanCompletionCheck 記錄計畫完成度追問並結束該回合。
func (r *Runner) recordPlanCompletionCheck(ctx context.Context, emit EventSink, ref turnReference, plan domain.Plan, checks int, response domain.ModelResponse) error {
	if _, err := appendRecord(ctx, r.Sessions, ref.sessionID, domain.SessionEntryPlanCompletionCheck, map[string]any{
		"operation_id": ref.operationID, "turn_id": ref.id, "checks_performed": checks,
		"plan_id": plan.ID, "current_step_id": plan.CurrentStepID,
	}); err != nil {
		return err
	}
	if err := emitEvent(emit, "plan.completion_check", map[string]any{
		"plan_id": plan.ID, "current_step_id": plan.CurrentStepID, "checks_performed": checks,
	}); err != nil {
		return err
	}
	return r.closeToollessTurn(ctx, emit, ref, "plan_completion_check", response)
}

// recordCompletionCheck 記錄完成度追問並結束該回合。reason 為
// unresolved_tool_failure 時附上未解決的工具失敗，否則附上追問原因。
func (r *Runner) recordCompletionCheck(ctx context.Context, emit EventSink, logger *slog.Logger, ref turnReference, completion *completionTracker, reason string, response domain.ModelResponse) error {
	record := map[string]any{
		"operation_id":     ref.operationID,
		"turn_id":          ref.id,
		"checks_performed": completion.checks,
	}
	event := map[string]any{
		"turn":             ref.number,
		"checks_performed": completion.checks,
	}
	if reason == "unresolved_tool_failure" {
		unresolved := completion.unresolved()
		logger.Info("completion challenged",
			"turn", ref.number,
			"turn_id", ref.id,
			"unresolved_failures", len(unresolved),
			"checks_performed", completion.checks,
		)
		record["unresolved_failures"] = unresolved
		event["unresolved_failures"] = unresolved
	} else {
		logger.Info("completion challenged", "turn", ref.number, "turn_id", ref.id, "reason", reason, "checks_performed", completion.checks)
		record["reason"] = reason
		event["reason"] = reason
	}
	if _, err := appendRecord(ctx, r.Sessions, ref.sessionID, domain.SessionEntryCompletionCheck, record); err != nil {
		return err
	}
	if err := emitEvent(emit, "run.completion_check", event); err != nil {
		return err
	}
	return r.closeToollessTurn(ctx, emit, ref, "completion_check", response)
}

// announceToolGroup 依原順序寫完同一群組的開始記錄與事件，讓 transcript 與
// 事件流的順序不受併發影響。
func (r *Runner) announceToolGroup(ctx context.Context, sink *serializedSink, ref turnReference, group []domain.ToolCall) error {
	for _, call := range group {
		if _, err := appendRecord(ctx, r.Sessions, ref.sessionID, domain.SessionEntryToolStarted, map[string]any{
			"operation_id": ref.operationID,
			"turn_id":      ref.id,
			"tool_call_id": call.ID,
			"tool_name":    call.Name,
			"arguments":    valueutil.CloneMap(call.Arguments),
		}); err != nil {
			return err
		}
		if err := sink.emitEvent("tool.execution.start", map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Name,
			"arguments":    call.Arguments,
			"parallel":     len(group) > 1,
		}); err != nil {
			return err
		}
	}
	return nil
}

// recallAfterToolFailure 在同一個工具連續失敗時補做一次記憶檢索。第二個回傳值
// 表示是否召回到記憶；檢索本身失敗只記錄警告，不中斷 Run。
func (r *Runner) recallAfterToolFailure(ctx context.Context, session domain.Session, call domain.ToolCall, result domain.ToolExecution, operationID string, logger *slog.Logger, sink *serializedSink) (string, bool, error) {
	// 「這個錯以前遇過」是記憶最有價值的時刻，值得為它多打一次檢索。
	retry, recallErr := r.recallMemory(ctx, session, memory.FailureRecallQuery(call.Name, result.Content), operationID)
	if recallErr != nil {
		logger.Warn("failure-triggered memory recall failed", "tool_name", call.Name, "error", recallErr)
		return "", false, nil
	}
	if len(retry.Memories) == 0 {
		return "", false, nil
	}
	logger.Debug("failure-triggered memory recall", "tool_name", call.Name, "count", len(retry.Memories))
	if err := sink.emitEvent("memory.recalled", map[string]any{
		"count":      len(retry.Memories),
		"memory_ids": memoryIDs(retry.Memories),
		"truncated":  retry.Truncated,
		"trigger":    "tool_failure",
	}); err != nil {
		return retry.SystemPrompt, true, err
	}
	return retry.SystemPrompt, true, nil
}
