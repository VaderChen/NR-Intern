package approval

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Done 的首次讀取代表 Wait 已登記完成，避免用 sleep 猜測 goroutine 的進度。
type observedWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestApprovalCancelWakesWaiterSmoke(t *testing.T) {
	c := NewCoordinator(nil)
	request := domain.ToolApprovalRequest{ID: "approval", RunID: "run", ToolCallID: "call"}
	if err := c.Begin(request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed := &observedWaitContext{Context: ctx, entered: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := c.Wait(observed, request.ID); result <- err }()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("waiter 未啟動")
	}
	if _, err := c.Wait(ctx, request.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("接受重複等待者", err)
	}
	c.Cancel(request.ID)
	c.Cancel(request.ID)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("未被 Cancel 喚醒", err)
		}
	case <-ctx.Done():
		t.Fatal("Cancel 後仍卡住")
	}
}

func TestApprovalOldCleanupCannotRemoveReplacementSmoke(t *testing.T) {
	c := NewCoordinator(nil)
	request := domain.ToolApprovalRequest{ID: "approval", RunID: "run", ToolCallID: "call"}
	if err := c.Begin(request); err != nil {
		t.Fatal(err)
	}
	old := c.pending[request.ID]
	c.Cancel(request.ID)
	if err := c.Begin(request); err != nil {
		t.Fatal(err)
	}
	c.cancelPending(request.ID, old)
	if err := c.Decide(request.RunID, domain.ToolApprovalDecisionInput{ApprovalID: request.ID, Decision: domain.ToolApprovalApprove}); err != nil {
		t.Fatal("新請求被舊等待者刪除", err)
	}
	if err := c.Begin(domain.ToolApprovalRequest{ID: "second", RunID: "run", ToolCallID: "other"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("舊決策尚未取走就接受另一核准", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Wait(ctx, request.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("已取消 context 仍取得核准", err)
	}
}
