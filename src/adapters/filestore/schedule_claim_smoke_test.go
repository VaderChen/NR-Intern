package filestore

import (
	"AgenticService/src/domain"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleOccurrenceClaimSmoke(t *testing.T) {
	root := t.TempDir()
	repo, err := NewScheduleRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := repo.Create(ctx, domain.CreateScheduleInput{WorkspaceID: "workspace", Name: "smoke", Prompt: "check", Recurrence: domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	due := *s.NextRunAt
	next := due.Add(time.Minute)
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := repo.ClaimOccurrence(ctx, s.ID, due, next)
			if err != nil {
				t.Error(err)
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claimed=%d", claimed.Load())
	}
	reopened, err := NewScheduleRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reopened.ClaimOccurrence(ctx, s.ID, due, next); err != nil || ok {
		t.Fatalf("duplicate after restart: %v %v", ok, err)
	}
}
