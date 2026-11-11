package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/internal/valueutil"
	"AgenticService/src/memory"
	"AgenticService/src/ports"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DefaultMaxTurns = 96
	// DefaultMaxAutonomousToolTurns 為 0 時不另設固定工具回合上限；長任務仍受
	// Run budget、取消機制與重複副作用防護約束。
	DefaultMaxAutonomousToolTurns = 0
	maxToolProtocolRepairAttempts = 2
	// maxEmptyAnswerRetries 是「只輸出思考、沒有回答」時的重試次數。
	// 每次重試都要重跑一次完整 prefill，本機模型代價很高，因此只給一次：
	// 明確要求之後還是給不出答案，再試也不會變。
	maxEmptyAnswerRetries = 1
	// retrievalQueryMessages／retrievalQueryRunes 限制檢索查詢的長度：整段對話
	// 丟進去只會讓共通詞淹掉這一輪真正的重點。
	retrievalQueryMessages = 3
	retrievalQueryRunes    = 400
	// retrievalQueryScanMessages 是往回掃描的訊息則數上限。
	//
	// 一輪工具密集的工作可能連續產生數十則助理與工具訊息，200 則足以涵蓋
	// 十幾輪對話；再往回的使用者訊息對「這次要查什麼」也已經沒有參考價值。
	retrievalQueryScanMessages = 200
)

type EventSink func(domain.EngineEvent) error

type BeforeToolHook func(context.Context, domain.Session, domain.ToolCall) error
type AfterToolHook func(context.Context, domain.Session, domain.ToolCall, domain.ToolExecution) domain.ToolExecution

type Runner struct {
	Model    ports.Model
	Tools    ports.ToolRuntime
	Sessions ports.SessionRepository
	Plans    ports.PlanRepository
	// ToolCallMode 決定 Harness 如何取得工具決策。instruction 會要求模型輸出
	// 嚴格 JSON 指令；native 則使用 OpenAI-compatible tool_calls 欄位。
	ToolCallMode ToolCallMode
	Context      *ContextManager
	Memory       *memory.Manager
	Logger       *slog.Logger
	Budget       domain.RunBudget
	// MaxAutonomousToolTurns 限制連續由模型自行擴張的工具回合。0 代表不另設
	// 固定上限；正數到達後會停用工具並要求模型以既有觀察產生最終答案。
	MaxAutonomousToolTurns int
	// MaxCompletionChecks 是模型宣稱完成、但仍有未解決工具失敗時的追問次數上限。
	// 0 代表停用追問，回到「模型說完成就是完成」。
	MaxCompletionChecks int
	Approvals           ports.ApprovalCoordinator
	// ToolRetrievalDisabled 關閉 MCP 工具目錄的檢索過濾，改成整份目錄進入每一次
	// 請求。刻意用反向命名：零值就是啟用，任何忘了設定這個欄位的 Runner 都不會
	// 悄悄退回會把提示撐爆的行為。
	ToolRetrievalDisabled bool
	SystemPrompt          string
	BeforeTool            BeforeToolHook
	AfterTool             AfterToolHook
	budgetMu              sync.RWMutex
}

// SetBudget 更新後續 Run 使用的限制；已開始的 Run 保留啟動時快照，避免執行中途
// 改變上限造成不一致，也避免管理介面更新與背景工作形成資料競爭。
func (r *Runner) SetBudget(budget domain.RunBudget) {
	if r == nil {
		return
	}
	r.budgetMu.Lock()
	r.Budget = budget
	r.budgetMu.Unlock()
}

// SetToolCallMode 讓管理介面切換工具呼叫協定；已開始的 Run 維持啟動時的模式。
func (r *Runner) SetToolCallMode(mode ToolCallMode) {
	if r == nil {
		return
	}
	r.budgetMu.Lock()
	r.ToolCallMode = mode
	r.budgetMu.Unlock()
}

// SetToolRetrieval 讓管理介面切換 MCP 工具檢索；已開始的 Run 維持啟動時的設定。
func (r *Runner) SetToolRetrieval(enabled bool) {
	if r == nil {
		return
	}
	r.budgetMu.Lock()
	r.ToolRetrievalDisabled = !enabled
	r.budgetMu.Unlock()
}

func (r *Runner) toolRetrievalSnapshot() bool {
	if r == nil {
		return true
	}
	r.budgetMu.RLock()
	defer r.budgetMu.RUnlock()
	return !r.ToolRetrievalDisabled
}

func (r *Runner) toolCallModeSnapshot() ToolCallMode {
	r.budgetMu.RLock()
	defer r.budgetMu.RUnlock()
	return r.ToolCallMode
}

func (r *Runner) budgetSnapshot() domain.RunBudget {
	r.budgetMu.RLock()
	defer r.budgetMu.RUnlock()
	return r.Budget
}

type Input struct {
	RunID        string
	Session      domain.Session
	UserInput    string
	ProviderID   string
	Model        string
	ThinkingMode string
	Metadata     map[string]any
	// Inputs 是執行中追加的使用者訊息；nil 表示此 Run 不支援插入。
	Inputs         *domain.RunInputQueue
	ModelSelection *domain.RunModelSelection
}

type turnLifecycleKey struct{}

// 只由 Run 所在 goroutine 更新；讓所有早退路徑共用同一個回合收尾保險。
type turnLifecycle struct {
	id        string
	number    int
	startedAt time.Time
	response  domain.ModelResponse
	toolCount int
	finished  bool
}

