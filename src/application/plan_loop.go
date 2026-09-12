package application

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"strings"
)

// 多輪執行的編排。
//
// 一輪 = 針對該 Session 的一次 Run。不做成「一個 Run 內部跑很多輪」：既有機制
// 全都假設 Run 有界——核准、取消、Context 整理、hasActiveSession 都以一次 Run
// 為單位。而且每輪一個 Run，才有天然的邊界去做真正重要的事：重新組裝輸入、
// 把 Agent 錨回同一個任務。作法比照排程執行器 startScheduleRun。

// StartPlanLoop 由使用者啟動多輪，並立刻送出第一輪。
func (s *Service) StartPlanLoop(ctx context.Context, sessionID, planID string, maxRounds int) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	plan, session, err := s.prepareLoopMutation(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	// 鎖定計畫時 Agent 不能改計畫，多輪也就沒有意義。
	if session.LockPlans {
		return domain.Plan{}, fmt.Errorf("%w: 這個對話鎖定了計畫，無法啟動 LOOP", domain.ErrConflict)
	}
	started, err := s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		return domain.StartPlanLoop(current, maxRounds, s.now())
	})
	if err != nil {
		return domain.Plan{}, err
	}
	return s.startPlanLoopRound(ctx, started)
}

// ResumePlanLoop 從檢查點的下一輪接續。由使用者發起，與「只有使用者能啟動」一致。
func (s *Service) ResumePlanLoop(ctx context.Context, sessionID, planID string) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	plan, session, err := s.prepareLoopMutation(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	if session.LockPlans {
		return domain.Plan{}, fmt.Errorf("%w: 這個對話鎖定了計畫，無法續跑 LOOP", domain.ErrConflict)
	}
	resumed, err := s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		return domain.ResumePlanLoop(current, s.now())
	})
	if err != nil {
		return domain.Plan{}, err
	}
	return s.startPlanLoopRound(ctx, resumed)
}

// StopPlanLoop 由使用者終結多輪，不留續跑餘地。
func (s *Service) StopPlanLoop(ctx context.Context, sessionID, planID, reason string) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	plan, err := s.loopPlan(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "使用者停止"
	}
	stopped, err := s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		return domain.StopPlanLoop(current, domain.PlanLoopStopUser, reason, s.now())
	})
	if err != nil {
		return domain.Plan{}, err
	}
	return stopped, s.cancelSessionRuns(context.WithoutCancel(ctx), sessionID)
}

// InterruptPlanLoop 立刻中止當前這一輪並留下檢查點。
//
// Agent 透過 plan_loop_interrupt 走這條路；使用者的「暫停」也是同一條。
// 語意是「現在就停」，所以要真的把正在跑的 Run 取消掉。
func (s *Service) InterruptPlanLoop(ctx context.Context, sessionID, planID string, by domain.PlanLoopStopReason, reason, note string) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	plan, err := s.loopPlan(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	// 原子讀取最新步驟並保存檢查點，再取消 Run；不得覆寫同期工具更新。
	saved, err := s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		return domain.InterruptPlanLoop(current, by, reason, note, s.now())
	})
	if err != nil {
		return domain.Plan{}, err
	}
	return saved, s.cancelSessionRuns(context.WithoutCancel(ctx), sessionID)
}

// ActivePlanLoop 回傳這個 Session 目前正在多輪執行的計畫。
//
// 供 plan_create 判斷要不要擋下：多輪期間換計畫是走鐘最直接的路徑——
// Agent 覺得原計畫不順就開一個新的，多輪機制會很開心地繼續跑，
// 跑的卻已經是另一個任務，而且輪數還在算。
func (s *Service) ActivePlanLoop(ctx context.Context, sessionID string) (domain.Plan, bool) {
	values, err := s.plans.List(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return domain.Plan{}, false
	}
	for _, value := range values {
		if value.Loop.Active() {
			return value, true
		}
	}
	return domain.Plan{}, false
}

