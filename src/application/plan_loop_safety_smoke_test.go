package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"sync/atomic"
	"testing"
)

func TestSmokePlanLoopPausesAfterRefusal(t *testing.T) {
	var calls atomic.Int32
	service, _ := loopTestService(t, func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
		calls.Add(1)
		return domain.RunResult{Message: domain.Message{Role: "assistant", Content: "拒絕執行", StopReason: "refusal"}}, nil
	})
	session, plan := loopSessionWithPlan(t, service)
	if _, err := service.StartPlanLoop(context.Background(), session.ID, plan.ID, 5); err != nil {
		t.Fatal(err)
	}
	final := waitForLoop(t, service, session.ID, plan.ID, func(value domain.Plan) bool {
		return value.Loop != nil && value.Loop.Status == domain.PlanLoopStatusPaused
	})
	if calls.Load() != 1 || final.Loop.StoppedBy != domain.PlanLoopStopRunFailed {
		t.Fatalf("calls=%d loop=%+v", calls.Load(), final.Loop)
	}
}