func (r *Runner) Run(ctx context.Context, input Input, emit EventSink) (output domain.RunResult, runErr error) {
	if r == nil || r.Model == nil || r.Tools == nil || r.Sessions == nil {
		return domain.RunResult{}, fmt.Errorf("%w: harness dependencies are incomplete", domain.ErrInvalidInput)
	}
	if strings.TrimSpace(input.Session.ID) == "" || strings.TrimSpace(input.UserInput) == "" {
		return domain.RunResult{}, fmt.Errorf("%w: session and user input are required", domain.ErrInvalidInput)
	}
	if r.Plans != nil {
		if _, err := r.Plans.Reconcile(ctx, input.Session.ID, input.Session.LockPlans); err != nil {
			return domain.RunResult{}, err
		}
	}
	operationID := strings.TrimSpace(input.RunID)
	if operationID == "" {
		operationID = domain.NewID("operation")
	}
	operationStartedAt := time.Now().UTC()
	budget := newRunBudgetTracker(r.budgetSnapshot(), operationStartedAt)
	runContext, cancelRunBudget := budget.context(ctx)
	defer cancelRunBudget()
	ctx = context.WithValue(runContext, runBudgetContextKey{}, budget)
	turnState := &turnLifecycle{}
	ctx = context.WithValue(ctx, turnLifecycleKey{}, turnState)
	providerID := valueutil.FirstNonEmpty(input.ProviderID, input.Session.ProviderID)
	modelID := valueutil.FirstNonEmpty(input.Model, input.Session.Model)
	effectiveSession := input.Session
	effectiveSession.ProviderID = providerID
	effectiveSession.Model = modelID
	logger := logging.Or(r.Logger).With(
		"operation_id", operationID,
		"run_id", strings.TrimSpace(input.RunID),
		"session_id", input.Session.ID,
	)
	if budgetTokens, declared := r.Context.BudgetDiagnostics(effectiveSession); !declared && budgetTokens > 0 {
		// 沒有這行，現場只會看到「很慢」，看不到「Harness 以為還有 26 萬 token 可用」。
		logger.Warn("provider did not declare a context window; compaction uses the configured fallback",
			"provider_id", providerID,
			"model", modelID,
			"fallback_budget_tokens", budgetTokens,
		)
	}
	logger.Info("harness run started",
		"provider_id", providerID,
		"model", modelID,
		"max_turns", budget.budget.MaxTurns,
		"max_wall_clock_ms", budget.budget.MaxWallClock.Milliseconds(),
		"max_tokens", budget.budget.MaxTokens,
		"max_tool_calls", budget.budget.MaxToolCalls,
	)
	if _, err := appendRecord(ctx, r.Sessions, input.Session.ID, domain.SessionEntryOperationStarted, map[string]any{
		"operation_id": operationID,
		"run_id":       strings.TrimSpace(input.RunID),
		"status":       domain.OperationStatusRunning,
		"provider_id":  providerID,
		"model":        modelID,
	}); err != nil {
		return domain.RunResult{}, err
	}
	finalMessageID := ""
	var undispatched []domain.ToolCall
	pendingTurnID := ""
	defer func() {
		if recovered := recover(); recovered != nil {
			// 仍交由 Application 的既有 panic 邊界處理，但 Harness 自己的稽核
			// 與 agent.end 不可先宣告成功，也不把 panic 的資料內容寫入記錄。
			runErr = errors.New("Harness 執行發生內部異常")
			defer func() { panic(recovered) }()
		}
		runErr = r.finishOperation(ctx, operationFinish{
			input:          input,
			operationID:    operationID,
			providerID:     providerID,
			modelID:        modelID,
			startedAt:      operationStartedAt,
			budget:         budget,
			turn:           turnState,
			logger:         logger,
			finalMessageID: finalMessageID,
			pendingTurnID:  pendingTurnID,
			undispatched:   undispatched,
		}, emit, &output, runErr)
	}()

	if err := emitEvent(emit, "agent.start", map[string]any{"lane": "main", "operation_id": operationID}); err != nil {
		return domain.RunResult{}, err
	}
	userMessage, err := r.recordUserMessage(ctx, input, emit)
	if err != nil {
		return domain.RunResult{}, err
	}

	definitions, err := r.Tools.Definitions(ctx, input.Session)
	if err != nil {
		return domain.RunResult{}, err
	}
	// 檢索是工具目錄的第一層過濾：只有與這次需求相關的工具進入模型目錄，
	// 其餘工具仍可經 find_tools 取回並直接呼叫。
	retrievalQuery := r.retrievalQuery(ctx, input)
	retrievalEnabled := r.toolRetrievalSnapshot()
	retriever := newToolRetriever(definitions, retrievalQuery, retrievalEnabled)
	if retriever.enabled() {
		logger.Debug("mcp tool retrieval active",
			"catalog", len(retriever.known),
			"selected", retriever.revealedCount(),
		)
		if err := emitEvent(emit, "tools.retrieved", map[string]any{
			"catalog":  len(retriever.known),
			"selected": retriever.revealedCount(),
		}); err != nil {
			return domain.RunResult{}, err
		}
	}
	parallelTools := parallelizableToolNames(definitions, r.Approvals)
	approvalState := newRunApprovalState(input.Session.PermanentToolApproval)
	loopGuard := newToolLoopGuard(definitions)
	presentedContracts := map[string]string{}
	if err := loopGuard.restoreUncertain(ctx, r.Sessions, input.Session.ID); err != nil {
		return domain.RunResult{}, fmt.Errorf("恢復工具派送狀態失敗: %w", err)
	}
	toolCallMode := effectiveToolCallMode(r.Model, providerID, NormalizeToolCallMode(string(r.toolCallModeSnapshot())))
	// 每個 Run 都從系統 Shell 階段開始。只有 shell_exec 的實際執行結果為失敗，
	// 才在下一輪公開檔案、搜尋、比較、SSH 等內建工具。
	builtinFallbackEnabled := !definitionNamed(definitions, systemShellToolName)
	// 回憶空間與工具檢索共用查詢字串：跟進提問常常只剩代名詞（「那再改一下」），
	// 單看這一句什麼都檢索不到。
	memoryQuery := input.UserInput
	if r.Memory.SpaceEnabled() {
		memoryQuery = retrievalQuery
	}
	recalled, err := r.recallMemory(ctx, input.Session, memoryQuery, operationID)
	if err != nil {
		return domain.RunResult{}, err
	}
	if len(recalled.Memories) > 0 {
		logger.Debug("long-term memory recalled", "count", len(recalled.Memories), "truncated", recalled.Truncated)
		if err := emitEvent(emit, "memory.recalled", map[string]any{
			"count":      len(recalled.Memories),
			"memory_ids": memoryIDs(recalled.Memories),
			"truncated":  recalled.Truncated,
		}); err != nil {
			return domain.RunResult{}, err
		}
	}
	var lastAssistant domain.Message
	completion := newCompletionTracker(definitions)
	// toolResultsObserved 代表已進入 pi-style loop 的收斂階段：工具觀察仍保留
	// 在內部 transcript，但下一個沒有工具呼叫的 assistant 訊息必須是對使用者
	// 可理解的最終回答，不能直接傾倒工具輸出或 Harness 協定。
	toolResultsObserved := false
	toolTurns := 0
	maxAutonomousToolTurns := r.MaxAutonomousToolTurns
	// completionDirective 只作用於下一輪的 system prompt，不寫進 transcript 的訊息串，
	// 避免把 Harness 的內部追問偽裝成使用者發言。
	completionDirective := ""
	// failureRecall 承載「同一個工具連續失敗兩次」時補檢索到的記憶，
	// 與 completionDirective 一樣只作用於下一輪。
	failureRecall := ""
	failureRecallTracker := &memory.FailureRecallTracker{}
	planCompletionChecks := 0
	lastPlanCompletionKey := ""
	toolProtocolRepairAttempts := 0
	emptyAnswerRetries := 0
	truncationRetries := 0
	// finishBudget 是所有 budget 退出點唯一的收尾路徑。集中在一處，是為了讓
	// 「補齊未執行的 tool call」這個 transcript 不變式不可能被某一條路徑漏掉——
	// 這裡有近十個退出點，逐點重複收尾邏輯遲早會有一條寫錯。
	finishBudget := func(exceeded *domain.RunBudgetExceeded, assistant domain.Message, assistantPersisted bool, pending []domain.ToolCall) (domain.RunResult, error) {
		// completeBudget 接手整批未派送結果，避免離開時重複補寫。
		undispatched = nil
		result, err := r.completeBudget(ctx, input.Session.ID, operationID, assistant, assistantPersisted, pending, exceeded, emit)
		result.Completion = completion.completion()
		finalMessageID = result.Message.ID
		return result, err
	}
	for turn := 1; turn <= budget.budget.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
				return finishBudget(exceeded, lastAssistant, true, nil)
			}
			return domain.RunResult{}, fmt.Errorf("%w: %v", domain.ErrCanceled, err)
		}
		budget.startTurn(turn)
		selected, thinking := input.ModelSelection.TakeSettings()
		changed := false
		if selected != "" && selected != modelID {
			modelID = selected
			effectiveSession.Model = selected
			input.Model = selected
			input.Session.Model = selected
			changed = true
		}
		if thinking != nil && *thinking != input.ThinkingMode {
			input.ThinkingMode = *thinking
			input.Session.ThinkingMode = *thinking
			effectiveSession.ThinkingMode = *thinking
			changed = true
		}
		if changed {
			if err := emitEvent(emit, "run.model_changed", map[string]any{"provider_id": providerID, "model": modelID, "thinking_mode": input.ThinkingMode, "turn": turn}); err != nil {
				return domain.RunResult{}, err
			}
		}
		// 執行中追加的使用者訊息在回合邊界寫入：上一輪的工具結果都已落盤，
		// 訊息排在它們之後，下一次重讀歷史時自然帶給模型。
		if _, err := r.injectRunInputs(ctx, input, emit, turn); err != nil {
			return domain.RunResult{}, err
		}
		if turn > 1 {
			latestDefinitions, err := r.Tools.Definitions(ctx, input.Session)
			if err != nil {
				return domain.RunResult{}, err
			}
			if !sameToolCatalog(definitions, latestDefinitions) {
				definitions = latestDefinitions
				retriever = refreshedRetriever(retriever, definitions, retrievalQuery, retrievalEnabled)
				parallelTools = parallelizableToolNames(definitions, r.Approvals)
				loopGuard.definitions = map[string]domain.ToolDefinition{}
				completion.definitions = map[string]domain.ToolDefinition{}
				for _, definition := range definitions {
					loopGuard.definitions[definition.Name] = definition
					completion.definitions[definition.Name] = definition
				}
				if err := emitEvent(emit, "tools.catalog_changed", map[string]any{"count": len(definitions)}); err != nil {
					return domain.RunResult{}, err
				}
			}
		}
		loopGuardReason := loopGuard.reason()
		successfulMutationSummary := loopGuard.successfulMutationSummary()
		forceFinalization := maxAutonomousToolTurns > 0 && toolTurns >= maxAutonomousToolTurns
		activeDefinitions := retriever.stage(stagedToolDefinitions(definitions, builtinFallbackEnabled))
		if forceFinalization {
			activeDefinitions = nil
		}
		// callableDefinitions 與 activeDefinitions 刻意不同：目錄決定模型看得到什麼，
		// 這份集合決定執行端接不接受。檢索沒命中的 MCP 工具照樣可以呼叫，
		// 免得模型多花一輪重新找一個它已經知道名字的工具。
		callableDefinitions := activeDefinitions
		if !forceFinalization {
			callableDefinitions = retriever.recognizable(activeDefinitions)
		}
		if loopGuardReason != "" && !forceFinalization {
			// 防重送只停止副作用，仍須讓模型讀取與驗證已存在的產物。
			callableDefinitions = readOnlyToolDefinitions(callableDefinitions)
			activeDefinitions = readOnlyToolDefinitions(activeDefinitions)
		}
		activeTools := availableToolNames(callableDefinitions)
		// 唯讀工具不需要逐次人工核准；MCP 的唯讀屬性仍以該 Server 的
		// trust_annotations 設定為準。
		approvalExemptTools := approvalExemptToolNames(callableDefinitions, input.Session)
		toolStage := toolStageSystemShell
		if builtinFallbackEnabled {
			toolStage = toolStageBuiltinFallback
		}
		turnID := domain.NewID("turn")
		turnStartedAt := time.Now().UTC()
		ref := turnReference{sessionID: input.Session.ID, operationID: operationID, id: turnID, number: turn, startedAt: turnStartedAt}
		if _, err := appendRecord(ctx, r.Sessions, input.Session.ID, domain.SessionEntryTurnStarted, map[string]any{
			"operation_id": operationID,
			"turn_id":      turnID,
			"turn":         turn,
		}); err != nil {
			return domain.RunResult{}, err
		}
		*turnState = turnLifecycle{id: turnID, number: turn, startedAt: turnStartedAt}
		if err := emitEvent(emit, "turn.start", map[string]any{
			"provider_id":     providerID,
			"model":           modelID,
			"thinking_mode":   input.ThinkingMode,
			"turn":            turn,
			"turn_id":         turnID,
			"available_tools": availableToolNamesSorted(activeDefinitions),
			"tool_turns":      toolTurns,
			"tool_stage":      toolStage,
			"phase":           map[bool]string{true: "finalization", false: "tool_loop"}[forceFinalization],
		}); err != nil {
			return domain.RunResult{}, err
		}
		finishBudgetInTurn := func(exceeded *domain.RunBudgetExceeded, assistant domain.Message, assistantPersisted bool, pending []domain.ToolCall, toolCount int, response domain.ModelResponse) (domain.RunResult, error) {
			_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "budget_exceeded", toolCount, response, nil)
			return finishBudget(exceeded, assistant, assistantPersisted, pending)
		}
		messages := []domain.Message{}
		systemPrompt := strings.TrimSpace(r.SystemPrompt)
		hostPrompt := joinPromptSections(hostEnvironmentPrompt(definitions, activeDefinitions), instructionsPrompt(input.Metadata))
		toolPrompt := nativeToolPrompt(activeDefinitions)
		if toolCallMode == ToolCallModeInstruction {
			toolPrompt = toolInstructionPrompt(activeDefinitions)
		}
		if forceFinalization {
			// 收斂輪只需要「這個 Run 實際動用得到哪些能力」作為證據邊界。檢索模式下
			// 把完整目錄倒進來，等於把前面省下來的 token 在最後一輪全部還回去。
			toolPrompt = finalizationToolCatalogPrompt(retriever.stage(definitions))
		}
		planPrompt, planCount, planErr := r.planContextPrompt(ctx, input.Session.ID, input.Session.LockPlans)
		if planErr != nil {
			_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, domain.ModelResponse{}, planErr)
			return domain.RunResult{}, planErr
		}
		phasePrompt := joinPromptSections(toolSelectionPhasePrompt(builtinFallbackEnabled, activeDefinitions), retrievalPhasePrompt(activeDefinitions), planningPhasePrompt(input.Session.LockPlans, planCount > 0), explorationPhasePrompt(builtinFallbackEnabled, activeDefinitions), answerAudiencePrompt(), progressPresentationPrompt(input.ThinkingMode))
		if toolResultsObserved {
			phasePrompt = joinPromptSections(phasePrompt, finalizationPhasePrompt(toolTurns, maxAutonomousToolTurns, forceFinalization, loopGuardReason, successfulMutationSummary))
		}
		contextPrompt := joinPromptSections(
			sandboxScopePrompt(input.Session),
			attachmentContextPrompt(input.Metadata),
			planPrompt,
			recalled.SystemPrompt,
			failureRecall,
			completionDirective,
		)
		completionDirective = ""
		failureRecall = ""
		contextBudgetPrompt := joinPromptSections(systemPrompt, hostPrompt, toolPrompt, phasePrompt, contextPrompt)
		modelSystemPrompt := systemPrompt
		modelHostPrompt := hostPrompt
		modelToolPrompt := toolPrompt
		modelPhasePrompt := phasePrompt
		modelContextPrompt := contextPrompt
		if r.Context != nil {
			compactionStarted := false
			window, contextErr := r.Context.BuildObserved(ctx, effectiveSession, contextBudgetPrompt, activeDefinitions, func(status ContextCompactionStatus) error {
				compactionStarted = true
				return reportCompactionStarted(logger, emit, turn, status)
			})
			if contextErr != nil {
				if compactionStarted {
					_ = emitEvent(emit, "context.compaction.failed", map[string]any{"message": contextErr.Error()})
				}
				if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
					return finishBudgetInTurn(exceeded, lastAssistant, true, nil, 0, domain.ModelResponse{})
				}
				if exceeded := budget.tokensExceeded(); exceeded != nil {
					return finishBudgetInTurn(exceeded, lastAssistant, true, nil, 0, domain.ModelResponse{})
				}
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, domain.ModelResponse{}, contextErr)
				return domain.RunResult{}, contextErr
			}
			messages = window.Messages
			modelContextPrompt = joinPromptSections(contextPrompt, sessionSummaryPrompt(window.Summary))
			if window.Compacted {
				logger.Info("session context compacted",
					"turn", turn,
					"estimated_tokens", window.EstimatedTokens,
					"budget_tokens", window.Budget,
					"message_count", len(window.Messages),
				)
				if err := emitEvent(emit, "context.compacted", map[string]any{
					"estimated_tokens": window.EstimatedTokens,
					"budget_tokens":    window.Budget,
					"message_count":    len(window.Messages),
					"trigger_ratio":    softCompactionRatio,
				}); err != nil {
					return domain.RunResult{}, err
				}
			}
		} else {
			var listErr error
			messages, listErr = r.Sessions.ListMessages(ctx, input.Session.ID)
			if listErr != nil {
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, domain.ModelResponse{}, listErr)
				return domain.RunResult{}, listErr
			}
			messages = repairMessages(messages)
		}
		history, userPrompt := splitCurrentUserMessage(messages, userMessage)
		// 組合 prompt 之前的最後一道檢查：以實際字數再驗一次。
		//
		// 前面的壓縮是依 token 估算決定的，而估算會失準——工具結果多半是 JSON 與
		// 識別碼，ASCII 權重每 4 字元 1 token 對這種內容低估一半以上。實測有一次
		// 請求帶著 131,861 字歷史送出，估算卻只有約 3.5 萬 token、看起來還很空。
		// 這道檢查不看估算，只看真的要送出去的字數。正常情況下它不會做任何事。
		history = r.enforceHistoryCharacterLimit(history, logger, turn, effectiveSession)
		modelHistory := history
		modelTools := activeDefinitions
		if toolCallMode == ToolCallModeInstruction {
			modelHistory = instructionMessages(history)
			modelTools = nil
		}
		for _, definition := range activeDefinitions {
			presentedContracts[definition.Name] = domain.ToolContractID(definition)
		}
		assistantID := domain.NewID("msg")
		if err := emitEvent(emit, "message.start", map[string]any{
			"message": map[string]any{"id": assistantID, "session_id": input.Session.ID, "role": "assistant"},
		}); err != nil {
			return domain.RunResult{}, err
		}
		var instructionStream *instructionTextStream
		if toolCallMode == ToolCallModeInstruction {
			instructionStream = &instructionTextStream{}
		}
		emitAssistantDelta := func(delta string) error {
			return emitEvent(emit, "message.delta", map[string]any{"message_id": assistantID, "delta": delta})
		}
		// 每一輪都記下請求的組成。使用者回報「卡住」時，第一個要回答的問題是
		// 「這次到底送了多少東西出去」——沒有這行就只能靠猜，而猜錯一次就是
		// 使用者再等二十分鐘。
		logger.Info("model request",
			"turn", turn,
			"turn_id", turnID,
			"tools", len(modelTools),
			"tool_chars", utf8.RuneCountInString(modelToolPrompt),
			"steering_chars", utf8.RuneCountInString(modelSystemPrompt)+utf8.RuneCountInString(modelHostPrompt)+utf8.RuneCountInString(modelPhasePrompt)+utf8.RuneCountInString(modelContextPrompt),
			"history_messages", len(modelHistory),
			"history_chars", historyCharacters(modelHistory),
			"user_chars", utf8.RuneCountInString(userPrompt),
			"tool_names", availableToolNamesSorted(modelTools),
		)
		response, err := streamWithBudget(ctx, r.Model, domain.ModelRequest{
			SessionID:     input.Session.ID,
			ProviderID:    providerID,
			Model:         modelID,
			ThinkingMode:  input.ThinkingMode,
			SystemPrompt:  modelSystemPrompt,
			HostPrompt:    modelHostPrompt,
			ToolPrompt:    modelToolPrompt,
			PhasePrompt:   modelPhasePrompt,
			ContextPrompt: modelContextPrompt,
			History:       modelHistory,
			UserPrompt:    userPrompt,
			Tools:         modelTools,
			Metadata:      valueutil.CloneMap(input.Metadata),
		}, modelEventForwarder(emit, assistantID, turnID, instructionStream, emitAssistantDelta), r.Context.counter())
		// 收尾只需要用量與協定狀態，不保留整份回覆或逐輪堆疊 context。
		turnState.response = domain.ModelResponse{Usage: response.Usage, StopReason: response.StopReason, ProviderRequestID: response.ProviderRequestID}
		if err != nil {
			if exceeded := budget.tokensExceeded(); exceeded != nil {
				assistant := domain.Message{ID: assistantID, SessionID: input.Session.ID, Role: "assistant"}
				return finishBudgetInTurn(exceeded, assistant, false, nil, 0, response)
			}
			if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
				assistant := domain.Message{ID: assistantID, SessionID: input.Session.ID, Role: "assistant"}
				return finishBudgetInTurn(exceeded, assistant, false, nil, 0, domain.ModelResponse{})
			}
			logger.Error("model turn failed", "turn", turn, "turn_id", turnID, "error", err)
			_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, domain.ModelResponse{}, err)
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return domain.RunResult{}, fmt.Errorf("%w: %v", domain.ErrCanceled, valueutil.FirstError(err, ctx.Err()))
			}
			return domain.RunResult{}, err
		}
		if response.StopReason == "length" {
			// 截斷不是空回答，也不是可執行的完整決策；必須先於兩種工具協定的解析。
			// 同一 Run 最多補救一次，不重送先前已執行的工具，也不提高使用者的輸出上限。
			response.ToolCalls = nil
			response.Continuation = nil
			assistant := internalPhaseAssistant(assistantID, input.Session.ID, &response, "output_truncated")
			if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
				return finishBudgetInTurn(exceeded, assistant, false, nil, 0, response)
			}
			if _, err := appendMessage(ctx, r.Sessions, input.Session.ID, assistant); err != nil {
				return domain.RunResult{}, err
			}
			if err := emitMessageLifecycle(emit, "message.end", assistant); err != nil {
				return domain.RunResult{}, err
			}
			lastAssistant = assistant
			if exceeded := budget.tokensExceeded(); exceeded != nil {
				return finishBudgetInTurn(exceeded, assistant, true, nil, 0, response)
			}
			if truncationRetries >= 1 || forceFinalization {
				err := fmt.Errorf("模型回覆因輸出或上下文上限而截斷，本輪工具未執行（本輪回報輸出 %d tokens）。請提高 Provider 的最大輸出 Tokens，或縮小單次工作內容；上下文使用率不代表輸出額度", response.Usage.OutputTokens)
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, response, err)
				return domain.RunResult{}, err
			}
			truncationRetries++
			if err := r.closeToollessTurn(ctx, emit, ref, "output_truncated", response); err != nil {
				return domain.RunResult{}, err
			}
			if err := emitEvent(emit, "agent.progress", map[string]any{"message": "模型輸出被截斷，正嘗試縮小單次工作（最多一次）；本輪工具尚未執行。"}); err != nil {
				return domain.RunResult{}, err
			}
			completionDirective = "上一輪因輸出或上下文上限截斷，該輪所有工具均未執行。請減少說明，重新產生較短且完整的回覆；需要工具時，每次只提交一個小型、參數完整的操作，將大型檔案工作拆成數輪。不可續接半截 JSON，不可聲稱截斷的工具已成功，不可重做更早回合已成功的操作。仍須遵守原始任務與工具限制。"
			continue
		}
		if response.StopReason == "refusal" {
			// 拒答是終態；即使回覆含工具範例，也不可解析成可執行指令。
			response.ToolCalls = nil
			if instructionStream != nil {
				if err := instructionStream.Finish(response.Content, emitAssistantDelta); err != nil {
					return domain.RunResult{}, err
				}
			}
		}
		response.ToolCalls, err = normalizeToolCalls(response.ToolCalls)
		if err != nil {
			_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, response, err)
			return domain.RunResult{}, err
		}
		// 原生工具呼叫模式下，模型有時會把工具參數寫進 content 而不是 tool_calls。
		// 那一輪會被當成最終回答，使用者看到的是一整片 JSON——實測就是這樣收到
		// 幾百個 {"cell":...,"sheet":...,"value":...} 而不是一份 Excel。
		// 這種輸出是協定錯誤，不是答案，要走與指令模式相同的修復流程。
		nativeArgumentsTool := ""
		if response.StopReason != "refusal" && toolCallMode == ToolCallModeNative && len(response.ToolCalls) == 0 && !forceFinalization {
			nativeArgumentsTool = toolArgumentsLeak(response.Content, callableDefinitions)
		}
		protocolRepairExhausted := false
		if response.StopReason != "refusal" && len(response.ToolCalls) == 0 && (toolCallMode == ToolCallModeInstruction || nativeArgumentsTool != "") {
			instructionDefinitions := callableDefinitions
			if forceFinalization {
				// 收斂輪仍辨識完整目錄中的工具指令，才能將違反收斂要求的輸出
				// 轉成最終回答；一般工具輪則只允許目前階段實際公開的工具。
				instructionDefinitions = definitions
			}
			calls, matched, parseErr := parseInstructionToolCalls(response.Content, instructionDefinitions)
			if parseErr == nil && !matched && nativeArgumentsTool != "" {
				parseErr = fmt.Errorf("%w: 模型把 %s 的參數當成回答輸出，沒有實際呼叫工具", domain.ErrProviderProtocol, nativeArgumentsTool)
			}
			if parseErr != nil {
				toolProtocolRepairAttempts++
				if toolProtocolRepairAttempts <= maxToolProtocolRepairAttempts {
					invalidAssistant := internalPhaseAssistant(assistantID, input.Session.ID, &response, "tool_protocol_repair")
					if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
						return finishBudgetInTurn(exceeded, invalidAssistant, false, nil, 0, response)
					}
					if _, err := appendMessage(ctx, r.Sessions, input.Session.ID, invalidAssistant); err != nil {
						return domain.RunResult{}, err
					}
					if err := emitMessageLifecycle(emit, "message.end", invalidAssistant); err != nil {
						return domain.RunResult{}, err
					}
					lastAssistant = invalidAssistant
					if exceeded := budget.tokensExceeded(); exceeded != nil {
						return finishBudgetInTurn(exceeded, invalidAssistant, true, nil, 0, response)
					}
					if err := emitEvent(emit, "run.tool_protocol_repair", map[string]any{
						"turn": turn, "attempt": toolProtocolRepairAttempts, "max_attempts": maxToolProtocolRepairAttempts,
					}); err != nil {
						return domain.RunResult{}, err
					}
					if err := r.closeToollessTurn(ctx, emit, ref, "tool_protocol_repair", response); err != nil {
						return domain.RunResult{}, err
					}
					completionDirective = toolProtocolRepairDirective(parseErr, toolProtocolRepairAttempts, nativeArgumentsTool)
					continue
				}
				// 連續修正仍失敗時，以可見的部分完成回覆正常收尾。計畫狀態與
				// transcript 都保留，使用者可再次執行或切換模型，不暴露底層錯誤頁。
				protocolRepairExhausted = true
				response.Content = "目前無法繼續執行：模型連續回傳不完整的工具指令。計畫進度已保留，請稍後重試或更換模型。"
				response.ToolCalls = nil
				response.StopReason = "stop"
			}
			if matched {
				// 成功取得完整工具指令後，下一個工具階段重新計算修復次數，
				// 避免前一個階段的格式錯誤耗盡後續階段的復原額度。
				toolProtocolRepairAttempts = 0
			}
			if matched && forceFinalization {
				response.Content = forcedFinalizationFallback(toolTurns, loopGuardReason)
				response.StopReason = "stop"
				matched = false
			}
			if matched {
				response.ToolCalls = calls
				response.Content = ""
				response.StopReason = "tool_calls"
				for index, call := range calls {
					arguments, _ := json.Marshal(call.Arguments)
					if err := emitEvent(emit, "tool_call.delta", map[string]any{
						"message_id":   assistantID,
						"index":        index,
						"tool_call_id": call.ID,
						"tool_name":    call.Name,
						"delta":        string(arguments),
					}); err != nil {
						return domain.RunResult{}, err
					}
				}
			} else if strings.TrimSpace(response.Content) != "" {
				if err := instructionStream.Finish(response.Content, emitAssistantDelta); err != nil {
					return domain.RunResult{}, err
				}
			}
		}
		if forceFinalization && len(response.ToolCalls) > 0 {
			response.ToolCalls = nil
			response.Content = forcedFinalizationFallback(toolTurns, loopGuardReason)
			response.StopReason = "stop"
		}
		if response.StopReason != "refusal" && toolCallMode == ToolCallModeNative && len(response.ToolCalls) == 0 {
			if toolName := imitatedToolCallName(response.Content, definitions); toolName != "" {
				err := fmt.Errorf("%w: Provider 將原生工具呼叫 %q 輸出成普通文字，而不是 OpenAI-compatible 的 tool_calls 欄位；請在 Provider 設定中測試目前的 Endpoint、模型與代理轉換器", domain.ErrProviderProtocol, toolName)
				_ = emitEvent(emit, "message.end", map[string]any{
					"message": map[string]any{"id": assistantID, "session_id": input.Session.ID, "role": "assistant"},
				})
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, response, err)
				return domain.RunResult{}, err
			}
		}
		logger.Debug("model turn completed",
			"turn", turn,
			"turn_id", turnID,
			"stop_reason", response.StopReason,
			"tool_calls", len(response.ToolCalls),
			"input_tokens", response.Usage.InputTokens,
			"output_tokens", response.Usage.OutputTokens,
			"provider_request_id", response.ProviderRequestID,
		)
		pendingPlanDirective := ""
		pendingPlan := domain.Plan{}
		if response.StopReason != "refusal" && !protocolRepairExhausted && len(response.ToolCalls) == 0 && strings.TrimSpace(response.Content) != "" {
			pendingPlanDirective, pendingPlan, err = r.planCompletionDirective(ctx, input.Session.ID, input.Session.LockPlans)
			if err != nil {
				_ = emitEvent(emit, "message.end", map[string]any{
					"message": map[string]any{"id": assistantID, "session_id": input.Session.ID, "role": "assistant"},
				})
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, response, err)
				return domain.RunResult{}, err
			}
		}
		planCompletionKey := pendingPlanStateKey(pendingPlan)
		continuePlanCompletion := pendingPlanDirective != "" && !forceFinalization && planCompletionKey != lastPlanCompletionKey
		// 完成度追問必須在寫入 assistant 訊息前決定：被追問的那一段只是中間產物，
		// 如果照一般回答寫進 transcript，使用者就會在對話裡看到兩份幾乎一樣的答案。
		completionCheckDirective := ""
		completionCheckReason := ""
		if response.StopReason != "refusal" && len(response.ToolCalls) == 0 && strings.TrimSpace(response.Content) != "" && !continuePlanCompletion && !protocolRepairExhausted {
			if directive := completion.challenge(r.MaxCompletionChecks); directive != "" {
				completionCheckDirective, completionCheckReason = directive, "unresolved_tool_failure"
			} else if directive := completion.challengeToolless(r.MaxCompletionChecks, len(activeDefinitions) > 0); directive != "" {
				completionCheckDirective, completionCheckReason = directive, "no_tool_executed"
			}
		}
		assistant := domain.Message{
			Continuation:      response.Continuation.Clone(),
			ID:                assistantID,
			SessionID:         input.Session.ID,
			Role:              "assistant",
			Content:           strings.TrimSpace(response.Content),
			Reasoning:         strings.TrimSpace(response.Reasoning),
			ToolCalls:         cloneToolCalls(response.ToolCalls),
			ProviderID:        response.ProviderID,
			ProviderRequestID: response.ProviderRequestID,
			Model:             response.Model,
			StopReason:        response.StopReason,
			Usage:             &response.Usage,
			CreatedAt:         time.Now().UTC(),
		}
		if len(assistant.ToolCalls) > 0 {
			assistant.Metadata = map[string]any{"internal": true, "phase": "tool_decision"}
		} else if strings.TrimSpace(assistant.Content) == "" {
			// 只有思考、沒有回答。留在 transcript 供稽核，但不是使用者可見的回覆。
			assistant.Metadata = map[string]any{"internal": true, "phase": "empty_answer"}
		} else if continuePlanCompletion {
			// 尚未完成計畫時，這段文字只是模型過早收尾的中間產物；保留於稽核
			// transcript 供下一輪修正，但不可當作使用者可見回答。
			assistant.Metadata = map[string]any{"internal": true, "phase": "plan_completion_check"}
		} else if completionCheckDirective != "" {
			assistant.Metadata = map[string]any{"internal": true, "phase": "completion_check", "reason": completionCheckReason}
		} else if pendingPlanDirective != "" {
			// 強制收斂或同一計畫狀態已提醒過一次時，保留模型這次的部分完成說明
			// 作為可見結果並停止迴圈，避免完成閘門與收斂階段互相驅動。
			assistant.Metadata = map[string]any{
				"termination":     "plan_no_progress",
				"plan_id":         pendingPlan.ID,
				"current_step_id": pendingPlan.CurrentStepID,
			}
		}
		if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
			return finishBudgetInTurn(exceeded, assistant, false, assistant.ToolCalls, 0, response)
		}
		if _, err := appendMessage(ctx, r.Sessions, input.Session.ID, assistant); err != nil {
			return domain.RunResult{}, err
		}
		undispatched = assistant.ToolCalls
		pendingTurnID = turnID
		turnState.toolCount = len(assistant.ToolCalls)
		if err := emitMessageLifecycle(emit, "message.end", assistant); err != nil {
			return domain.RunResult{}, err
		}
		lastAssistant = assistant
		if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
			return finishBudgetInTurn(exceeded, assistant, true, assistant.ToolCalls, 0, response)
		}
		if exceeded := budget.tokensExceeded(); exceeded != nil {
			return finishBudgetInTurn(exceeded, assistant, true, assistant.ToolCalls, 0, response)
		}

		if len(assistant.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Content) == "" {
				// 思考有內容、回答是空的，是本機模型很常見的一種收尾失敗：
				// 只吐了 <think>／harmony 的 analysis 頻道就停住。直接讓整個 Run
				// 失敗，使用者就是白等一輪；先明確要求它輸出回答本身再重試一次。
				if emptyAnswerRetries < maxEmptyAnswerRetries {
					emptyAnswerRetries++
					if err := emitEvent(emit, "run.empty_answer_retry", map[string]any{
						"turn": turn, "attempt": emptyAnswerRetries, "max_attempts": maxEmptyAnswerRetries,
						"had_reasoning": strings.TrimSpace(assistant.Reasoning) != "",
					}); err != nil {
						return domain.RunResult{}, err
					}
					if err := r.closeToollessTurn(ctx, emit, ref, "empty_answer_retry", response); err != nil {
						return domain.RunResult{}, err
					}
					completionDirective = emptyAnswerDirective()
					continue
				}
				err := errors.New("model returned neither text nor tool calls")
				if strings.TrimSpace(assistant.Reasoning) != "" {
					err = errors.New("模型只輸出了思考內容，沒有產生回答")
				}
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "failed", 0, response, err)
				return domain.RunResult{}, err
			}
			// 計畫完成度閘門：只要目前步驟尚未完成或仍待驗證，就不能把一般文字
			// 回覆當作整個長任務完成。blocked 例外，必須讓 Agent 回報阻塞並等待使用者。
			if continuePlanCompletion {
				planCompletionChecks++
				lastPlanCompletionKey = planCompletionKey
				if err := r.recordPlanCompletionCheck(ctx, emit, ref, pendingPlan, planCompletionChecks, response); err != nil {
					return domain.RunResult{}, err
				}
				completionDirective = pendingPlanDirective
				continue
			}
			// 完成度閘門：模型宣稱完成，但本次執行記錄顯示仍有未解決的工具失敗時，
			// 先讓它面對事實再決定是否接受。判定只用客觀執行記錄，不解讀模型文字。
			// 一個工具都沒執行就收尾時，同樣先讓模型面對執行記錄一次。
			if completionCheckDirective != "" && (completionCheckReason == "unresolved_tool_failure" || completionCheckReason == "no_tool_executed") {
				if err := r.recordCompletionCheck(ctx, emit, logger, ref, completion, completionCheckReason, response); err != nil {
					return domain.RunResult{}, err
				}
				completionDirective = completionCheckDirective
				continue
			}
			// 模型準備收尾時使用者又補了訊息：保留這次回答，再跑一輪回應補充內容。
			// 已是最後一輪時照常完成，未消化的訊息由 Application 在終止事件中退回。
			if input.Inputs.Pending() && turn < budget.budget.MaxTurns {
				if err := r.closeToollessTurn(ctx, emit, ref, "input_received", response); err != nil {
					return domain.RunResult{}, err
				}
				continue
			}
			if err := r.closeToollessTurn(ctx, emit, ref, "completed", response); err != nil {
				return domain.RunResult{}, err
			}
			finalMessageID = assistant.ID
			return domain.RunResult{Message: assistant, Completion: completion.completion()}, nil
		}

		terminateCount := 0
		planStepCompleted := false
		sink := &serializedSink{emit: emit}
		allowedToolCalls, toolBudgetExceeded := budget.planToolCalls(len(assistant.ToolCalls))
		executableCalls := assistant.ToolCalls[:allowedToolCalls]
		readOnlyContracts := map[string]bool{}
		for _, definition := range callableDefinitions {
			readOnlyContracts[definition.Name] = definition.ReadOnly
		}
		for index := range executableCalls {
			// 隱藏目錄中的工具不能直接綁到模型尚未看過的新契約；拒絕後會 reveal，
			// 下一輪提供定義。先前確實公開且契約未變的工具則不必重複檢索。
			executableCalls[index].ExpectedContractID = presentedContracts[executableCalls[index].Name]
		}
		groups := groupToolCalls(executableCalls, parallelTools)
		processedToolCalls := 0
		for _, group := range groups {
			if err := r.announceToolGroup(ctx, sink, ref, group); err != nil {
				return domain.RunResult{}, err
			}
			if len(group) > 1 {
				logger.Debug("executing read-only tools in parallel", "turn", turn, "turn_id", turnID, "count", len(group))
			}
			budget.addToolCalls(len(group))
			// 一旦交給群組執行，就只能採用它回傳的真實結果；外層保險只補後續
			// 尚未進入群組的呼叫，不能把已派送但保存失敗的結果說成未派送。
			undispatched = undispatched[len(group):]
			outcomes := r.runToolGroup(ctx, input.Session, group, sink, strings.TrimSpace(input.RunID), activeTools, approvalExemptTools, approvalState, loopGuard, retriever)
			toolMessages, persistErr := r.persistToolOutcomes(ctx, input.Session.ID, operationID, turnID, outcomes, readOnlyContracts)
			if persistErr != nil {
				return domain.RunResult{}, persistErr
			}
			for index, outcome := range outcomes {
				call, result := outcome.call, outcome.result
				toolResultsObserved = true
				completion.observe(call, result)
				if successfulPlanStepCompletion(call, result) {
					planStepCompleted = true
				}
				if !builtinFallbackEnabled && shouldEnableBuiltinFallback(call, result) {
					builtinFallbackEnabled = true
					if err := sink.emitEvent("tools.fallback_enabled", map[string]any{
						"trigger":      "shell_execution_failed",
						"tool_call_id": call.ID,
						"tool_name":    call.Name,
					}); err != nil {
						return domain.RunResult{}, err
					}
				}
				toolMessage := toolMessages[index]
				if failureRecallTracker.Observe(call.Name, result.IsError) && r.Memory.SpaceEnabled() {
					prompt, recalledOnFailure, err := r.recallAfterToolFailure(ctx, input.Session, call, result, operationID, logger, sink)
					if err != nil {
						return domain.RunResult{}, err
					}
					if recalledOnFailure {
						failureRecall = prompt
					}
				}
				toolStatus := toolExecutionStatus(result)
				// 只記錄名稱、狀態、耗時與輸出大小：工具參數與輸出可能含憑證或使用者資料。
				logger.Info("tool execution finished",
					"turn", turn,
					"tool_name", call.Name,
					"tool_call_id", call.ID,
					"status", toolStatus,
					"duration_ms", outcome.duration.Milliseconds(),
					"result_bytes", len(result.Content),
				)
				if err := sink.emitEvent("tool.execution.end", map[string]any{"result": result}); err != nil {
					return domain.RunResult{}, err
				}
				if err := emitMessageLifecycle(emit, "message.start", toolMessage); err != nil {
					return domain.RunResult{}, err
				}
				if err := emitMessageLifecycle(emit, "message.end", toolMessage); err != nil {
					return domain.RunResult{}, err
				}
				if result.Terminate {
					terminateCount++
				}
			}
			processedToolCalls += len(group)
			if cancelErr := ctx.Err(); cancelErr != nil {
				if exceeded := budget.wallClockExceeded(ctx); exceeded != nil {
					return finishBudgetInTurn(exceeded, assistant, true, assistant.ToolCalls[processedToolCalls:], processedToolCalls, response)
				}
				_ = r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "canceled", len(assistant.ToolCalls), response, cancelErr)
				return domain.RunResult{}, fmt.Errorf("%w: %v", domain.ErrCanceled, cancelErr)
			}
		}
		if toolBudgetExceeded != nil {
			toolBudgetExceeded.Usage = budget.usage()
			pending := assistant.ToolCalls[allowedToolCalls:]
			if err := r.finishTurn(context.WithoutCancel(ctx), input.Session.ID, operationID, turnID, turn, turnStartedAt, "budget_exceeded", len(assistant.ToolCalls), response, nil); err != nil {
				return domain.RunResult{}, err
			}
			return finishBudget(toolBudgetExceeded, assistant, true, pending)
		}
		if err := r.finishTurn(ctx, input.Session.ID, operationID, turnID, turn, turnStartedAt, "tool_calls_completed", len(assistant.ToolCalls), response, nil); err != nil {
			return domain.RunResult{}, err
		}
		if err := emitEvent(emit, "turn.end", map[string]any{
			"turn":              turn,
			"turn_id":           turnID,
			"tool_result_count": len(assistant.ToolCalls),
		}); err != nil {
			return domain.RunResult{}, err
		}
		if containsNonPlanningTool(assistant.ToolCalls) {
			toolTurns++
		}
		// 自主工具回合上限應限制單一步驟的失控擴張，不應讓前面已完成的步驟
		// 吃掉整份計畫的額度。完成或略過一個經後端接受的步驟後，下一步重新計算。
		if planStepCompleted {
			toolTurns = 0
		}
		if terminateCount == len(executableCalls) {
			return domain.RunResult{}, errors.New("all tool results requested termination before a final assistant message")
		}
	}
	if exceeded := budget.turnsExceeded(); exceeded != nil {
		return finishBudget(exceeded, lastAssistant, true, nil)
	}
	return domain.RunResult{}, errors.New("run completed without a final assistant message")
}

