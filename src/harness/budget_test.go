package harness

import (
	"AgenticService/src/domain"
	"testing"
	"time"
)

func TestRunBudgetTrackerAccumulatesMultiTurnUsage(t *testing.T) {
	tracker := newRunBudgetTracker(domain.RunBudget{MaxTurns: 4}, time.Now())
	tracker.addUsage(domain.Usage{InputTokens: 120, OutputTokens: 8, TotalTokens: 128})
	tracker.addUsage(domain.Usage{InputTokens: 240, OutputTokens: 12})

	usage := tracker.usageSnapshot()
	if usage.InputTokens != 360 || usage.OutputTokens != 20 || usage.Total() != 380 {
		t.Fatalf("usage = %+v, want input=360 output=20 total=380", usage)
	}
}
