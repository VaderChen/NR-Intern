package application

import (
	"context"
	"fmt"

	"AgenticService/src/domain"
)

// saveActiveRun 讓核准回呼與取消／終態共用控制鎖，避免晚到的回呼復活工作。
func (s *Service) saveActiveRun(run domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := s.runs.Get(context.Background(), run.ID)
	if err != nil {
		return err
	}
	if active, ok := s.active[run.ID]; ok && active.cancelRequested {
		return context.Canceled
	}
	if terminalRun(latest.Status) {
		return fmt.Errorf("%w: terminal run cannot become active", domain.ErrConflict)
	}
	return s.runs.Save(context.Background(), run)
}

// saveTerminalRun 的 bool 表示這次是否首次寫入終態，只有勝出的呼叫者發送終止事件。
// 已終止時只接受較完整的用量；取消、Provider 收尾與錯誤回呼不應互相覆寫狀態。
func (s *Service) saveTerminalRun(run domain.Run) (domain.Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := s.runs.Get(context.Background(), run.ID)
	if err != nil {
		return run, false, err
	}
	if terminalRun(latest.Status) {
		if usageAtLeast(run.Usage, latest.Usage) {
			latest.Usage = run.Usage
			if latest.Result != nil {
				latest.Result.Usage = run.Usage
			}
			if err := s.runs.Save(context.Background(), latest); err != nil {
				return latest, false, err
			}
		}
		return latest, false, nil
	}
	if !usageAtLeast(run.Usage, latest.Usage) {
		run.Usage = latest.Usage
	}
	if run.StartedAt == nil {
		run.StartedAt = latest.StartedAt
	}
	if active, ok := s.active[run.ID]; ok && active.cancelRequested && run.Status != domain.RunStatusCanceled {
		run.Status = domain.RunStatusCanceled
		run.Result = nil
		run.Error = &domain.RunError{Code: "run_canceled", Message: "run canceled", Retryable: true}
	}
	run.PendingApproval = nil
	if err := s.runs.Save(context.Background(), run); err != nil {
		return latest, false, err
	}
	return run, true, nil
}

func usageAtLeast(candidate, previous *domain.RunUsage) bool {
	return candidate != nil && (previous == nil ||
		(candidate.InputTokens >= previous.InputTokens && candidate.OutputTokens >= previous.OutputTokens &&
			candidate.TotalTokens >= previous.TotalTokens))
}

// commitCompletedRun 的 bool 表示仍暫停，呼叫端應在鎖外等待。
// 兩層鎖分別以 defer 釋放，事件儲存異常也不能永久鎖住同分片的其他 Run。
func (s *Service) commitCompletedRun(run domain.Run, sequence *int64) (paused bool, err error) {
	lock := s.eventLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	err = func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.active[run.ID]; ok && current.cancelRequested {
			return context.Canceled
		}
		if s.pausedRuns[run.ID] {
			paused = true
			return nil
		}
		return s.runs.Save(context.Background(), run)
	}()
	if err != nil || paused {
		return paused, err
	}
	defer s.notifyRun(run.ID)
	payload := map[string]any{"status": run.Status, "result": run.Result}
	if err := s.appendEventLocked(run, sequence, "run.completed", payload); err != nil {
		// 工作已完成，事件通道故障不改寫成 failed；重連時只補事件。
		s.logger.Error("terminal event write failed", "run_id", run.ID, "error", err)
	}
	return false, nil
}

func (s *Service) finishTerminalRun(run domain.Run, sequence *int64) {
	lock := s.eventLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	// 即使事件寫入失敗，等待者仍須醒來讀取終態並嘗試補齊事件。
	defer s.notifyRun(run.ID)
	current, changed, err := s.saveTerminalRun(run)
	if err != nil {
		s.logger.Error("terminal run write failed", "run_id", run.ID, "error", err)
		return
	}
	if !changed {
		return
	}
	payload := map[string]any{"status": current.Status, "error": current.Error}
	if current.Usage != nil {
		payload["usage"] = *current.Usage
	}
	if err := s.appendEventLocked(current, sequence, "run."+string(current.Status), payload); err != nil {
		s.logger.Error("terminal event write failed", "run_id", run.ID, "error", err)
	}
	s.notifyRunFinished(current)
}