// completeBudget 把資源上限視為可辨識的正常終止。尚未執行的 tool call 會得到
// 合成結果，否則這次正常收尾反而會把 transcript 留在 Provider 拒絕的狀態。
func (r *Runner) completeBudget(
	ctx context.Context,
	sessionID string,
	operationID string,
	assistant domain.Message,
	assistantPersisted bool,
	pending []domain.ToolCall,
	exceeded *domain.RunBudgetExceeded,
	emit EventSink,
) (domain.RunResult, error) {
	if exceeded == nil {
		exceeded = &domain.RunBudgetExceeded{Resource: domain.RunBudgetResourceTurns}
	}
	if strings.TrimSpace(assistant.ID) == "" {
		assistant.ID = domain.NewID("msg")
		assistant.SessionID = sessionID
		assistant.Role = "assistant"
		if assistant.CreatedAt.IsZero() {
			assistant.CreatedAt = time.Now().UTC()
		}
		assistantPersisted = false
		if err := emitMessageLifecycle(emit, "message.start", assistant); err != nil {
			return domain.RunResult{}, err
		}
	}
	if strings.TrimSpace(assistant.Content) == "" && len(assistant.ToolCalls) == 0 {
		assistant.Content = budgetPauseMessage(exceeded.Resource)
		assistant.Metadata = valueutil.CloneMap(assistant.Metadata)
		if assistant.Metadata == nil {
			assistant.Metadata = map[string]any{}
		}
		assistant.Metadata["synthesized"] = true
		assistant.Metadata["termination"] = "budget_exceeded"
		assistant.Metadata["budget_resource"] = exceeded.Resource
	}
	if !assistantPersisted {
		persistCtx, cancel := persistContext(ctx)
		_, err := appendMessage(persistCtx, r.Sessions, sessionID, assistant)
		cancel()
		if err != nil {
			return domain.RunResult{}, err
		}
	}
	pendingMessages, err := r.persistUndispatched(ctx, sessionID, operationID, "", pending, "工具未執行：Run 已達 "+exceeded.Resource+" 上限。", exceeded.Resource)
	if err != nil {
		return domain.RunResult{}, err
	}
	if !assistantPersisted {
		if err := emitMessageLifecycle(emit, "message.end", assistant); err != nil {
			return domain.RunResult{}, err
		}
	}
	for _, message := range pendingMessages {
		if err := emitMessageLifecycle(emit, "message.start", message); err != nil {
			return domain.RunResult{}, err
		}
		if err := emitMessageLifecycle(emit, "message.end", message); err != nil {
			return domain.RunResult{}, err
		}
	}
	// 安全上限可能發生在內部工具訊息、空訊息，或模型逾時後才回傳內容的邊界。
	// 既有 transcript 全部保留，但 Result 必須是獨立且可見的暫停說明，避免 UI
	// 隱藏內部訊息，或把逾時後的模型文字誤當成已正常完成。
	pauseContent := budgetPauseMessage(exceeded.Resource)
	if internalAssistantMessage(assistant) || strings.TrimSpace(assistant.Content) != pauseContent {
		visible := domain.Message{
			ID:        domain.NewID("msg"),
			SessionID: sessionID,
			Role:      "assistant",
			Content:   pauseContent,
			Metadata: map[string]any{
				"synthesized":     true,
				"termination":     "budget_exceeded",
				"budget_resource": exceeded.Resource,
			},
			CreatedAt: time.Now().UTC(),
		}
		if err := emitMessageLifecycle(emit, "message.start", visible); err != nil {
			return domain.RunResult{}, err
		}
		persistCtx, cancel := persistContext(ctx)
		_, err := appendMessage(persistCtx, r.Sessions, sessionID, visible)
		cancel()
		if err != nil {
			return domain.RunResult{}, err
		}
		if err := emitMessageLifecycle(emit, "message.end", visible); err != nil {
			return domain.RunResult{}, err
		}
		assistant = visible
	}
	if err := emitEvent(emit, "run.budget_exceeded", map[string]any{
		"resource": exceeded.Resource,
		"limit":    exceeded.Limit,
		"observed": exceeded.Observed,
		"usage":    exceeded.Usage,
	}); err != nil {
		return domain.RunResult{}, err
	}
	return domain.RunResult{Message: assistant, BudgetExceeded: exceeded}, nil
}

