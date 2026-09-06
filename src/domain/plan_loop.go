package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 多輪執行要解決的是「Agent 跑一半就忘了」，不是自動化便利。
//
// 反覆叫 Agent「繼續」的典型失敗不是它停下來，而是它繼續得很起勁卻在做別的事：
// 忘記原始目標、每輪重新鋪陳一次計畫、沒有證據就宣告完成。這些看起來都像在工作。
// 因此這裡的每個限制都指向同一件事——**任務的真相在計畫裡，不在模型的記憶裡**。

type PlanLoopStatus string

const (
	PlanLoopStatusRunning PlanLoopStatus = "running"
	// PlanLoopStatusPaused 留有檢查點，可以續跑；Stopped 則是結案。
	PlanLoopStatusPaused  PlanLoopStatus = "paused"
	PlanLoopStatusStopped PlanLoopStatus = "stopped"
)

// PlanLoopStopReason 記錄是什麼讓多輪停下來。使用者回頭看時要知道
// 「為什麼停在第 3 輪」，「已停止」這種空話沒有意義。
type PlanLoopStopReason string

const (
	PlanLoopStopAgent      PlanLoopStopReason = "agent"
	PlanLoopStopUser       PlanLoopStopReason = "user"
	PlanLoopStopMaxRounds  PlanLoopStopReason = "max_rounds"
	PlanLoopStopNoProgress PlanLoopStopReason = "no_progress"
	PlanLoopStopRunFailed  PlanLoopStopReason = "run_failed"
	PlanLoopStopCompleted  PlanLoopStopReason = "completed"
)

const (
	// DefaultPlanLoopRounds 是使用者沒指定時的輪數。
	DefaultPlanLoopRounds = 3
	// MaxPlanLoopRounds 是硬上限。存在的理由不是技術限制，而是使用者也可能
	// 手滑輸入 999——自動消耗成本的功能需要一個打不破的天花板。
	MaxPlanLoopRounds = 20
	// PlanLoopIdleLimit 是連續空轉幾輪就停。空轉以「步驟狀態或證據有沒有變」
	// 判定，不看輸出長度：「我來確認一下」寫得再長也不是推進。
	PlanLoopIdleLimit = 2
	// maxPlanCheckpointNote 限制交接內容長度，避免它膨脹成另一份對話紀錄。
	maxPlanCheckpointNote = 4000
)

// PlanCheckpoint 是中斷當下的狀態。
//
// 中斷會讓計畫停在寫到一半的狀態——這不是要避免的問題，而是要正式記錄的東西。
// 有了它，中斷才等於「隨時可以再開始」，而不是「前面白做了」。
type PlanCheckpoint struct {
	Round int `json:"round"`
	// StepID 是中斷當下進行到哪一步。續跑時要明確告訴 Agent「上次在這裡被中斷」，
	// 否則它看起來只是個普通的未完成步驟，該採取的行動不一樣。
	StepID string `json:"step_id,omitempty"`
	// Note 是 Agent 自述「我做到哪裡、下一步打算做什麼」。
	//
	// 這是整個設計裡唯一允許把模型輸出帶進下一輪的地方：它是 Agent 明知會被
	// 中斷而刻意留下的交接，不是隨手複述。其餘內容一律從儲存重新組裝。
	Note          string    `json:"note"`
	InterruptedAt time.Time `json:"interrupted_at"`
}

