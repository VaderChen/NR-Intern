package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func loopTestPlan(t *testing.T) Plan {
	t.Helper()
	plan, err := NewPlan("session_1", CreatePlanInput{
		Title:     "任務",
		Objective: "原始目標",
		Steps: []CreatePlanStepInput{
			{Title: "步驟一", Verification: "條件一"},
			{Title: "步驟二", Verification: "條件二"},
		},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	return plan
}

func startLoop(t *testing.T, plan Plan, rounds int) Plan {
	t.Helper()
	started, err := StartPlanLoop(plan, rounds, time.Now())
	if err != nil {
		t.Fatalf("start loop: %v", err)
	}
	return started
}

// 輪數是預算，超過上限就不能再開。這是使用者的帳單防線。
func TestPlanLoopCannotExceedMaxRounds(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 2)
	for round := 1; round <= 2; round++ {
		next, err := BeginPlanLoopRound(plan, time.Now())
		if err != nil {
			t.Fatalf("第 %d 輪應該可以開始：%v", round, err)
		}
		plan = next
		if plan.Loop.Round != round {
			t.Fatalf("輪數應為 %d，得到 %d", round, plan.Loop.Round)
		}
	}
	if _, err := BeginPlanLoopRound(plan, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("用完輪數後不該還能開新的一輪：%v", err)
	}
}

// 中斷的那一輪照樣計入，否則反覆中斷再續跑就能無限延長預算。
func TestPlanLoopInterruptedRoundStillCountsAgainstBudget(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 2)
	for round := 1; round <= 2; round++ {
		plan, _ = BeginPlanLoopRound(plan, time.Now())
		interrupted, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "先停一下", "做到步驟一的一半", time.Now())
		if err != nil {
			t.Fatalf("第 %d 輪中斷失敗：%v", round, err)
		}
		plan = interrupted
		if round == 1 {
			if plan, err = ResumePlanLoop(plan, time.Now()); err != nil {
				t.Fatalf("續跑失敗：%v", err)
			}
		}
	}
	if plan.Loop.Round != 2 {
		t.Fatalf("兩輪都中斷過，輪數仍應為 2，得到 %d", plan.Loop.Round)
	}
	if _, err := ResumePlanLoop(plan, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("用完輪數後不該還能續跑")
	}
}

// 續跑要從中斷的下一輪接續，不能歸零重來。
func TestPlanLoopResumeKeepsRoundCount(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 5)
	for round := 1; round <= 2; round++ {
		plan, _ = BeginPlanLoopRound(plan, time.Now())
	}
	plan, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "需要人工確認", "已完成前置檢查", time.Now())
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if plan.Loop.Checkpoint == nil || plan.Loop.Checkpoint.Round != 2 {
		t.Fatalf("檢查點應記下第 2 輪：%+v", plan.Loop.Checkpoint)
	}
	if plan.Loop.Checkpoint.Note != "已完成前置檢查" {
		t.Fatalf("交接內容應原樣保留：%q", plan.Loop.Checkpoint.Note)
	}
	plan, err = ResumePlanLoop(plan, time.Now())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	plan, err = BeginPlanLoopRound(plan, time.Now())
	if err != nil {
		t.Fatalf("續跑後的第一輪：%v", err)
	}
	if plan.Loop.Round != 3 {
		t.Fatalf("續跑應從第 3 輪開始，得到第 %d 輪", plan.Loop.Round)
	}
}

// Agent 中斷後不該被系統的判定覆寫：那個原因比「達到上限」更能解釋現況。
func TestPlanLoopKeepsAgentReasonOverSystemStop(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 1)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	plan, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "偵測到重複勞動", "步驟一已完成，步驟二待確認", time.Now())
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	evaluated, decision := EvaluatePlanLoopAfterRound(plan, false, time.Now())
	if decision.Continue {
		t.Fatal("中斷之後不該再開下一輪")
	}
	if evaluated.Loop.StoppedBy != PlanLoopStopAgent {
		t.Fatalf("停止原因應保留 Agent 的說法，得到 %q", evaluated.Loop.StoppedBy)
	}
	if evaluated.Loop.Status != PlanLoopStatusPaused {
		t.Fatalf("Agent 中斷應為可續跑的暫停，得到 %q", evaluated.Loop.Status)
	}
}

// 沒有推進就是空轉，連續達門檻要自動停止，而且說明是空轉不是完成。
func TestPlanLoopStopsAfterIdleRounds(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 10)
	var decision PlanLoopDecision
	for round := 1; round <= PlanLoopIdleLimit; round++ {
		plan, _ = BeginPlanLoopRound(plan, time.Now())
		plan, decision = EvaluatePlanLoopAfterRound(plan, false, time.Now())
	}
	if decision.Continue {
		t.Fatal("連續空轉達門檻後不該繼續")
	}
	if plan.Loop.StoppedBy != PlanLoopStopNoProgress {
		t.Fatalf("應標示為空轉停止，得到 %q", plan.Loop.StoppedBy)
	}
	if plan.Loop.Status != PlanLoopStatusStopped {
		t.Fatalf("空轉是終結而非暫停，得到 %q", plan.Loop.Status)
	}
}

// 有推進就把空轉計數歸零，否則偶爾一輪沒動就會被誤判。
func TestPlanLoopProgressResetsIdleCount(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 10)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	plan, _ = EvaluatePlanLoopAfterRound(plan, false, time.Now())
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	plan, decision := EvaluatePlanLoopAfterRound(plan, true, time.Now())
	if !decision.Continue {
		t.Fatal("有推進就該繼續")
	}
	if plan.Loop.IdleRounds != 0 {
		t.Fatalf("推進後空轉計數應歸零，得到 %d", plan.Loop.IdleRounds)
	}
}