func successfulPlanStepCompletion(call domain.ToolCall, result domain.ToolExecution) bool {
	if result.IsError || call.Name != "plan_step_update" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(call.Arguments["status"])))
	return status == string(domain.PlanStepStatusCompleted) || status == string(domain.PlanStepStatusSkipped)
}

func internalAssistantMessage(message domain.Message) bool {
	if len(message.ToolCalls) > 0 {
		return true
	}
	value, _ := message.Metadata["internal"].(bool)
	return value
}

func (r *Runner) recallMemory(ctx context.Context, session domain.Session, query, operationID string) (memory.RecallResult, error) {
	if r.Memory == nil {
		return memory.RecallResult{}, nil
	}
	result, err := r.Memory.Recall(ctx, session, query)
	if err != nil {
		return memory.RecallResult{}, err
	}
	if len(result.Memories) == 0 {
		return result, nil
	}
	_, err = appendRecord(ctx, r.Sessions, session.ID, domain.SessionEntryMemoryRecall, map[string]any{
		"operation_id": operationID,
		"scope":        memory.ScopeForSessionWithSpace(session, r.Memory.SpaceEnabled()),
		"memory_ids":   memoryIDs(result.Memories),
		"truncated":    result.Truncated,
	})
	if err != nil {
		return memory.RecallResult{}, err
	}
	return result, nil
}