// PlanLoop 是計畫的多輪執行狀態。nil 代表這個計畫沒有啟用多輪。
type PlanLoop struct {
	// Round 記的是「已開始」而非「已完成」的輪數。中斷的那一輪照樣計入：
	// 它確實花掉了 token，而且若不算，Agent 只要反覆中斷就能無限延長預算。
	Round         int                `json:"round"`
	MaxRounds     int                `json:"max_rounds"`
	Status        PlanLoopStatus     `json:"status"`
	StoppedBy     PlanLoopStopReason `json:"stopped_by,omitempty"`
	StoppedReason string             `json:"stopped_reason,omitempty"`
	IdleRounds    int                `json:"idle_rounds"`
	// RoundRunID 與 RoundFingerprint 是當前這一輪的執行狀態。
	//
	// 刻意存在計畫上而不是 Run metadata：Run metadata 由呼叫端送入，
	// 後端得逐一清掉 Client 夾帶的同名值才安全；存在這裡則根本沒有偽造路徑，
	// 而且跟著 PlanRepository 的 ProjectRoots 路由，隔離專案自動比照辦理。
	RoundRunID       string          `json:"round_run_id,omitempty"`
	RoundFingerprint string          `json:"round_fingerprint,omitempty"`
	Checkpoint       *PlanCheckpoint `json:"checkpoint,omitempty"`
	StartedAt        time.Time       `json:"started_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Active 回報這個多輪是否還會再開下一輪。
func (loop *PlanLoop) Active() bool {
	return loop != nil && loop.Status == PlanLoopStatusRunning
}

// Resumable 回報這個多輪是否停在可以續跑的狀態。
func (loop *PlanLoop) Resumable() bool {
	return loop != nil && loop.Status == PlanLoopStatusPaused && loop.Round < loop.MaxRounds
}

// RoundsLeft 回傳還可以開幾輪。
func (loop *PlanLoop) RoundsLeft() int {
	if loop == nil || loop.Round >= loop.MaxRounds {
		return 0
	}
	return loop.MaxRounds - loop.Round
}

// StartPlanLoop 由**使用者**啟動多輪。
//
// Agent 不能啟動：plan_create 已經允許它建立計畫，若同時允許它啟動多輪，
// 等於它可以自行決定開始無人看管地花錢，與 Approval 機制的整個前提衝突。
func StartPlanLoop(plan Plan, maxRounds int, now time.Time) (Plan, error) {
	if err := ValidatePlan(plan); err != nil {
		return Plan{}, err
	}
	if plan.Status != PlanStatusActive {
		return Plan{}, fmt.Errorf("%w: plan is %s", ErrConflict, plan.Status)
	}
	if plan.Loop.Active() {
		return Plan{}, fmt.Errorf("%w: plan loop is already running", ErrConflict)
	}
	if maxRounds <= 0 {
		maxRounds = DefaultPlanLoopRounds
	}
	if maxRounds > MaxPlanLoopRounds {
		return Plan{}, fmt.Errorf("%w: plan loop is limited to %d rounds", ErrInvalidInput, MaxPlanLoopRounds)
	}
	now = now.UTC()
	plan.Loop = &PlanLoop{
		Round:     0,
		MaxRounds: maxRounds,
		Status:    PlanLoopStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}
	plan.UpdatedAt = now
	return plan, nil
}

// BeginPlanLoopRound 在送出一輪之前把輪數加一。
//
// 先加再跑，而不是跑完才加：中斷、當機、Run 失敗的那一輪都確實花了成本，
// 事後才計數的話，這些情況會憑空多出預算。
func BeginPlanLoopRound(plan Plan, now time.Time) (Plan, error) {
	if !plan.Loop.Active() {
		return Plan{}, fmt.Errorf("%w: plan loop is not running", ErrConflict)
	}
	if plan.Loop.Round >= plan.Loop.MaxRounds {
		return Plan{}, fmt.Errorf("%w: plan loop has used all %d rounds", ErrConflict, plan.Loop.MaxRounds)
	}
	now = now.UTC()
	loop := *plan.Loop
	loop.Round++
	// 記下這一輪開始時的步驟指紋，收尾時才比對得出有沒有推進。
	loop.RoundFingerprint = PlanStepFingerprint(plan)
	loop.RoundRunID = ""
	loop.UpdatedAt = now
	plan.Loop = &loop
	plan.UpdatedAt = now
	return plan, nil
}

// InterruptPlanLoop 立刻中止當前這一輪，並留下檢查點。
//
// 語意是「現在就停」，不是「跑完這輪再說」。呼叫端負責取消正在跑的 Run。
// note 必填：少了它，續跑就退化成重新開始，而重新開始正是這個功能要防的走鐘。
func InterruptPlanLoop(plan Plan, by PlanLoopStopReason, reason, note string, now time.Time) (Plan, error) {
	if plan.Loop == nil {
		return Plan{}, fmt.Errorf("%w: plan has no loop", ErrNotFound)
	}
	if plan.Loop.Status != PlanLoopStatusRunning {
		return Plan{}, fmt.Errorf("%w: plan loop is %s", ErrConflict, plan.Loop.Status)
	}
	reason = strings.TrimSpace(reason)
	note = strings.TrimSpace(note)
	if reason == "" {
		return Plan{}, fmt.Errorf("%w: interrupting a plan loop requires a reason", ErrInvalidInput)
	}
	if by == PlanLoopStopAgent && note == "" {
		return Plan{}, fmt.Errorf("%w: interrupting a plan loop requires a state note for resuming", ErrInvalidInput)
	}
	if len([]rune(note)) > maxPlanCheckpointNote {
		note = string([]rune(note)[:maxPlanCheckpointNote])
	}
	now = now.UTC()
	loop := *plan.Loop
	loop.Status = PlanLoopStatusPaused
	loop.StoppedBy = by
	loop.StoppedReason = reason
	loop.UpdatedAt = now
	loop.Checkpoint = &PlanCheckpoint{
		Round:         loop.Round,
		StepID:        plan.CurrentStepID,
		Note:          note,
		InterruptedAt: now,
	}
	plan.Loop = &loop
	plan.UpdatedAt = now
	return plan, nil
}

// ResumePlanLoop 從檢查點的下一輪接續。
//
// 輪數不歸零、不重來——歸零等於讓中斷成為繞過預算上限的後門。
func ResumePlanLoop(plan Plan, now time.Time) (Plan, error) {
	if plan.Loop == nil {
		return Plan{}, fmt.Errorf("%w: plan has no loop", ErrNotFound)
	}
	if plan.Loop.Status != PlanLoopStatusPaused {
		return Plan{}, fmt.Errorf("%w: plan loop is %s", ErrConflict, plan.Loop.Status)
	}
	if plan.Status != PlanStatusActive {
		return Plan{}, fmt.Errorf("%w: plan is %s", ErrConflict, plan.Status)
	}
	if plan.Loop.Round >= plan.Loop.MaxRounds {
		return Plan{}, fmt.Errorf("%w: plan loop has used all %d rounds", ErrConflict, plan.Loop.MaxRounds)
	}
	now = now.UTC()
	loop := *plan.Loop
	loop.Status = PlanLoopStatusRunning
	loop.StoppedBy = ""
	loop.StoppedReason = ""
	loop.UpdatedAt = now
	plan.Loop = &loop
	plan.UpdatedAt = now
	return plan, nil
}

// StopPlanLoop 結束多輪，不留續跑的餘地。
func StopPlanLoop(plan Plan, by PlanLoopStopReason, reason string, now time.Time) (Plan, error) {
	if plan.Loop == nil {
		return Plan{}, fmt.Errorf("%w: plan has no loop", ErrNotFound)
	}
	if plan.Loop.Status == PlanLoopStatusStopped {
		return plan, nil
	}
	now = now.UTC()
	loop := *plan.Loop
	loop.Status = PlanLoopStatusStopped
	loop.StoppedBy = by
	loop.StoppedReason = strings.TrimSpace(reason)
	loop.Checkpoint = nil
	loop.UpdatedAt = now
	plan.Loop = &loop
	plan.UpdatedAt = now
	return plan, nil
}

// PlanLoopDecision 是一輪結束後要不要再開下一輪。
type PlanLoopDecision struct {
	Continue bool
	StopBy   PlanLoopStopReason
	Reason   string
}

// EvaluatePlanLoopAfterRound 在一輪結束後決定下一步。
//
// progressed 由呼叫端比對這一輪前後的計畫算出：只要有任何步驟的狀態或證據改變
// 就算推進。停止條件的優先順序刻意如此——記錄下來的原因要是最能解釋的那個，
// 而不是剛好先檢查到的那個。
func EvaluatePlanLoopAfterRound(plan Plan, progressed bool, now time.Time) (Plan, PlanLoopDecision) {
	if plan.Loop == nil {
		return plan, PlanLoopDecision{}
	}
	// Agent 或使用者已經中斷：那個原因比任何系統判定都更能解釋現況，不要覆蓋。
	if plan.Loop.Status != PlanLoopStatusRunning {
		return plan, PlanLoopDecision{}
	}
	now = now.UTC()
	loop := *plan.Loop
	if progressed {
		loop.IdleRounds = 0
	} else {
		loop.IdleRounds++
	}
	loop.UpdatedAt = now
	plan.Loop = &loop
	plan.UpdatedAt = now

	// 完成優先於一切：做完了就是做完了，不必再提上限或空轉。
	// 這裡看的是計畫狀態，不是 Agent 說了什麼——Agent 的最後一句話不是驗收標準。
	if PlanIsTerminal(plan) {
		stopped, _ := StopPlanLoop(plan, PlanLoopStopCompleted, "計畫的所有步驟都已完成或略過", now)
		return stopped, PlanLoopDecision{StopBy: PlanLoopStopCompleted, Reason: "計畫已完成"}
	}
	if loop.IdleRounds >= PlanLoopIdleLimit {
		reason := "連續 " + strconv.Itoa(loop.IdleRounds) + " 輪沒有任何步驟前進"
		stopped, _ := StopPlanLoop(plan, PlanLoopStopNoProgress, reason, now)
		return stopped, PlanLoopDecision{StopBy: PlanLoopStopNoProgress, Reason: reason}
	}
	if loop.Round >= loop.MaxRounds {
		reason := "已用完設定的 " + strconv.Itoa(loop.MaxRounds) + " 輪"
		stopped, _ := StopPlanLoop(plan, PlanLoopStopMaxRounds, reason, now)
		return stopped, PlanLoopDecision{StopBy: PlanLoopStopMaxRounds, Reason: reason}
	}
	return plan, PlanLoopDecision{Continue: true}
}

// PlanStepFingerprint 是用來比對「這一輪有沒有推進」的指紋。
//
// 只取狀態與證據：標題或描述被改寫不算推進，那正是走鐘的表現之一。
func PlanStepFingerprint(plan Plan) string {
	parts := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		parts = append(parts, step.ID+":"+string(step.Status)+":"+step.Evidence)
	}
	return strings.Join(parts, "\n")
}

// PlanRoundBrief 是送給 Agent 的那一輪輸入。
//
// **全部內容一律從儲存重新組裝**，不沿用模型上一輪的說法。模型的複述本身就是
// 走鐘的傳染途徑：第 2 輪照著第 1 輪的複述工作、第 3 輪照著第 2 輪的，
// 三輪之後就跟原始目標無關了。唯一的例外是檢查點的交接內容，
// 那是 Agent 明知會被中斷而刻意留下的，不是隨手複述。
func PlanRoundBrief(plan Plan) string {
	if plan.Loop == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("這是同一個任務的多輪執行，第 ")
	builder.WriteString(strconv.Itoa(plan.Loop.Round))
	builder.WriteString(" 輪／共 ")
	builder.WriteString(strconv.Itoa(plan.Loop.MaxRounds))
	builder.WriteString(" 輪。\n")
	// 輪次緊接著編號步驟清單，很容易被讀成「第 N 輪＝做第 N 步」。
	// 兩者無關：輪是「再推進一次」的機會，步驟是任務本身的結構。
	builder.WriteString("「輪」是再推進一次的機會，與步驟編號無關——")
	builder.WriteString("這一輪該做什麼由下面的步驟狀態決定，不是由輪次決定。\n\n")

	// 目標逐字給，不要摘要——這是不可變的錨。
	builder.WriteString("原始目標（不可變更）：\n")
	objective := strings.TrimSpace(plan.Objective)
	if objective == "" {
		objective = strings.TrimSpace(plan.Title)
	}
	builder.WriteString(objective)
	builder.WriteString("\n\n計畫目前的狀態：\n")

	remaining := 0
	for index, step := range plan.Steps {
		if step.Status != PlanStepStatusCompleted && step.Status != PlanStepStatusSkipped {
			remaining++
		}
		builder.WriteString(strconv.Itoa(index + 1))
		builder.WriteString(". [")
		builder.WriteString(string(step.Status))
		builder.WriteString("] ")
		builder.WriteString(step.Title)
		builder.WriteString("\n   驗證條件：")
		builder.WriteString(step.Verification)
		if evidence := strings.TrimSpace(step.Evidence); evidence != "" {
			builder.WriteString("\n   證據：")
			builder.WriteString(evidence)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n尚未結束的步驟：")
	builder.WriteString(strconv.Itoa(remaining))
	builder.WriteString(" 個。\n")

	if checkpoint := plan.Loop.Checkpoint; checkpoint != nil {
		// 續跑：明確標示上次中斷在哪裡。被中斷的步驟看起來只是「未完成」，
		// 但該採取的行動不一樣——要接續，不是重做。
		builder.WriteString("\n上一次在第 ")
		builder.WriteString(strconv.Itoa(checkpoint.Round))
		builder.WriteString(" 輪被中斷")
		if stepTitle := planStepTitle(plan, checkpoint.StepID); stepTitle != "" {
			builder.WriteString("，當時進行到「")
			builder.WriteString(stepTitle)
			builder.WriteString("」")
		}
		builder.WriteString("。你當時留下的交接：\n")
		builder.WriteString(checkpoint.Note)
		builder.WriteString("\n請從這裡接續，不要重新開始。\n")
	}

	builder.WriteString("\n完成的唯一方式是把每個步驟都做到 completed 並附上證據；")
	builder.WriteString("宣稱完成但沒有證據不算數。\n")
	builder.WriteString("這一輪若沒有任何步驟前進，會被記為空轉。\n")
	builder.WriteString("判斷再做下去沒有意義時，呼叫 plan_loop_interrupt 立刻中止，")
	builder.WriteString("並寫下做到哪裡，之後才能從那裡接續。")
	return builder.String()
}

func planStepTitle(plan Plan, stepID string) string {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return ""
	}
	for _, step := range plan.Steps {
		if step.ID == stepID {
			return step.Title
		}
	}
	return ""
}
