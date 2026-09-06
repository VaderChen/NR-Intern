package plans

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"strings"
	"testing"
	"time"
)

type stubLoops struct {
	plan        domain.Plan
	active      bool
	interrupted bool
	gotReason   string
	gotNote     string
}

func (s *stubLoops) ActivePlanLoop(context.Context, string) (domain.Plan, bool) {
	return s.plan, s.active
}

func (s *stubLoops) InterruptPlanLoop(_ context.Context, _, _ string, _ domain.PlanLoopStopReason, reason, note string) (domain.Plan, error) {
	s.interrupted = true
	s.gotReason, s.gotNote = reason, note
	updated, err := domain.InterruptPlanLoop(s.plan, domain.PlanLoopStopAgent, reason, note, time.Now())
	if err != nil {
		return domain.Plan{}, err
	}
	s.plan = updated
	return updated, nil
}

func loopingPlan(t *testing.T) domain.Plan {
	t.Helper()
	plan, err := domain.NewPlan("session_1", domain.CreatePlanInput{
		Title: "原任務", Objective: "原始目標",
		Steps: []domain.CreatePlanStepInput{{Title: "步驟一", Verification: "條件一"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new plan: %v", err)
	}
	if plan, err = domain.StartPlanLoop(plan, 3, time.Now()); err != nil {
		t.Fatalf("start loop: %v", err)
	}
	if plan, err = domain.BeginPlanLoopRound(plan, time.Now()); err != nil {
		t.Fatalf("begin round: %v", err)
	}
	return plan
}

func invocation(name string, arguments map[string]any) tools.Invocation {
	return tools.Invocation{
		Session: domain.Session{ID: "session_1"},
		Call:    domain.ToolCall{ID: "call_1", Name: name, Arguments: arguments},
	}
}

// 多輪期間換計畫是走鐘最直接的路徑：Agent 覺得原計畫不順就開一個新的，
// 多輪機制會很開心地繼續跑，跑的卻已經是另一個任務，而且輪數還在算。
func TestCreatePlanBlockedDuringLoop(t *testing.T) {
	repository, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	loops := &stubLoops{plan: loopingPlan(t), active: true}
	tool := &CreateTool{Repository: repository, Loops: loops}
	execution, err := tool.Execute(context.Background(), invocation("plan_create", map[string]any{
		"title": "改做別的", "steps": []any{map[string]any{"title": "新步驟", "verification": "新條件"}},
	}), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !execution.IsError {
		t.Fatal("多輪執行期間不該允許新建計畫")
	}
	// 錯誤訊息要告訴 Agent 兩條正當出路，否則它只會換個說法再試一次。
	for _, want := range []string{"plan_step_update", "plan_loop_interrupt"} {
		if !strings.Contains(execution.Content, want) {
			t.Fatalf("錯誤訊息應指出可行的替代做法 %q：%s", want, execution.Content)
		}
	}
}

// 沒有多輪在跑的時候，行為必須與原本完全相同。
func TestCreatePlanUnaffectedWithoutLoop(t *testing.T) {
	repository, err := filestore.NewPlanRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	tool := &CreateTool{Repository: repository, Loops: &stubLoops{active: false}}
	execution, _ := tool.Execute(context.Background(), invocation("plan_create", map[string]any{
		"title": "新計畫", "steps": []any{map[string]any{"title": "步驟", "verification": "條件"}},
	}), nil)
	if execution.IsError {
		t.Fatalf("沒有多輪時建立計畫應照常成功：%s", execution.Content)
	}
}

// 中斷必須帶著理由與交接：少了交接，續跑就退化成重新開始。
func TestInterruptToolRequiresReasonAndNote(t *testing.T) {
	loops := &stubLoops{plan: loopingPlan(t), active: true}
	tool := NewInterruptTool(loops)
	for _, arguments := range []map[string]any{
		{"reason": "卡住了"},
		{"state_note": "做到步驟一"},
		{"reason": " ", "state_note": " "},
	} {
		execution, _ := tool.Execute(context.Background(), invocation("plan_loop_interrupt", arguments), nil)
		if !execution.IsError {
			t.Fatalf("缺少必填欄位應被擋下：%v", arguments)
		}
	}
	if loops.interrupted {
		t.Fatal("驗證失敗時不該真的中斷")
	}
}

// 正常中斷要把理由與交接原樣傳下去，並回報還剩幾輪。
func TestInterruptToolPassesReasonAndNote(t *testing.T) {
	loops := &stubLoops{plan: loopingPlan(t), active: true}
	tool := NewInterruptTool(loops)
	execution, err := tool.Execute(context.Background(), invocation("plan_loop_interrupt", map[string]any{
		"reason": "需要使用者確認資料庫連線", "state_note": "已完成步驟一的前置檢查",
	}), nil)
	if err != nil || execution.IsError {
		t.Fatalf("中斷不該失敗：%+v %v", execution, err)
	}
	if loops.gotReason != "需要使用者確認資料庫連線" || loops.gotNote != "已完成步驟一的前置檢查" {
		t.Fatalf("理由與交接應原樣傳遞：%q / %q", loops.gotReason, loops.gotNote)
	}
	if !strings.Contains(execution.Content, "\"resumable\": true") {
		t.Fatalf("中斷後應可續跑：%s", execution.Content)
	}
}

// 沒有多輪時呼叫中斷要講清楚，而不是靜默成功讓 Agent 以為停掉了。
func TestInterruptToolWithoutActiveLoop(t *testing.T) {
	tool := NewInterruptTool(&stubLoops{active: false})
	execution, _ := tool.Execute(context.Background(), invocation("plan_loop_interrupt", map[string]any{
		"reason": "想停", "state_note": "做到一半",
	}), nil)
	if !execution.IsError {
		t.Fatal("沒有多輪在跑時應回報錯誤")
	}
}

// 使用者說「跑五輪」時，計畫的內容與執行次數是兩件事：
// 前者照工作本身的結構拆，後者由使用者在計畫建立之後設定。
// 這個分工只寫在 plan_create 的說明裡——Agent 設計計畫時只讀得到那段。
func TestCreatePlanDescriptionSeparatesContentFromRepeatCount(t *testing.T) {
	description := (&CreateTool{}).Definition().Description
	for _, want := range []string{"先規劃內容，再談次數", "LOOP", "與步驟數無關"} {
		if !strings.Contains(description, want) {
			t.Fatalf("plan_create 的說明應交代兩段式分工 %q：%s", want, description)
		}
	}
	// 舊的做法是直接禁止某種命名；那會誤傷使用者自己的領域用語（例如
	// 輸入法演算法的「五輪修正」），已經移除，不要再回來。
	if strings.Contains(description, "不要命名為") {
		t.Fatalf("不應再以禁止命名的方式處理：%s", description)
	}
}
