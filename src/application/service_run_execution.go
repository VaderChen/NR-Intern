package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/ports"
	"context"
	"errors"
	"fmt"
	"runtime/debug"
)

func (s *Service) executeRun(ctx context.Context, engine ports.AgentEngine, session domain.Session, input domain.RunInput, run domain.Run) {
	sequence := int64(0)
	defer s.wg.Done()
	if guard, ok := s.runs.(ports.RunRetentionGuard); ok {
		defer guard.ReleaseRun(run.ID)
	}
	// 排在 clearActive 之前註冊，因此執行順序在它之後：下一輪要通過
	// hasActiveSession 的檢查，前一輪必須已經從 active 移除。
	defer s.advancePlanLoop(run.ID)
	defer s.clearActive(run.ID)
	defer func() {
		if recovered := recover(); recovered != nil {
			// 這裡過去完全沒有紀錄：panic 被吞掉，只留下一筆沒有原因的 failed run。
			s.logger.Error("run panicked", "run_id", run.ID, "session_id", run.SessionID, "panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
			latest, err := s.runs.Get(context.Background(), run.ID)
			if err == nil && !terminalRun(latest.Status) {
				s.finishFailed(latest, "agent_panic", fmt.Errorf("代理執行發生內部異常；請以 Run 識別碼查閱日誌"), false, &sequence)
			}
		}
	}()

	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	release, err := s.acquireSession(ctx, session.ID, run.ID)
	if err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	defer func() {
		if release != nil {
			release()
		}
		s.clearSessionPause(session.ID, run.ID)
	}()
	if err := ctx.Err(); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}
	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}

	startedAt := s.now().UTC()
	run.Status = domain.RunStatusRunning
	run.StartedAt = &startedAt
	s.mu.Lock()
	if current, canceled := s.active[run.ID]; canceled && current.cancelRequested {
		s.mu.Unlock()
		return
	}
	runSaveErr := s.runs.Save(context.Background(), run)
	s.mu.Unlock()
	if runSaveErr != nil {
		s.finishFailed(run, "run_state_write_failed", runSaveErr, true, &sequence)
		return
	}
	if err := s.appendEvent(run, &sequence, "run.started", map[string]any{"status": run.Status}); err != nil {
		s.finishFailed(run, "event_write_failed", err, true, &sequence)
		return
	}

	result, runErr := engine.Run(ctx, input, func(event domain.EngineEvent) error {
		if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
			return err
		}
		switch event.Type {
		case "run.approval_required":
			request, ok := approvalRequestFromPayload(event.Payload)
			if !ok {
				return fmt.Errorf("%w: approval event has no request", domain.ErrInvalidInput)
			}
			run.Status = domain.RunStatusWaitingApproval
			run.PendingApproval = &request
			if err := s.saveActiveRun(run); err != nil {
				return err
			}
			if err := s.appendEvent(run, &sequence, event.Type, event.Payload); err != nil {
				return err
			}
			s.notifyApproval(run, request)
			// 先建立邏輯預約再歸還 gate，已排隊的其他 Run 才不會插入尚未完成的
			// assistant/tool 協定區段。
			s.pauseSession(session.ID, run.ID)
			release()
			release = nil
			return nil
		case "run.approval_resolved":
			if release == nil {
				newRelease, err := s.acquireSession(ctx, session.ID, run.ID)
				if err != nil {
					return err
				}
				release = newRelease
			}
			s.clearSessionPause(session.ID, run.ID)
			run.Status = domain.RunStatusRunning
			run.PendingApproval = nil
			if err := s.saveActiveRun(run); err != nil {
				return err
			}
			return s.appendEvent(run, &sequence, event.Type, event.Payload)
		default:
			if err := s.appendEvent(run, &sequence, event.Type, event.Payload); err != nil {
				return err
			}
			return s.waitIfRunPaused(ctx, run.ID)
		}
	})
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, domain.ErrCanceled) {
		s.saveRunUsage(&run, &result)
		s.finishCanceled(run, valueutil.FirstError(runErr, ctx.Err()), &sequence)
		return
	}
	if runErr != nil {
		s.saveRunUsage(&run, &result)
		code, retryable := classifyRunFailure(runErr)
		s.finishFailed(run, code, runErr, retryable, &sequence)
		return
	}
	s.saveRunUsage(&run, &result)
	if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
		s.finishCanceled(run, err, &sequence)
		return
	}

	completedAt := s.now().UTC()
	run.Status = domain.RunStatusCompleted
	run.Result = &result
	run.PendingApproval = nil
	if result.BudgetExceeded != nil {
		run.Metadata = valueutil.CloneMap(run.Metadata)
		if run.Metadata == nil {
			run.Metadata = map[string]any{}
		}
		run.Metadata["termination"] = "budget_exceeded"
		run.Metadata["budget_exceeded"] = *result.BudgetExceeded
	}
	run.CompletedAt = &completedAt
	// PauseRun 與 terminal transition 共用這把鎖，避免完成寫入後又被舊的
	// running 快照覆蓋成 paused。若 pause 先取得鎖，則在安全邊界等待恢復。
	for {
		paused, err := s.commitCompletedRun(run, &sequence)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.finishCanceled(run, err, &sequence)
			} else {
				s.finishFailed(run, "run_state_write_failed", err, true, &sequence)
			}
			return
		}
		if !paused {
			break
		}
		if err := s.waitIfRunPaused(ctx, run.ID); err != nil {
			s.finishCanceled(run, err, &sequence)
			return
		}
	}
	s.logger.Info("run completed", "run_id", run.ID, "session_id", run.SessionID, "duration_ms", completedAt.Sub(startedAt).Milliseconds(), "event_count", sequence)
	s.notifyRunFinished(run)
}

