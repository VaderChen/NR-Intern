package harness

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"time"
)

var errRunBudgetWallClock = errors.New("run wall-clock budget exhausted")

type runBudgetTracker struct {
	budget    domain.RunBudget
	startedAt time.Time
	turns     int
	tokens    int
	toolCalls int
}

func newRunBudgetTracker(budget domain.RunBudget, startedAt time.Time) *runBudgetTracker {
	if budget.MaxTurns <= 0 {
		budget.MaxTurns = DefaultMaxTurns
	}
	return &runBudgetTracker{budget: budget, startedAt: startedAt}
}

// context 讓 wall-clock 成為真正的執行上限；只在回合邊界檢查會讓長時間 shell/SSH
// 繼續佔用資源，失去 budget 作為安全煞車的意義。
func (t *runBudgetTracker) context(parent context.Context) (context.Context, context.CancelFunc) {
	if t.budget.MaxWallClock <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeoutCause(parent, t.budget.MaxWallClock, errRunBudgetWallClock)
}

func (t *runBudgetTracker) startTurn(turn int) {
	t.turns = turn
}

func (t *runBudgetTracker) turnsExceeded() *domain.RunBudgetExceeded {
	if t.budget.MaxTurns <= 0 || t.turns < t.budget.MaxTurns {
		return nil
	}
	return t.exceeded(
		domain.RunBudgetResourceTurns,
		int64(t.budget.MaxTurns),
		int64(t.turns),
	)
}

func (t *runBudgetTracker) addUsage(usage domain.Usage) *domain.RunBudgetExceeded {
	t.tokens += usageTokens(usage)
	if t.budget.MaxTokens > 0 && t.tokens >= t.budget.MaxTokens {
		return t.exceeded(domain.RunBudgetResourceTokens, int64(t.budget.MaxTokens), int64(t.tokens))
	}
	return nil
}

// planToolCalls 只允許在剩餘額度內的前綴。模型同一輪要求的其餘呼叫會得到合成的
// budget 結果，避免執行額外副作用，同時維持 assistant/tool 配對協定。
func (t *runBudgetTracker) planToolCalls(requested int) (int, *domain.RunBudgetExceeded) {
	if requested <= 0 || t.budget.MaxToolCalls <= 0 {
		return requested, nil
	}
	remaining := t.budget.MaxToolCalls - t.toolCalls
	if remaining >= requested {
		return requested, nil
	}
	if remaining < 0 {
		remaining = 0
	}
	return remaining, t.exceeded(
		domain.RunBudgetResourceToolCalls,
		int64(t.budget.MaxToolCalls),
		int64(t.toolCalls+requested),
	)
}

func (t *runBudgetTracker) addToolCalls(count int) {
	if count > 0 {
		t.toolCalls += count
	}
}

func (t *runBudgetTracker) wallClockExceeded(ctx context.Context) *domain.RunBudgetExceeded {
	if t.budget.MaxWallClock <= 0 || !errors.Is(context.Cause(ctx), errRunBudgetWallClock) {
		return nil
	}
	return t.exceeded(
		domain.RunBudgetResourceWallClock,
		t.budget.MaxWallClock.Milliseconds(),
		time.Since(t.startedAt).Milliseconds(),
	)
}

func (t *runBudgetTracker) exceeded(resource string, limit, observed int64) *domain.RunBudgetExceeded {
	return &domain.RunBudgetExceeded{
		Resource: resource,
		Limit:    limit,
		Observed: observed,
		Usage:    t.usage(),
	}
}

func (t *runBudgetTracker) usage() domain.RunBudgetUsage {
	return domain.RunBudgetUsage{
		Turns:                 t.turns,
		WallClockMilliseconds: time.Since(t.startedAt).Milliseconds(),
		Tokens:                t.tokens,
		ToolCalls:             t.toolCalls,
	}
}

func usageTokens(usage domain.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens
}
