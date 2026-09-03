package filestore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"AgenticService/src/domain"
)

// runs.json 每次 Save 都整份重寫；沒有上限，寫入成本就正比於歷來 run 總數。
func TestRunRetentionCapsTheStore(t *testing.T) {
	repository, err := NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new run repository: %v", err)
	}
	repository.retention = 10
	base := time.Now().UTC().Add(-24 * time.Hour)
	for index := 0; index < 25; index++ {
		if err := repository.Save(context.Background(), domain.Run{
			ID:        fmt.Sprintf("run_%02d", index),
			SessionID: "session-1",
			Status:    domain.RunStatusCompleted,
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatalf("save run %d: %v", index, err)
		}
	}
	values, err := repository.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(values) != 10 {
		t.Fatalf("retained %d runs, want 10", len(values))
	}
	if values[0].ID != "run_24" {
		t.Fatalf("newest retained run = %s, want run_24", values[0].ID)
	}
	pruned := repository.TakePrunedRunIDs()
	if len(pruned) != 15 {
		t.Fatalf("reported %d pruned runs, want 15", len(pruned))
	}
	if len(repository.TakePrunedRunIDs()) != 0 {
		t.Fatal("pruned ids must be reported only once")
	}
}

// 未結束的 run 等一下要寫回狀態，不能被淘汰——paused 與 waiting_approval
// 看起來停住了，其實都還會回來。
func TestRunRetentionKeepsUnfinishedRuns(t *testing.T) {
	repository, err := NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new run repository: %v", err)
	}
	repository.retention = 3
	base := time.Now().UTC().Add(-time.Hour)
	if err := repository.Save(context.Background(), domain.Run{
		ID: "run_oldest_running", SessionID: "s", Status: domain.RunStatusRunning, CreatedAt: base,
	}); err != nil {
		t.Fatalf("save running run: %v", err)
	}
	for index := 1; index <= 6; index++ {
		if err := repository.Save(context.Background(), domain.Run{
			ID:        fmt.Sprintf("run_%02d", index),
			SessionID: "s",
			Status:    domain.RunStatusCompleted,
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatalf("save run %d: %v", index, err)
		}
	}
	if _, err := repository.Get(context.Background(), "run_oldest_running"); err != nil {
		t.Fatalf("a running run must survive pruning: %v", err)
	}
}

// 失效記憶留在檔案裡會拖慢每一次寫入，因為整份檔案每次都重寫。
func TestPurgeInactiveMemories(t *testing.T) {
	repository, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new memory repository: %v", err)
	}
	ctx := context.Background()
	kept, err := repository.Remember(ctx, domain.RememberMemoryInput{Scope: "s", Kind: domain.MemoryKindDecision, Content: "仍然生效的決策"})
	if err != nil {
		t.Fatalf("remember kept: %v", err)
	}
	dropped, err := repository.Remember(ctx, domain.RememberMemoryInput{Scope: "s", Kind: domain.MemoryKindDecision, Content: "已被遺忘的決策"})
	if err != nil {
		t.Fatalf("remember dropped: %v", err)
	}
	if _, err := repository.Forget(ctx, "s", dropped.ID, "測試"); err != nil {
		t.Fatalf("forget: %v", err)
	}

	// 才剛失效的不能移除：稽核保留期還沒過。
	removed, err := repository.PurgeInactive(ctx, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("purge within retention: %v", err)
	}
	if removed != 0 {
		t.Fatalf("purged %d memories inside the retention window, want 0", removed)
	}

	removed, err = repository.PurgeInactive(ctx, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("purge past retention: %v", err)
	}
	if removed != 1 {
		t.Fatalf("purged %d memories, want 1", removed)
	}
	if _, err := repository.Get(ctx, "s", kept.ID); err != nil {
		t.Fatalf("an active memory must never be purged: %v", err)
	}
	if _, err := repository.Get(ctx, "s", dropped.ID); err == nil {
		t.Fatal("the forgotten memory should be gone after the retention window")
	}
}