// startPlanLoopRound 把輪數加一並送出這一輪的 Run。
func (s *Service) startPlanLoopRound(ctx context.Context, plan domain.Plan) (domain.Plan, error) {
	// 呼叫端持有 startMu：計畫啟停、一般 Run 啟動與自動續跑不會交錯。
	briefPlan, err := domain.BeginPlanLoopRound(plan, s.now())
	if err != nil {
		return plan, err
	}
	saved := plan
	_, err = s.startRunLocked(ctx, domain.RunInput{
		SessionID: plan.SessionID,
		UserInput: domain.PlanRoundBrief(briefPlan),
	}, func(run domain.Run) error {
		var updateErr error
		saved, updateErr = s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
			if s.hasActiveSession(current.SessionID) {
				return domain.Plan{}, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
			}
			begun, err := domain.BeginPlanLoopRound(current, s.now())
			if err != nil {
				return domain.Plan{}, err
			}
			begun.Loop.RoundRunID = run.ID
			return begun, nil
		})
		return updateErr
	})
	if err == nil {
		return saved, nil
	}
	// HTTP context 取消也要收尾；儲存失敗則明確回報，不吞掉第二個錯誤。
	paused, pauseErr := s.plans.Mutate(context.WithoutCancel(ctx), plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		return domain.InterruptPlanLoop(current, domain.PlanLoopStopRunFailed, "無法送出這一次："+err.Error(), "", s.now())
	})
	if pauseErr != nil {
		return saved, errors.Join(err, fmt.Errorf("保存 LOOP 暫停狀態失敗: %w", pauseErr))
	}
	return paused, err
}

// advancePlanLoop 在一輪的 Run 結束後決定要不要開下一輪。
//
// 掛在 executeRun 的 defer 上，且排在 clearActive 之後執行——下一輪要通過
// hasActiveSession 的檢查，前一輪必須已經從 active 移除。
func (s *Service) advancePlanLoop(runID string) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	ctx := context.Background()
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		s.logger.Warn("could not read finished run for plan loop", "run_id", runID, "error", err)
		return
	}
	plan, found := s.planForLoopRun(ctx, run.SessionID, runID)
	if !found {
		return
	}
	continueLoop := false
	updated, err := s.plans.Mutate(ctx, plan.SessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
		if !current.Loop.Active() || current.Loop.RoundRunID != runID {
			return domain.Plan{}, fmt.Errorf("%w: plan loop round changed", domain.ErrConflict)
		}
		if run.Status != domain.RunStatusCompleted {
			reason := "這一次的 Run 未正常結束（" + string(run.Status) + "）"
			return domain.InterruptPlanLoop(current, domain.PlanLoopStopRunFailed, reason, "", s.now())
		}
		progressed := domain.PlanStepFingerprint(current) != current.Loop.RoundFingerprint
		evaluated, decision := domain.EvaluatePlanLoopAfterRound(current, progressed, s.now())
		continueLoop = decision.Continue
		return evaluated, nil
	})
	if err != nil {
		s.logger.Error("could not persist plan loop completion", "plan_id", plan.ID, "run_id", runID, "error", err)
		return
	}
	if !continueLoop {
		s.logger.Info("plan loop finished", "plan_id", updated.ID, "session_id", updated.SessionID,
			"round", updated.Loop.Round, "stopped_by", updated.Loop.StoppedBy, "reason", updated.Loop.StoppedReason)
		return
	}
	if _, err := s.startPlanLoopRound(ctx, updated); err != nil {
		s.logger.Warn("could not start next plan loop round",
			"plan_id", updated.ID, "session_id", updated.SessionID, "error", err)
	}
}

// cancelSessionRuns 取消該 Session 目前還在跑的 Run；呼叫端持有 startMu。
func (s *Service) cancelSessionRuns(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for runID, item := range s.active {
		if item.sessionID == sessionID {
			ids = append(ids, runID)
		}
	}
	s.mu.Unlock()
	var cancelErr error
	for _, runID := range ids {
		if _, err := s.cancelRunLocked(ctx, runID); err != nil {
			s.logger.Warn("could not cancel run for plan loop", "run_id", runID, "error", err)
			cancelErr = errors.Join(cancelErr, err)
		}
	}
	return cancelErr
}

