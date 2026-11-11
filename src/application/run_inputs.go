package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/textutil"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxRunInputRunes 限制單則插入訊息的長度；插入是補充說明，不是貼大型資料的管道。
const maxRunInputRunes = 32000

// maxPendingRunInputs 限制同一 Run 尚未消化的插入訊息數量。
const maxPendingRunInputs = 16

// AppendRunInput 把使用者在 Run 執行中送出的訊息排入插入佇列。訊息不會打斷
// 進行中的模型回覆或工具執行，而是在目前回合結束、下一輪請求組裝前寫入對話。
// Run 已結束或不在本程序執行時回 ErrConflict，呼叫端應改為建立新的 Run。
func (s *Service) AppendRunInput(ctx context.Context, runID, content, clientID string) (domain.RunInputMessage, error) {
	runID = strings.TrimSpace(runID)
	content = strings.TrimSpace(textutil.NormalizeFullwidthASCII(content))
	clientID = strings.TrimSpace(clientID)
	if runID == "" || content == "" {
		return domain.RunInputMessage{}, fmt.Errorf("%w: run_id and input are required", domain.ErrInvalidInput)
	}
	if utf8.RuneCountInString(content) > maxRunInputRunes {
		return domain.RunInputMessage{}, fmt.Errorf("%w: input exceeds %d characters", domain.ErrInvalidInput, maxRunInputRunes)
	}
	if len(clientID) > 128 {
		return domain.RunInputMessage{}, fmt.Errorf("%w: client_id is too long", domain.ErrInvalidInput)
	}
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return domain.RunInputMessage{}, err
	}
	if terminalRun(run.Status) {
		return domain.RunInputMessage{}, fmt.Errorf("%w: run already finished", domain.ErrConflict)
	}
	message := domain.RunInputMessage{
		ID:        domain.NewID("input"),
		Content:   content,
		ClientID:  clientID,
		CreatedAt: s.now().UTC(),
	}
	s.mu.Lock()
	current, active := s.active[runID]
	if !active || current.inputs == nil || current.cancelRequested {
		s.mu.Unlock()
		return domain.RunInputMessage{}, fmt.Errorf("%w: run is not accepting input", domain.ErrConflict)
	}
	if current.inputs.Count() >= maxPendingRunInputs {
		s.mu.Unlock()
		return domain.RunInputMessage{}, fmt.Errorf("%w: too many pending inputs", domain.ErrConflict)
	}
	if !current.inputs.Push(message) {
		s.mu.Unlock()
		return domain.RunInputMessage{}, fmt.Errorf("%w: run already finished", domain.ErrConflict)
	}
	s.mu.Unlock()
	// 事件只是通知；Run 恰好在此刻結束時，訊息會改由終止事件的 returned_inputs 退回。
	if err := s.appendControlEvent(run, "run.input_queued", map[string]any{"input": message}); err != nil {
		s.logger.Warn("run input queued event not written", "run_id", runID, "error", err)
	}
	return message, nil
}

// closeRunInputsLocked 關閉插入佇列並取回未消化的訊息；呼叫端必須持有 s.mu。
func (s *Service) closeRunInputsLocked(runID string) []domain.RunInputMessage {
	current, ok := s.active[runID]
	if !ok || current.inputs == nil {
		return nil
	}
	return current.inputs.Close()
}