func classifyRunFailure(cause error) (code string, retryable bool) {
	switch {
	case errors.Is(cause, domain.ErrProviderProtocol):
		return "provider_protocol_incompatible", false
	case errors.Is(cause, domain.ErrInvalidInput):
		return "invalid_input", false
	default:
		return "agent_run_failed", true
	}
}

func (s *Service) appendEvent(run domain.Run, sequence *int64, eventType string, payload map[string]any) error {
	lock := s.eventLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	return s.appendEventLocked(run, sequence, eventType, payload)
}

// appendControlEvent 供 HTTP 控制操作使用；它會和 Harness 的事件寫入共用同一把鎖，
// 避免 pause/resume 與模型事件同時寫入時產生重複 sequence。
func (s *Service) appendControlEvent(run domain.Run, eventType string, payload map[string]any) error {
	lock := s.eventLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	value, _ := s.eventSequences.Load(run.ID)
	sequence, _ := value.(int64)
	if sequence == 0 {
		sequence = s.latestRunSequence(context.Background(), run.ID)
	}
	return s.appendEventLocked(run, &sequence, eventType, payload)
}

func (s *Service) appendEventLocked(run domain.Run, sequence *int64, eventType string, payload map[string]any) error {
	// 取消會先落盤再寫 terminal event；若底層 Provider 在取消後仍送出回呼，
	// 不得讓 late event 排到 run.canceled 之後，破壞事件串流的終止語意。
	//
	// 讀不到 Run 記錄時跳過這道檢查，而不是讓事件寫入失敗：去重是「盡量不要寫出
	// 重複的終止事件」的保護，不是正確性不變量。把它升級成硬失敗，等於讓一次暫時性
	// 的讀取失敗殺掉一個本來健康的 run——事件是診斷通道，run 才是工作本身。
	// 記一筆 warn，讓「這一輪沒有做去重」看得出來。
	if latest, err := s.runs.Get(context.Background(), run.ID); err != nil {
		s.logger.Warn("run record unreadable while appending event; terminal-event dedupe skipped",
			"run_id", run.ID, "event_type", eventType, "error", err)
	} else if terminalRun(latest.Status) && eventType != "run."+string(latest.Status) {
		return fmt.Errorf("%w: cannot append %s after run.%s", domain.ErrConflict, eventType, latest.Status)
	}
	value, _ := s.eventSequences.Load(run.ID)
	if latest, _ := value.(int64); latest > *sequence {
		*sequence = latest
	}
	next := *sequence + 1
	event := domain.Event{
		SchemaVersion: domain.EventSchemaVersion,
		ID:            domain.NewID("evt"),
		Type:          eventType,
		AgentID:       run.AgentID,
		SessionID:     run.SessionID,
		RunID:         run.ID,
		Sequence:      next,
		CreatedAt:     s.now().UTC(),
		Payload:       valueutil.CloneMap(payload),
	}
	if err := s.events.Append(context.Background(), event); err != nil {
		return err
	}
	*sequence = next
	s.eventSequences.Store(run.ID, next)
	s.notifyRun(run.ID)
	return nil
}

