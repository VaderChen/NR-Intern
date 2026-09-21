package question

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type observedWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestQuestionCancelWakesWaiterSmoke(t *testing.T) {
	c := NewCoordinator()
	if err := c.Begin(domain.UserQuestion{ID: "question"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed := &observedWaitContext{Context: ctx, entered: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := c.Wait(observed, "question"); result <- err }()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("waiter 未啟動")
	}
	if _, err := c.Wait(ctx, "question"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("接受重複等待者", err)
	}
	c.Cancel("question")
	c.Cancel("question")
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("未被 Cancel 喚醒", err)
		}
	case <-ctx.Done():
		t.Fatal("Cancel 後仍卡住")
	}
}

func TestQuestionOldCleanupCannotRemoveReplacementSmoke(t *testing.T) {
	c := NewCoordinator()
	if err := c.Begin(domain.UserQuestion{ID: "question"}); err != nil {
		t.Fatal(err)
	}
	old := c.pending["question"]
	c.Cancel("question")
	if err := c.Begin(domain.UserQuestion{ID: "question"}); err != nil {
		t.Fatal(err)
	}
	c.cancelPending("question", old)
	if err := c.Answer(domain.UserQuestionAnswer{QuestionID: "question", Custom: "合成測試"}); err != nil {
		t.Fatal("新請求被舊等待者刪除", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Wait(ctx, "question"); !errors.Is(err, context.Canceled) {
		t.Fatal("已取消 context 仍取得答案", err)
	}
}