func memoryIDs(values []domain.Memory) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.ID)
	}
	return result
}

func (r *Runner) finishTurn(ctx context.Context, sessionID, operationID, turnID string, turn int, startedAt time.Time, status string, toolCount int, response domain.ModelResponse, cause error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data := map[string]any{
		"operation_id":        operationID,
		"turn_id":             turnID,
		"turn":                turn,
		"status":              status,
		"tool_result_count":   toolCount,
		"stop_reason":         response.StopReason,
		"provider_request_id": response.ProviderRequestID,
		"usage":               response.Usage,
		"duration_ms":         time.Since(startedAt).Milliseconds(),
	}
	if cause != nil {
		data["error"] = cause.Error()
	}
	_, err := appendRecord(ctx, r.Sessions, sessionID, domain.SessionEntryTurnFinished, data)
	if err == nil {
		if state, _ := ctx.Value(turnLifecycleKey{}).(*turnLifecycle); state != nil && state.id == turnID {
			state.finished = true
		}
	}
	return err
}

// persistContext 供「已經發生的事實」使用：工具執行完成後，結果必須寫進 transcript，
// 否則會留下沒有結果的 tool_call，使後續請求不符合 Provider 的 tool call 協定。
func persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// repairMessages 是 repairToolCallPairs 對純訊息序列的包裝，
// 供沒有 ContextManager 的組裝方式使用。
func repairMessages(messages []domain.Message) []domain.Message {
	repaired := repairToolCallPairs(messagesFromDomain(messages))
	result := make([]domain.Message, len(repaired))
	for index, item := range repaired {
		result[index] = item.Message
	}
	return result
}

