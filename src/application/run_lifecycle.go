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

func (s *Service) finishTerminalRun(run domain.Run, sequence *int64) {
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
	if err := s.appendEvent(current, sequence, "run."+string(current.Status), payload); err != nil {
		s.logger.Error("terminal event write failed", "run_id", run.ID, "error", err)
	}
	s.notifyRunFinished(current)
}
