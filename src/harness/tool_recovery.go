package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
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
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, more, err := repository.ListEntriesPage(ctx, sessionID, cursor, 256)
		if err != nil {
			return err
		}
		if more && len(entries) == 0 {
			return fmt.Errorf("%w: 工具稽核分頁宣告有後續資料但未回傳紀錄", domain.ErrConflict)
		}
		for _, entry := range entries {
			if entry.Sequence <= cursor {
				return fmt.Errorf("%w: 工具稽核分頁序號未遞增", domain.ErrConflict)
			}
			cursor = entry.Sequence
			if message := entry.Message; message != nil {
				for _, call := range message.ToolCalls {
					if previous := pending[call.ID]; previous != "" {
						g.uncertainSignatures[previous] = true
					}
					pending[call.ID] = toolCallSignature(call)
				}
				if strings.EqualFold(message.Role, "tool") {
					if signature := pending[message.ToolCallID]; signature != "" && domain.ToolExecutionState(message.Metadata) == domain.ToolOutcomeUnknown {
						// 未知是已保存的事實；晚到或重複的成功訊息不能抹除它。
						g.uncertainSignatures[signature] = true
					}
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
		if !more {
			break
		}
	}
	for _, signature := range pending {
		g.uncertainSignatures[signature] = true
	}
	return nil
}
