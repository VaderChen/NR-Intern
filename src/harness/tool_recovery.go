package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
)

// 以原始稽核紀錄恢復未確認操作，不依賴會被壓縮／撤回的模型檢視。
// 分頁只保存未確認呼叫的摘要，不把整份長任務載入記憶體。
func (g *toolLoopGuard) restoreUncertain(ctx context.Context, repository ports.SessionRepository, sessionID string) error {
	if repository == nil {
		return nil
	}
	pending := map[string]string{}
	var cursor int64
	for {
		entries, more, err := repository.ListEntriesPage(ctx, sessionID, cursor, 256)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			cursor = entry.Sequence
			if message := entry.Message; message != nil {
				for _, call := range message.ToolCalls {
					if previous := pending[call.ID]; previous != "" {
						g.uncertainSignatures[previous] = true
					}
					pending[call.ID] = toolCallSignature(call)
				}
				if message.Role == "tool" && domain.ToolExecutionState(message.Metadata) != domain.ToolOutcomeUnknown {
					delete(pending, message.ToolCallID)
				}
			}
			if entry.Type == domain.SessionEntryToolDispatched {
				id, _ := entry.Data["tool_call_id"].(string)
				signature, _ := entry.Data["call_signature"].(string)
				if id != "" && signature != "" {
					pending[id] = signature
				}
			}
		}
		if !more || len(entries) == 0 {
			break
		}
	}
	for _, signature := range pending {
		g.uncertainSignatures[signature] = true
	}
	return nil
}