func appendMessage(ctx context.Context, repository ports.SessionRepository, sessionID string, message domain.Message) (domain.SessionEntry, error) {
	copyMessage := message
	// Reasoning 只供目前 Run 的即時事件使用，不寫入 Session transcript。
	// 保留原始 message 不變，讓後續 message.end 仍能把本輪思考內容送給目前 UI。
	copyMessage.Reasoning = ""
	return repository.AppendEntry(ctx, sessionID, domain.SessionEntry{
		ID:        domain.NewID("entry"),
		SessionID: sessionID,
		Type:      domain.SessionEntryMessage,
		Message:   &copyMessage,
		CreatedAt: time.Now().UTC(),
	})
}

func appendRecord(ctx context.Context, repository ports.SessionRepository, sessionID, entryType string, data map[string]any) (domain.SessionEntry, error) {
	return repository.AppendEntry(ctx, sessionID, domain.SessionEntry{
		ID:        domain.NewID("entry"),
		SessionID: sessionID,
		Type:      entryType,
		Data:      valueutil.CloneMap(data),
		CreatedAt: time.Now().UTC(),
	})
}

func normalizeToolCalls(input []domain.ToolCall) ([]domain.ToolCall, error) {
	if len(input) == 0 {
		return nil, nil
	}
	result := make([]domain.ToolCall, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, call := range input {
		call.Name = strings.TrimSpace(call.Name)
		if call.Name == "" {
			return nil, fmt.Errorf("tool call %d has no tool name", index)
		}
		call.ID = strings.TrimSpace(call.ID)
		if call.ID == "" {
			call.ID = domain.NewID("call")
		}
		if _, exists := seen[call.ID]; exists {
			return nil, fmt.Errorf("duplicate tool call id %q", call.ID)
		}
		seen[call.ID] = struct{}{}
		call.Arguments = valueutil.CloneMap(call.Arguments)
		if call.Arguments == nil {
			call.Arguments = map[string]any{}
		}
		if _, err := json.Marshal(call.Arguments); err != nil {
			return nil, fmt.Errorf("%w: tool call %d arguments are not JSON encodable", domain.ErrProviderProtocol, index)
		}
		result[index] = call
	}
	return result, nil
}

// effectiveToolCallMode 讓 Provider 以能力宣告選擇可靠的工具協定。全域 native
// 設定仍會強制使用原生工具；instruction 則只作為未宣告原生能力的相容後備。
func effectiveToolCallMode(model ports.Model, providerID string, configured ToolCallMode) ToolCallMode {
	if configured == ToolCallModeNative {
		return ToolCallModeNative
	}
	catalog, ok := model.(ports.ProviderCatalog)
	if !ok {
		return configured
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = catalog.DefaultProviderID()
	}
	for _, provider := range catalog.ListProviders() {
		if provider.ID == providerID && provider.SupportsNativeToolCalls {
			return ToolCallModeNative
		}
	}
	return configured
}

func operationStatus(ctx context.Context, err error) string {
	if err == nil {
		return domain.OperationStatusCompleted
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, domain.ErrCanceled) || ctx.Err() != nil {
		return domain.OperationStatusCanceled
	}
	return domain.OperationStatusFailed
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func definitionNamed(definitions []domain.ToolDefinition, name string) bool {
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(definition.Name), name) {
			return true
		}
	}
	return false
}