// 完成的判定看計畫狀態，不看 Agent 說了什麼；且完成優先於上限與空轉。
func TestPlanLoopCompletionBeatsOtherStopReasons(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 1)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	for _, step := range plan.Steps {
		for _, status := range []PlanStepStatus{PlanStepStatusInProgress, PlanStepStatusVerifying, PlanStepStatusCompleted} {
			next, err := TransitionPlanStep(plan, step.ID, UpdatePlanStepInput{Status: status, Evidence: "已驗證"}, time.Now())
			if err != nil {
				t.Fatalf("transition %s: %v", status, err)
			}
			plan = next
		}
	}
	// 同時也達到了輪數上限，但記錄的原因應該是「完成」。
	plan, decision := EvaluatePlanLoopAfterRound(plan, true, time.Now())
	if decision.Continue {
		t.Fatal("完成後不該繼續")
	}
	if plan.Loop.StoppedBy != PlanLoopStopCompleted {
		t.Fatalf("完成應優先於上限，得到 %q", plan.Loop.StoppedBy)
	}
}

// 中斷必須留下可用的交接，否則續跑就退化成重新開始。
func TestPlanLoopAgentInterruptRequiresStateNote(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 3)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	if _, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "有理由", "  ", time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("缺少交接內容應被擋下：%v", err)
	}
	if _, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "  ", "有交接", time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("缺少理由應被擋下：%v", err)
	}
}

// 使用者設定超過硬上限要被擋下——手滑輸入 999 不該變成 999 輪。
func TestPlanLoopRejectsRoundsBeyondHardCap(t *testing.T) {
	if _, err := StartPlanLoop(loopTestPlan(t), MaxPlanLoopRounds+1, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超過硬上限應被擋下：%v", err)
	}
	plan, err := StartPlanLoop(loopTestPlan(t), 0, time.Now())
	if err != nil {
		t.Fatalf("未指定輪數應使用預設：%v", err)
	}
	if plan.Loop.MaxRounds != DefaultPlanLoopRounds {
		t.Fatalf("預設輪數應為 %d，得到 %d", DefaultPlanLoopRounds, plan.Loop.MaxRounds)
	}
}

// 指紋只看狀態與證據：標題被改寫不算推進，那正是走鐘的表現之一。
func TestPlanStepFingerprintIgnoresTitleRewrites(t *testing.T) {
	plan := loopTestPlan(t)
	before := PlanStepFingerprint(plan)
	plan.Steps[0].Title = "換個說法的步驟一"
	if PlanStepFingerprint(plan) != before {
		t.Fatal("改寫標題不該被當成推進")
	}
	advanced, err := TransitionPlanStep(plan, plan.Steps[0].ID, UpdatePlanStepInput{Status: PlanStepStatusInProgress}, time.Now())
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if PlanStepFingerprint(advanced) == before {
		t.Fatal("步驟狀態改變應被視為推進")
	}
}

// 每輪輸入是防走鐘的核心：目標要逐字出現，輪次要明確，
// 而且完成與中斷的規則要寫在裡面，Agent 才知道邊界在哪。
func TestPlanRoundBriefAnchorsOnStoredObjective(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 3)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	brief := PlanRoundBrief(plan)
	for _, want := range []string{"原始目標", "LOOP", "第 1 次／共 3 次", "步驟一", "條件一", "plan_loop_interrupt"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("每輪輸入應包含 %q：\n%s", want, brief)
		}
	}
}

// 續跑時要帶回中斷當下的交接，而且要明說「從這裡接續，不要重新開始」。
func TestPlanRoundBriefCarriesCheckpointNote(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 3)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	plan, err := InterruptPlanLoop(plan, PlanLoopStopAgent, "等待外部確認", "已跑完前置檢查，待確認資料庫連線", time.Now())
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	plan, _ = ResumePlanLoop(plan, time.Now())
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	brief := PlanRoundBrief(plan)
	if !strings.Contains(brief, "已跑完前置檢查，待確認資料庫連線") {
		t.Fatalf("交接內容應逐字帶回：\n%s", brief)
	}
	if !strings.Contains(brief, "不要重新開始") {
		t.Fatalf("續跑要明說接續而非重做：\n%s", brief)
	}
	if !strings.Contains(brief, "第 2 次") {
		t.Fatalf("續跑應為第 2 次：\n%s", brief)
	}
}

// 「第 N 輪」緊接著編號步驟清單，很容易被讀成「第 N 輪＝做第 N 步」。
// 實際觀察到的誤解正是這個：把計畫的 5 個步驟命名成「第 1～5 輪」。
func TestPlanRoundBriefSeparatesRoundsFromSteps(t *testing.T) {
	plan := startLoop(t, loopTestPlan(t), 3)
	plan, _ = BeginPlanLoopRound(plan, time.Now())
	brief := PlanRoundBrief(plan)
	if !strings.Contains(brief, "與步驟編號無關") {
		t.Fatalf("每次的輸入必須說明次數與步驟不是同一件事：\n%s", brief)
	}
	// 說明要出現在步驟清單之前，否則讀到清單時已經先建立了錯誤的對應。
	if strings.Index(brief, "與步驟編號無關") > strings.Index(brief, "計畫目前的狀態") {
		t.Fatalf("釐清要放在步驟清單之前：\n%s", brief)
	}
}
