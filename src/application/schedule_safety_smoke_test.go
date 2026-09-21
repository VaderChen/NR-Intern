package application

import (
	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"errors"
	"testing"
	"time"
)

type failingScheduleStore struct {
	ports.ScheduleRepository
	claimErr bool
}

func (s failingScheduleStore) ClaimOccurrence(ctx context.Context, id string, due, next time.Time) (domain.Schedule, bool, error) {
	if s.claimErr {
		return domain.Schedule{}, false, errors.New("synthetic persistence failure")
	}
	return s.ScheduleRepository.(ports.ScheduleOccurrenceClaimer).ClaimOccurrence(ctx, id, due, next)
}
func (s failingScheduleStore) MarkTriggered(context.Context, string, domain.ScheduleRunState) (domain.Schedule, error) {
	return domain.Schedule{}, errors.New("synthetic result persistence failure")
}

func TestSmokeScheduleDoesNotDispatchWithoutDurableClaim(t *testing.T) {
	for _, failClaim := range []bool{true, false} {
		service, engine := loopTestService(t, func(context.Context, domain.RunInput, ports.AgentEventSink) (domain.RunResult, error) {
			return domain.RunResult{}, nil
		})
		repo, err := filestore.NewScheduleRepository(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		service.schedules = failingScheduleStore{ScheduleRepository: repo, claimErr: failClaim}
		ctx := context.Background()
		schedule, err := service.CreateSchedule(ctx, domain.CreateScheduleInput{WorkspaceID: "workspace_1", Name: "test", Prompt: "synthetic", Recurrence: domain.ScheduleRecurrence{Frequency: domain.ScheduleFrequencyInterval, IntervalMinutes: 60}})
		if err != nil {
			t.Fatal(err)
		}
		// 直接測試派送 use case；建立中的 Service 沒有啟動背景排程。
		next := schedule.NextRunAt.Add(time.Hour)
		run, err := service.triggerSchedule(ctx, schedule, &next)
		if err == nil {
			t.Fatal("persistence failure not returned")
		}
		if (run.ID == "") != failClaim {
			t.Fatalf("claim failure=%v run=%+v", failClaim, run)
		}
		engine.mu.Lock()
		createdSessions := len(engine.sessions)
		engine.mu.Unlock()
		if failClaim && createdSessions != 0 {
			t.Fatal("claim failure created a session")
		}
		if !failClaim {
			stored, err := repo.Get(ctx, schedule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !stored.NextRunAt.Equal(next) {
				t.Fatal("failed result write lost durable claim")
			}
			if repeated, err := service.triggerSchedule(ctx, schedule, &next); err == nil || repeated.ID != "" {
				t.Fatalf("replayed occurrence: %+v %v", repeated, err)
			}
		}
	}
}