// enforceHistoryCharacterLimit 在超出字元上限時由舊而新丟棄訊息，直到裝得下。
//
// 這是壓縮沒能把歷史降下來時的最後手段：寧可讓模型少看到幾則舊訊息，也不要送出
// 一份它要花二十分鐘 prefill 的請求——那對使用者而言與當機沒有分別。
func (r *Runner) enforceHistoryCharacterLimit(messages []domain.Message, logger *slog.Logger, turn int, sessions ...domain.Session) []domain.Message {
	limit := r.Context.HistoryCharacterLimit(sessions...)
	total := historyCharacters(messages)
	if limit <= 0 || total <= limit || len(messages) <= 1 {
		return messages
	}
	kept := 0
	size := 0
	for index := len(messages) - 1; index >= 0; index-- {
		size += historyCharacters(messages[index : index+1])
		if size > limit && kept > 0 {
			break
		}
		kept++
	}
	// 裁切與摘要使用相同的原子邊界，不可先丟呼叫再由 repair 移除孤立結果。
	_, retained := splitForCompaction(messagesFromDomain(messages), kept)
	trimmed := sequencedToMessages(retained)
	logger.Warn("history trimmed before the request was assembled",
		"turn", turn,
		"limit_chars", limit,
		"before_chars", total,
		"after_chars", historyCharacters(trimmed),
		"before_messages", len(messages),
		"after_messages", len(trimmed),
	)
	return trimmed
}