// planForLoopRun 找出這個 Run 屬於哪一輪的哪個計畫。
//
// 只認 Loop.RoundRunID：已經被中斷或停止的多輪不會匹配，因此那個原因不會被
// 系統判定覆寫——它比任何自動判定都更能解釋現況。
func (s *Service) planForLoopRun(ctx context.Context, sessionID, runID string) (domain.Plan, bool) {
	values, err := s.plans.List(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		s.logger.Error("could not find plan loop for finished run", "run_id", runID, "error", err)
		return domain.Plan{}, false
	}
	for _, value := range values {
		if value.Loop.Active() && value.Loop.RoundRunID == runID {
			return value, true
		}
	}
	return domain.Plan{}, false
}

func (s *Service) loopPlan(ctx context.Context, sessionID, planID string) (domain.Plan, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	return s.plans.Get(ctx, session.ID, strings.TrimSpace(planID))
}

// prepareLoopMutation 是啟動與續跑共用的前置檢查。
//
// 這裡刻意不用 preparePlanMutation：那個會擋掉「有 Run 正在跑」，
// 但多輪的每一輪本來就是 Run，續跑時上一輪剛結束、狀態可能還沒完全落定。
func (s *Service) prepareLoopMutation(ctx context.Context, sessionID, planID string) (domain.Plan, domain.Session, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Plan{}, domain.Session{}, err
	}
	if s.hasActiveSession(session.ID) {
		return domain.Plan{}, domain.Session{}, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	if err := s.reconcileSessionPlanLoops(ctx, session.ID); err != nil {
		return domain.Plan{}, domain.Session{}, err
	}
	plan, err := s.plans.Get(ctx, session.ID, strings.TrimSpace(planID))
	if err != nil {
		return domain.Plan{}, domain.Session{}, err
	}
	return plan, session, nil
}

// 啟動只修復狀態，不自動重送前一輪，也不重設已消耗的輪數。
func (s *Service) reconcilePlanLoops(ctx context.Context) {
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			s.logger.Error("could not inspect plan loops after restart", "error", err)
			continue
		}
		for _, session := range sessions {
			if err := s.reconcileSessionPlanLoops(ctx, session.ID); err != nil {
				s.logger.Error("could not recover plan loop", "session_id", session.ID, "error", err)
			}
		}
	}
}

func (s *Service) reconcileSessionPlanLoops(ctx context.Context, sessionID string) error {
	if s.hasActiveSession(sessionID) {
		return nil
	}
	plans, err := s.plans.List(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if !plan.Loop.Active() {
			continue
		}
		run, runErr := s.runs.Get(ctx, plan.Loop.RoundRunID)
		if runErr != nil && !errors.Is(runErr, domain.ErrNotFound) && plan.Loop.RoundRunID != "" {
			return runErr
		}
		_, err := s.plans.Mutate(ctx, sessionID, plan.ID, func(current domain.Plan) (domain.Plan, error) {
			if !current.Loop.Active() || current.Loop.RoundRunID != plan.Loop.RoundRunID {
				return current, nil
			}
			if runErr == nil && run.Status == domain.RunStatusCompleted && domain.PlanIsTerminal(current) {
				reason := domain.PlanLoopStopCompleted
				if current.Status != domain.PlanStatusCompleted {
					reason = domain.PlanLoopStopIncomplete
				}
				return domain.StopPlanLoop(current, reason, "恢復時確認計畫已結束", s.now())
			}
			reason := "上一輪已無執行者；可能因程式重啟或收尾中斷。請確認已產生的結果後續跑。"
			note := "保留既有步驟、證據與輪數；未知工具結果不得直接重送。"
			if current.Loop.Checkpoint != nil && current.Loop.Checkpoint.Note != "" {
				note += "\n" + current.Loop.Checkpoint.Note
			}
			return domain.InterruptPlanLoop(current, domain.PlanLoopStopRunFailed, reason, note, s.now())
		})
		if err != nil {
			return err
		}
	}
	return nil
}
