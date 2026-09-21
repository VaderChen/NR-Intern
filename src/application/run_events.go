package application

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"strings"
)

var errMissingTerminalEvent = errors.New("terminal event requires reconciliation")

// ReadRunEventBatch 與終態提交共用事件鎖，避免取完舊事件後看到新終態而漏送尾筆。
// Run 已落盤但終止事件先前寫入失敗時，僅補事件，不重新執行工作。
func (s *Service) ReadRunEventBatch(ctx context.Context, runID string, afterSequence int64) (domain.Run, []domain.Event, error) {
	run, events, err := s.readRunEventBatch(ctx, runID, afterSequence, false)
	if !errors.Is(err, errMissingTerminalEvent) {
		return run, events, err
	}
	// 補寫才取得准入／刪除鎖；正常重播不序列化不同 Run 的磁碟讀取。
	// 必須先釋放事件鎖，再依 startMu → eventLock 的順序重讀，避免刪除後重建事件。
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.readRunEventBatch(ctx, runID, afterSequence, true)
}

func (s *Service) readRunEventBatch(ctx context.Context, runID string, afterSequence int64, repair bool) (domain.Run, []domain.Event, error) {
	runID = strings.TrimSpace(runID)
	if afterSequence < 0 {
		return domain.Run{}, nil, fmt.Errorf("%w: after_sequence cannot be negative", domain.ErrInvalidInput)
	}
	lock := s.eventLock(runID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, nil, err
	}
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, nil, err
	}
	events, err := s.events.List(ctx, runID, afterSequence)
	if err != nil || !terminalRun(run.Status) {
		return run, events, err
	}
	tail := events
	if len(tail) == 0 {
		// 重連的 cursor 可能已涵蓋終止事件；從已知末筆讀起，避免每次掃整份長任務。
		value, _ := s.eventSequences.Load(runID)
		last, _ := value.(int64)
		if last > 0 {
			last--
		}
		tail, err = s.events.List(ctx, runID, last)
		if err != nil {
			return run, nil, err
		}
	}
	sequence := int64(0)
	if len(tail) > 0 {
		last := tail[len(tail)-1]
		sequence = last.Sequence
		if terminalEvent(last.Type) {
			if last.Type != "run."+string(run.Status) {
				return run, nil, fmt.Errorf("%w: run status and terminal event disagree", domain.ErrConflict)
			}
			s.eventSequences.Store(runID, sequence)
			return run, events, nil
		}
	}
	if !repair {
		return run, events, errMissingTerminalEvent
	}
	// List 已核對磁碟實況，不能因先前 Sync 失敗而沿用較舊／較新的快取序號。
	s.eventSequences.Store(runID, sequence)
	if err := s.appendEventLocked(run, &sequence, "run."+string(run.Status), terminalRunPayload(run)); err != nil {
		return run, nil, err
	}
	events, err = s.events.List(ctx, runID, afterSequence)
	return run, events, err
}

func terminalRunPayload(run domain.Run) map[string]any {
	payload := map[string]any{"status": run.Status}
	if run.Result != nil {
		payload["result"] = *run.Result
	}
	if run.Usage != nil {
		payload["usage"] = *run.Usage
	}
	if run.Error != nil {
		payload["error"] = run.Error
	}
	return payload
}
