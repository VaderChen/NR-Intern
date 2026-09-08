package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"context"
	"errors"
	"testing"
)

// 讀不到 Run 記錄時，事件仍要寫得出去。
//
// 終態事件的去重需要先讀回 Run 的目前狀態，但那是「盡量不要寫出重複終止事件」
// 的保護，不是正確性不變量。曾經把讀取失敗改成直接 return err，結果是一次暫時性
// 的讀取失敗就會殺掉一個本來健康的 run——事件是診斷通道，run 才是工作本身。
//
// 這一條釘住優先序：讀不到就跳過去重、記一筆 warn，不要讓事件寫入失敗。
func TestAppendEventSurvivesAnUnreadableRunRecord(t *testing.T) {
	service, _ := newTestService(t, lockedPolicy())
	sequence := int64(0)
	run := domain.Run{ID: "run_missing", SessionID: "session_1", AgentID: "agent_test"}

	if err := service.appendEvent(run, &sequence, "run.started", map[string]any{}); err != nil {
		t.Fatalf("Run 記錄讀不到時事件仍應寫得出去，得到：%v", err)
	}
	if sequence != 1 {
		t.Fatalf("事件序號應前進，得到 %d", sequence)
	}
}

// 讀得到而且已經終止時，去重照常生效：晚到的 Provider 回呼不得排在終止事件之後。
func TestAppendEventStillRejectsLateEventsAfterTerminal(t *testing.T) {
	directory := t.TempDir()
	runs, err := filestore.NewRunRepository(directory)
	if err != nil {
		t.Fatalf("NewRunRepository: %v", err)
	}
	engine := newFakeEngine()
	registry, err := NewRegistry(engine)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	plans, err := filestore.NewPlanRepository(directory)
	if err != nil {
		t.Fatalf("NewPlanRepository: %v", err)
	}
	service, err := NewService(Dependencies{
		Registry: registry, Runs: runs, Events: fakeEvents{}, Projects: fakeProjects{},
		Workspaces: fakeWorkspaces{}, Providers: fakeProviders{}, Plans: plans,
		Permissions: lockedPolicy(), Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })

	run := domain.Run{ID: "run_terminal", SessionID: "session_1", AgentID: "agent_test", Status: domain.RunStatusCanceled}
	if err := runs.Save(context.Background(), run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sequence := int64(0)
	err = service.appendEvent(run, &sequence, "run.progress", map[string]any{})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("終止之後的晚到事件應被擋下，得到：%v", err)
	}
}
