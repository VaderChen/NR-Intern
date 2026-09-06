package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/ports"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// loopTestService 用真實的計畫與 Run 儲存：編排會從 runs.Get 讀回這一輪的結果，
// 而測試替身的 Get 一律回 ErrNotFound，接不上。
func loopTestService(t *testing.T, run func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error)) (*Service, *fakeEngine) {
	t.Helper()
	engine := newFakeEngine()
	engine.run = run
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	directory := t.TempDir()
	plans, err := filestore.NewPlanRepository(directory)
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	runs, err := filestore.NewRunRepository(directory)
	if err != nil {
		t.Fatalf("NewRunRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry:    registry,
		Runs:        runs,
		Events:      fakeEvents{},
		Projects:    fakeProjects{},
		Workspaces:  fakeWorkspaces{},
		Providers:   fakeProviders{},
		Plans:       plans,
		Permissions: lockedPolicy(),
		Logger:      logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return service, engine
}

func loopSessionWithPlan(t *testing.T, service *Service) (domain.Session, domain.Plan) {
	t.Helper()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	plan, err := service.CreatePlan(ctx, session.ID, domain.CreatePlanInput{
		Title: "多輪任務", Objective: "把儲存層搬離硬碟",
		Steps: []domain.CreatePlanStepInput{
			{Title: "步驟一", Verification: "條件一"},
			{Title: "步驟二", Verification: "條件二"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	return session, plan
}

// waitForLoop 等編排跑完。每一輪是非同步的 Run，收尾掛在 executeRun 的 defer 上。
func waitForLoop(t *testing.T, service *Service, sessionID, planID string, want func(domain.Plan) bool) domain.Plan {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var latest domain.Plan
	for time.Now().Before(deadline) {
		value, err := service.plans.Get(context.Background(), sessionID, planID)
		if err == nil {
			latest = value
			if want(value) {
				return value
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待逾時，計畫目前狀態：%+v", latest.Loop)
	return latest
}

// 這是編排的核心：一輪結束後要自動開下一輪，而且每一輪的輸入都逐字帶著原始目標。
// 領域測試守得住輪數與預算，但「Run 結束後真的會接下一輪」只有這裡測得到。
func TestPlanLoopRunsMultipleRoundsAndAnchorsEachOne(t *testing.T) {
	var mutex sync.Mutex
	inputs := []string{}
	service, _ := loopTestService(t, func(_ context.Context, input domain.RunInput, _ ports.AgentEventSink) (domain.RunResult, error) {
		mutex.Lock()
		inputs = append(inputs, input.UserInput)
		mutex.Unlock()
		return domain.RunResult{Message: domain.Message{Role: "assistant", Content: "這一輪做完了"}}, nil
	})
	session, plan := loopSessionWithPlan(t, service)

	if _, err := service.StartPlanLoop(context.Background(), session.ID, plan.ID, 2); err != nil {
		t.Fatalf("StartPlanLoop: %v", err)
	}
	final := waitForLoop(t, service, session.ID, plan.ID, func(value domain.Plan) bool {
		return value.Loop != nil && value.Loop.Status == domain.PlanLoopStatusStopped
	})

	if final.Loop.Round != 2 {
		t.Fatalf("設定 2 輪就該跑滿 2 輪，得到 %d", final.Loop.Round)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("應送出 2 輪，實際 %d 輪：%v", len(inputs), inputs)
	}
	for index, input := range inputs {
		// 每一輪都要重新錨定，而不是只有第一輪。
		if !strings.Contains(input, "把儲存層搬離硬碟") {
			t.Fatalf("第 %d 輪的輸入沒有帶原始目標：%s", index+1, input)
		}
		// 不含模型上一輪的輸出——複述是走鐘的傳染途徑。
		if strings.Contains(input, "這一輪做完了") {
			t.Fatalf("第 %d 輪的輸入混入了上一輪的模型輸出：%s", index+1, input)
		}
	}
	if !strings.Contains(inputs[1], "第 2 輪") {
		t.Fatalf("第 2 輪應告知輪次：%s", inputs[1])
	}
}

// 完全沒有推進時要在空轉門檻停下，而且說明是空轉而非跑完。
func TestPlanLoopStopsOnIdleRoundsBeforeUsingBudget(t *testing.T) {
	service, _ := loopTestService(t, func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		return domain.RunResult{Message: domain.Message{Role: "assistant", Content: "我來確認一下"}}, nil
	})
	session, plan := loopSessionWithPlan(t, service)

	if _, err := service.StartPlanLoop(context.Background(), session.ID, plan.ID, 10); err != nil {
		t.Fatalf("StartPlanLoop: %v", err)
	}
	final := waitForLoop(t, service, session.ID, plan.ID, func(value domain.Plan) bool {
		return value.Loop != nil && value.Loop.Status == domain.PlanLoopStatusStopped
	})
	if final.Loop.StoppedBy != domain.PlanLoopStopNoProgress {
		t.Fatalf("應標示為空轉停止，得到 %q（%s）", final.Loop.StoppedBy, final.Loop.StoppedReason)
	}
	// 設定 10 輪卻在門檻就停：空轉不該把預算燒完。
	if final.Loop.Round > domain.PlanLoopIdleLimit {
		t.Fatalf("空轉應在第 %d 輪停下，卻跑到第 %d 輪", domain.PlanLoopIdleLimit, final.Loop.Round)
	}
}

// Agent 中斷後不該再開下一輪，而且檢查點要留得下來讓使用者續跑。
func TestPlanLoopAgentInterruptStopsFurtherRounds(t *testing.T) {
	var mutex sync.Mutex
	rounds := 0
	var service *Service
	var session domain.Session
	var plan domain.Plan
	service, _ = loopTestService(t, func(ctx context.Context, _ domain.RunInput, _ ports.AgentEventSink) (domain.RunResult, error) {
		mutex.Lock()
		rounds++
		current := rounds
		mutex.Unlock()
		if current == 1 {
			// 模擬 Agent 在第一輪呼叫 plan_loop_interrupt。
			if _, err := service.InterruptPlanLoop(ctx, session.ID, plan.ID,
				domain.PlanLoopStopAgent, "需要使用者確認", "已完成步驟一的前置檢查"); err != nil {
				t.Errorf("InterruptPlanLoop: %v", err)
			}
		}
		return domain.RunResult{Message: domain.Message{Role: "assistant", Content: "停在這裡"}}, nil
	})
	session, plan = loopSessionWithPlan(t, service)

	if _, err := service.StartPlanLoop(context.Background(), session.ID, plan.ID, 5); err != nil {
		t.Fatalf("StartPlanLoop: %v", err)
	}
	final := waitForLoop(t, service, session.ID, plan.ID, func(value domain.Plan) bool {
		return value.Loop != nil && value.Loop.Status == domain.PlanLoopStatusPaused
	})
	if final.Loop.StoppedBy != domain.PlanLoopStopAgent {
		t.Fatalf("停止原因應為 Agent 中斷，得到 %q", final.Loop.StoppedBy)
	}
	if final.Loop.Checkpoint == nil || final.Loop.Checkpoint.Note != "已完成步驟一的前置檢查" {
		t.Fatalf("交接內容應保留下來：%+v", final.Loop.Checkpoint)
	}
	time.Sleep(150 * time.Millisecond)
	mutex.Lock()
	defer mutex.Unlock()
	if rounds != 1 {
		t.Fatalf("中斷後不該再開下一輪，實際跑了 %d 輪", rounds)
	}
}
