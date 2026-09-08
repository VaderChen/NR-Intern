package application

import (
	"context"
	"errors"

	"AgenticService/src/domain"
	"AgenticService/src/ports"
)

// PruneRunEvents 在刪除當下核對生命週期。終態可能只是「已接受取消」，
// active 執行緒仍可能收尾；同時鎖住新 Run 准入，避免把新檔案誤當孤兒。
func (s *Service) PruneRunEvents(ctx context.Context, runID string) (bool, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	_, active := s.active[runID]
	s.mu.Unlock()
	if active {
		return false, nil
	}
	run, err := s.runs.Get(ctx, runID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	if err == nil && !terminalRun(run.Status) {
		return false, nil
	}
	store, ok := s.events.(ports.RunEventDeleter)
	if !ok {
		return false, nil
	}
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if err := store.Delete(runID); err != nil {
		return false, err
	}
	delete(s.eventSequences, runID)
	return true, nil
}