func (s *Service) finishCanceled(run domain.Run, cause error, sequence *int64) {
	s.logger.Info("run canceled", "run_id", run.ID, "session_id", run.SessionID, "cause", cause)
	completedAt := s.now().UTC()
	run.Status = domain.RunStatusCanceled
	run.Result = nil
	run.Error = &domain.RunError{Code: "run_canceled", Message: "run canceled", Retryable: true}
	if cause != nil {
		run.Error.Message = cause.Error()
	}
	run.CompletedAt = &completedAt
	s.finishTerminalRun(run, sequence)
}

func (s *Service) finishFailed(run domain.Run, code string, cause error, retryable bool, sequence *int64) {
	s.logger.Error("run failed", "run_id", run.ID, "session_id", run.SessionID, "code", code, "retryable", retryable, "error", cause)
	completedAt := s.now().UTC()
	run.Status = domain.RunStatusFailed
	run.Result = nil
	run.Error = &domain.RunError{Code: code, Message: cause.Error(), Retryable: retryable}
	run.CompletedAt = &completedAt
	s.finishTerminalRun(run, sequence)
}

// reconcileTerminalEvents 補齊伺服器重啟或先前事件寫入失敗留下的終止事件，
// 讓任何 terminal Run 的 durable event log 都能自行說明最終狀態。
func (s *Service) reconcileTerminalEvents(ctx context.Context) error {
	runs, err := s.runs.List(ctx, "")
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !terminalRun(run.Status) {
			continue
		}
		values, err := s.events.List(ctx, run.ID, 0)
		if err != nil {
			s.logger.Error("run event log unavailable; isolating this run", "run_id", run.ID, "error", err)
			continue
		}
		if len(values) > 0 && terminalEvent(values[len(values)-1].Type) {
			s.eventSequences.Store(run.ID, values[len(values)-1].Sequence)
			continue
		}
		sequence := int64(0)
		if len(values) > 0 {
			sequence = values[len(values)-1].Sequence
		}
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
		s.logger.Warn("reconciling missing terminal event after restart", "run_id", run.ID, "status", run.Status)
		if err := s.appendEvent(run, &sequence, "run."+string(run.Status), payload); err != nil {
			s.logger.Error("could not reconcile terminal event; other runs remain available", "run_id", run.ID, "error", err)
			continue
		}
		if run.Error != nil && run.Error.Code == "server_restarted" {
			s.notifyRunFinished(run)
		}
	}
	return nil
}

func terminalEvent(eventType string) bool {
	return eventType == "run.completed" || eventType == "run.failed" || eventType == "run.canceled"
}

func terminalRun(status domain.RunStatus) bool {
	return status == domain.RunStatusCompleted || status == domain.RunStatusFailed || status == domain.RunStatusCanceled
}
