package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"context"
	"errors"
	"time"
)

// 整批已執行的結果優先保存；事件通道或其中一筆失敗，不能丟掉其他工具的真實觀察。
// 一批最多 8 筆，共用有界收尾 context，不因每筆失敗重設逾時。
func (r *Runner) persistToolOutcomes(ctx context.Context, sessionID, operationID, turnID string, outcomes []toolOutcome, readOnly map[string]bool) ([]domain.Message, error) {
	persistCtx, cancel := persistContext(ctx)
	defer cancel()
	messages := make([]domain.Message, len(outcomes))
	var failures []error
	for i, outcome := range outcomes {
		if outcome.err != nil {
			failures = append(failures, outcome.err)
		}
		call, result := outcome.call, outcome.result
		message := domain.Message{
			ID: domain.NewID("msg"), SessionID: sessionID, Role: "tool",
			Content: result.Content, ToolCallID: call.ID, ToolName: call.Name, IsError: result.IsError,
			Metadata: valueutil.CloneMap(result.Details), CreatedAt: time.Now().UTC(),
		}
		if message.Metadata == nil {
			message.Metadata = map[string]any{}
		}
		message.Metadata["internal"] = true
		message.Metadata["phase"] = "tool_observation"
		message.Metadata["tool_read_only"] = readOnly[call.Name]
		message.Metadata["tool_contract_id"] = call.ExpectedContractID
		messages[i] = message
		if _, err := appendMessage(persistCtx, r.Sessions, sessionID, message); err != nil {
			failures = append(failures, err)
			continue
		}
		if _, err := appendRecord(persistCtx, r.Sessions, sessionID, domain.SessionEntryToolFinished, map[string]any{
			"operation_id": operationID, "turn_id": turnID, "tool_call_id": call.ID,
			"tool_name": call.Name, "status": toolExecutionStatus(result),
			"duration_ms": outcome.duration.Milliseconds(), "result": result,
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return messages, errors.Join(failures...)
}
