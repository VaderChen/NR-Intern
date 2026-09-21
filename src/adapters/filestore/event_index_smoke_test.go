package filestore

import (
	"AgenticService/src/domain"
	"context"
	"os"
	"testing"
)

func TestEventOffsetIndexSmoke(t *testing.T) {
	r, err := NewRunEventRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := int64(1); i <= 30; i++ {
		if err := r.Append(ctx, domain.Event{ID: domain.NewID("event"), RunID: "run_smoke", Type: "text", Sequence: i}); err != nil {
			t.Fatal(err)
		}
		events, err := r.List(ctx, "run_smoke", i-1)
		if err != nil || len(events) != 1 || events[0].Sequence != i {
			t.Fatalf("incremental=%v err=%v", events, err)
		}
	}
	index := r.indexes["run_smoke"]
	if index == nil || len(index.ends) != 31 || index.ends[29] <= 0 {
		t.Fatal("missing seek index")
	}
	file, _ := r.eventPath("run_smoke")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("corrupted\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.List(ctx, "run_smoke", 30); err == nil {
		t.Fatal("changed file bypassed validation")
	}
}
