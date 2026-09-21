package filestore

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRetentionSkipsProtectedAndKeepsScanningSmoke(t *testing.T) {
	r, err := NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.retention = 3
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.ProtectRun("protected")
	for i, run := range []domain.Run{
		{ID: "active", Status: domain.RunStatusRunning},
		{ID: "protected", Status: domain.RunStatusCanceled},
		{ID: "old", Status: domain.RunStatusCompleted},
		{ID: "new", Status: domain.RunStatusCompleted},
	} {
		run.SessionID, run.CreatedAt = "session", base.Add(time.Duration(i)*time.Second)
		run.Usage = &domain.RunUsage{TotalTokens: 10}
		if err := r.Save(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := r.List(ctx, "session")
	if err != nil || len(runs) != 3 {
		t.Fatalf("保留數未收斂：%d %v", len(runs), err)
	}
	for _, id := range []string{"active", "protected", "new"} {
		if _, err := r.Get(ctx, id); err != nil {
			t.Fatal("保護項目或最新結果遺失", id, err)
		}
	}
	if ids := r.TakePrunedRunIDs(); len(ids) != 1 || ids[0] != "old" {
		t.Fatalf("淘汰順序不正確：%v", ids)
	}
	usage, err := r.ListSessionRunUsage(ctx, "session")
	if err != nil || len(usage) != 4 {
		t.Fatal("明細淘汰影響用量快照", len(usage), err)
	}
}

func TestRetentionMayExceedLimitOnlyForProtectedRunsSmoke(t *testing.T) {
	r, err := NewRunRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.retention = 1
	for i := 0; i < 3; i++ {
		if err := r.Save(context.Background(), domain.Run{ID: fmt.Sprint("active", i), SessionID: "session", Status: domain.RunStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	values, err := r.List(context.Background(), "")
	if err != nil || len(values) != 3 || len(r.TakePrunedRunIDs()) != 0 {
		t.Fatal("進行中 Run 不得為符合上限而刪除", err)
	}
}
