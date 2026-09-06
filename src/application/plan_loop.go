package application

import (
	"AgenticService/src/domain"
	"context"
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
	plan, session, err := s.prepareLoopMutation(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	// 鎖定計畫時 Agent 不能改計畫，多輪也就沒有意義。
	if session.LockPlans {
		return domain.Plan{}, fmt.Errorf("%w: 這個對話鎖定了計畫，無法啟動 LOOP", domain.ErrConflict)
	}
	started, err := domain.StartPlanLoop(plan, maxRounds, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	if started, err = s.plans.Update(ctx, started); err != nil {
		return domain.Plan{}, err
	}
	return s.startPlanLoopRound(ctx, started)
}

// ResumePlanLoop 從檢查點的下一輪接續。由使用者發起，與「只有使用者能啟動」一致。
func (s *Service) ResumePlanLoop(ctx context.Context, sessionID, planID string) (domain.Plan, error) {
	plan, _, err := s.prepareLoopMutation(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	resumed, err := domain.ResumePlanLoop(plan, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	if resumed, err = s.plans.Update(ctx, resumed); err != nil {
		return domain.Plan{}, err
	}
	return s.startPlanLoopRound(ctx, resumed)
}

// StopPlanLoop 由使用者終結多輪，不留續跑餘地。
func (s *Service) StopPlanLoop(ctx context.Context, sessionID, planID, reason string) (domain.Plan, error) {
	plan, err := s.loopPlan(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "使用者停止"
	}
	stopped, err := domain.StopPlanLoop(plan, domain.PlanLoopStopUser, reason, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	s.cancelSessionRuns(ctx, sessionID)
	return s.plans.Update(ctx, stopped)
}

// InterruptPlanLoop 立刻中止當前這一輪並留下檢查點。
//
// Agent 透過 plan_loop_interrupt 走這條路；使用者的「暫停」也是同一條。
// 語意是「現在就停」，所以要真的把正在跑的 Run 取消掉。
func (s *Service) InterruptPlanLoop(ctx context.Context, sessionID, planID string, by domain.PlanLoopStopReason, reason, note string) (domain.Plan, error) {
	plan, err := s.loopPlan(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	interrupted, err := domain.InterruptPlanLoop(plan, by, reason, note, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	// 先寫入檢查點再取消 Run：反過來的話，取消觸發的收尾會看到還在 running
	// 的多輪，而去開下一輪。
	saved, err := s.plans.Update(ctx, interrupted)
	if err != nil {
		return domain.Plan{}, err
	}
	s.cancelSessionRuns(ctx, sessionID)
	return saved, nil
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
	begun, err := domain.BeginPlanLoopRound(plan, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	// 先把輪數寫進儲存再送 Run：Run 送出後才寫的話，程序在中間死掉就會少算一輪，
	// 而少算的那一輪確實已經花掉了成本。
	saved, err := s.plans.Update(ctx, begun)
	if err != nil {
		return domain.Plan{}, err
	}
	// 輸入完全由後端從儲存組裝，不含模型上一輪的任何複述——那正是走鐘的傳染途徑。
	run, err := s.StartRun(ctx, domain.RunInput{
		SessionID: saved.SessionID,
		UserInput: domain.PlanRoundBrief(saved),
	})
	if err != nil {
		// 送不出去就把這一輪標記為暫停，讓使用者看得到原因並自行續跑，
		// 而不是留下一個看起來在跑、其實不會前進的多輪。
		failed, interruptErr := domain.InterruptPlanLoop(saved, domain.PlanLoopStopRunFailed,
			"無法送出這一次："+err.Error(), "", s.now())
		if interruptErr == nil {
			if updated, updateErr := s.plans.Update(ctx, failed); updateErr == nil {
				return updated, err
			}
		}
		return saved, err
	}
	// 記下這一輪對應的 Run，收尾時才認得出「這是多輪的一輪」。
	loop := *saved.Loop
	loop.RoundRunID = run.ID
	saved.Loop = &loop
	if updated, updateErr := s.plans.Update(ctx, saved); updateErr == nil {
		saved = updated
	}
	return saved, nil
}

// advancePlanLoop 在一輪的 Run 結束後決定要不要開下一輪。
//
// 掛在 executeRun 的 defer 上，且排在 clearActive 之後執行——下一輪要通過
// hasActiveSession 的檢查，前一輪必須已經從 active 移除。
func (s *Service) advancePlanLoop(runID string) {
	ctx := context.Background()
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return
	}
	plan, found := s.planForLoopRun(ctx, run.SessionID, runID)
	if !found {
		return
	}
	if run.Status != domain.RunStatusCompleted {
		reason := "這一次的 Run 未正常結束（" + string(run.Status) + "）"
		paused, interruptErr := domain.InterruptPlanLoop(plan, domain.PlanLoopStopRunFailed, reason, "", s.now())
		if interruptErr == nil {
			_, _ = s.plans.Update(ctx, paused)
		}
		return
	}

	// 推進與否用「步驟狀態或證據有沒有變」判定，不看輸出長度——
	// 「我來確認一下」寫得再長也不是推進。
	progressed := domain.PlanStepFingerprint(plan) != plan.Loop.RoundFingerprint
	evaluated, decision := domain.EvaluatePlanLoopAfterRound(plan, progressed, s.now())
	updated, err := s.plans.Update(ctx, evaluated)
	if err != nil {
		return
	}
	if !decision.Continue {
		s.logger.Info("plan loop finished",
			"plan_id", updated.ID, "session_id", updated.SessionID,
			"round", updated.Loop.Round, "stopped_by", updated.Loop.StoppedBy, "reason", updated.Loop.StoppedReason)
		return
	}
	if _, err := s.startPlanLoopRound(ctx, updated); err != nil {
		s.logger.Warn("could not start next plan loop round",
			"plan_id", updated.ID, "session_id", updated.SessionID, "error", err)
	}
}

// cancelSessionRuns 取消該 Session 目前還在跑的 Run。
func (s *Service) cancelSessionRuns(ctx context.Context, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for runID, item := range s.active {
		if item.sessionID == sessionID {
			ids = append(ids, runID)
		}
	}
	s.mu.Unlock()
	for _, runID := range ids {
		if _, err := s.CancelRun(ctx, runID); err != nil {
			s.logger.Warn("could not cancel run for plan loop", "run_id", runID, "error", err)
		}
	}
}

// planForLoopRun 找出這個 Run 屬於哪一輪的哪個計畫。
//
// 只認 Loop.RoundRunID：已經被中斷或停止的多輪不會匹配，因此那個原因不會被
// 系統判定覆寫——它比任何自動判定都更能解釋現況。
func (s *Service) planForLoopRun(ctx context.Context, sessionID, runID string) (domain.Plan, bool) {
	values, err := s.plans.List(ctx, strings.TrimSpace(sessionID))
	if err != nil {
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
	plan, err := s.plans.Get(ctx, session.ID, strings.TrimSpace(planID))
	if err != nil {
		return domain.Plan{}, domain.Session{}, err
	}
	return plan, session, nil
}