func historyCharacters(messages []domain.Message) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content) + utf8.RuneCountInString(message.Reasoning) +
			toolCallCharacters(message.ToolCalls) + message.Continuation.Bytes()
	}
	return total
}

// retrievalQuery 是工具檢索的查詢字串：這次的需求，加上同一個 session 最近幾則
// 使用者訊息。
//
// 跟進提問常常只剩代名詞（「那部門呢？」「再查一次」），只看目前這一句會什麼都
// 檢索不到，等於白白多花一輪讓模型自己去 find_tools。取不到歷史時退回目前這句，
// 檢索品質降級可以接受，Run 不能因此失敗。
func (r *Runner) retrievalQuery(ctx context.Context, input Input) string {
	current := strings.TrimSpace(input.UserInput)
	if r == nil || r.Sessions == nil {
		return current
	}
	// 只要最近幾則使用者訊息，不必把整份 transcript 解碼一遍。取的則數比
	// retrievalQueryMessages 寬鬆得多：中間夾雜的助理訊息與工具觀察也算在內，
	// 太小會在長工具序列之後找不到任何使用者訊息。
	messages, err := r.Sessions.ListRecentMessages(ctx, input.Session.ID, retrievalQueryScanMessages)
	if err != nil {
		return current
	}
	parts := []string{current}
	budget := retrievalQueryRunes - utf8.RuneCountInString(current)
	for index := len(messages) - 1; index >= 0 && budget > 0 && len(parts) <= retrievalQueryMessages; index-- {
		if !strings.EqualFold(messages[index].Role, "user") {
			continue
		}
		value := strings.TrimSpace(messages[index].Content)
		if value == "" || value == current {
			continue
		}
		if count := utf8.RuneCountInString(value); count > budget {
			value = string([]rune(value)[:budget])
			budget = 0
		} else {
			budget -= count
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

func toolExposed(definitions []domain.ToolDefinition, name string) bool {
	return definitionNamed(definitions, name)
}

func categoryExposed(definitions []domain.ToolDefinition, category string) bool {
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(definition.Category), category) {
			return true
		}
	}
	return false
}

// mutationExposed 判斷本輪是否有會寫入本機資源的內建工具。
//
// 這段守則講的是檔案寫入的驗證方式（bytes、hash、原子改名），只有內建寫入工具適用。
// MCP 工具一律標記 RequiresPermission，但它們的副作用由遠端服務定義、由 Approval 把關，
// 套用這段檔案導向的守則只會多送一段不相干的文字。
func mutationExposed(definitions []domain.ToolDefinition) bool {
	for _, definition := range definitions {
		name := strings.ToLower(strings.TrimSpace(definition.Name))
		if definition.ReadOnly || name == systemShellToolName ||
			strings.HasPrefix(name, "plan_") || strings.HasPrefix(name, "mcp__") {
			continue
		}
		if definition.RequiresPermission {
			return true
		}
	}
	return false
}

func splitCurrentUserMessage(messages []domain.Message, current domain.Message) ([]domain.Message, string) {
	currentIndex := -1
	if current.ID != "" {
		for index, message := range messages {
			if message.ID == current.ID {
				currentIndex = index
				break
			}
		}
	}
	if currentIndex < 0 {
		return messages, strings.TrimSpace(current.Content)
	}
	// 第一次模型請求時，目前 user 訊息位於 transcript 尾端，可拆成
	// ModelRequest.UserPrompt。工具或中間 assistant 訊息寫入後，它已不再是尾端；
	// 此時必須保留原始順序，否則把 user 訊息重新追加到 function_call_output
	// 後面，Provider 會誤認為使用者在工具成功後再次提出相同問題。
	if currentIndex != len(messages)-1 {
		return messages, ""
	}
	history := make([]domain.Message, 0, len(messages)-1)
	history = append(history, messages[:currentIndex]...)
	return history, messages[currentIndex].Content
}

func imitatedToolCallName(content string, definitions []domain.ToolDefinition) string {
	compact := strings.ToLower(strings.Join(strings.Fields(content), ""))
	for _, definition := range definitions {
		name := strings.ToLower(strings.TrimSpace(definition.Name))
		if name != "" && strings.Contains(compact, "to="+name) {
			return definition.Name
		}
	}
	return ""
}

func emitMessageLifecycle(emit EventSink, eventType string, message domain.Message) error {
	return emitEvent(emit, eventType, map[string]any{"message": message})
}

func emitEvent(emit EventSink, eventType string, payload map[string]any) error {
	if emit == nil {
		return nil
	}
	return emit(domain.EngineEvent{Type: eventType, Payload: payload})
}

func cloneToolCalls(input []domain.ToolCall) []domain.ToolCall {
	if len(input) == 0 {
		return nil
	}
	out := make([]domain.ToolCall, len(input))
	for index, call := range input {
		out[index] = call
		out[index].Arguments = valueutil.CloneMap(call.Arguments)
	}
	return out
}

// CompactSession 手動壓縮一個 Session 的對話歷史。
//
// 系統提示與工具目錄都會計入 context，估算時必須帶上，否則算出來的「壓縮後」
// 會比實際送出時樂觀，使用者按完仍然可能一送就爆。
func (r *Runner) CompactSession(ctx context.Context, session domain.Session) (domain.ContextCompactionResult, error) {
	if r == nil || r.Context == nil {
		return domain.ContextCompactionResult{}, fmt.Errorf("%w: context manager is unavailable", domain.ErrInvalidInput)
	}
	definitions, err := r.Tools.Definitions(ctx, session)
	if err != nil {
		return domain.ContextCompactionResult{}, err
	}
	return r.Context.CompactNow(ctx, session, r.SystemPrompt, definitions)
}
