package application

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// scheduleTickInterval 是排程執行器檢查到期排程的頻率。排程最小週期是 1 分鐘，
	// 20 秒一次的檢查足以維持可接受的誤差，又不會讓閒置的後端一直忙碌。
	scheduleTickInterval = 20 * time.Second
	// scheduleMissedGrace 是後端重新啟動後仍願意補跑的寬限時間。
	// 超過寬限的時間點一律跳過：關機一週再開機時，使用者要的是下一次執行，
	// 不是一次湧出的補償 Run。
	scheduleMissedGrace = 15 * time.Minute
)

func (s *Service) requireSchedules() error {
	if s.schedules == nil {
		return fmt.Errorf("%w: schedules are unavailable", domain.ErrConflict)
	}
	return nil
}

func (s *Service) ListSchedules(ctx context.Context) ([]domain.Schedule, error) {
	if err := s.requireSchedules(); err != nil {
		return nil, err
	}
	return s.schedules.List(ctx)
}

func (s *Service) GetSchedule(ctx context.Context, scheduleID string) (domain.Schedule, error) {
	if err := s.requireSchedules(); err != nil {
		return domain.Schedule{}, err
	}
	return s.schedules.Get(ctx, strings.TrimSpace(scheduleID))
}

func (s *Service) CreateSchedule(ctx context.Context, input domain.CreateScheduleInput) (domain.Schedule, error) {
	if err := s.requireSchedules(); err != nil {
		return domain.Schedule{}, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if _, err := s.workspaces.Get(ctx, input.WorkspaceID); err != nil {
		return domain.Schedule{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		agentID = s.defaultAgentID()
	}
	if _, err := s.registry.Get(agentID); err != nil {
		return domain.Schedule{}, err
	}
	input.AgentID = agentID
	if providerID := strings.TrimSpace(input.ProviderID); providerID != "" {
		if err := s.validateSessionProvider(providerID); err != nil {
			return domain.Schedule{}, err
		}
	}
	profile, err := s.permissions.Resolve(input.PermissionProfile)
	if err != nil {
		return domain.Schedule{}, err
	}
	input.PermissionProfile = profile
	return s.schedules.Create(ctx, input)
}

func (s *Service) UpdateSchedule(ctx context.Context, scheduleID string, input domain.UpdateScheduleInput) (domain.Schedule, error) {
	if err := s.requireSchedules(); err != nil {
		return domain.Schedule{}, err
	}
	if input.ProviderID != nil {
		if providerID := strings.TrimSpace(*input.ProviderID); providerID != "" {
			if err := s.validateSessionProvider(providerID); err != nil {
				return domain.Schedule{}, err
			}
		}
	}
	if input.PermissionProfile != nil {
		profile, err := s.permissions.Resolve(*input.PermissionProfile)
		if err != nil {
			return domain.Schedule{}, err
		}
		input.PermissionProfile = &profile
	}
	return s.schedules.Update(ctx, strings.TrimSpace(scheduleID), input)
}

func (s *Service) DeleteSchedule(ctx context.Context, scheduleID string) error {
	if err := s.requireSchedules(); err != nil {
		return err
	}
	return s.schedules.Delete(ctx, strings.TrimSpace(scheduleID))
}

// RunSchedule 立刻執行一次排程，不影響原本的下一次執行時間。
// 使用者在建立排程後通常會想先確認任務描述是否可用，這是最短的驗證路徑。
func (s *Service) RunSchedule(ctx context.Context, scheduleID string) (domain.Run, error) {
	if err := s.requireSchedules(); err != nil {
		return domain.Run{}, err
	}
	schedule, err := s.schedules.Get(ctx, strings.TrimSpace(scheduleID))
	if err != nil {
		return domain.Run{}, err
	}
	return s.triggerSchedule(ctx, schedule, nil)
}

// triggerSchedule 每次都建立新的 Session，再把排程的 Prompt 當成使用者訊息送出。
//
// 排程刻意不綁定既有 Session：定期任務不應該繼承上一次的對話與上下文，也不該讓
// 使用者為了排程去維護一個長對話。nextRunAt 為 nil 代表手動執行，不改寫時間軸。
func (s *Service) triggerSchedule(ctx context.Context, schedule domain.Schedule, nextRunAt *time.Time) (domain.Run, error) {
	startedAt := s.now().UTC()
	run, err := s.startScheduleRun(ctx, schedule)
	state := domain.ScheduleRunState{
		LastRunAt:  startedAt,
		NextRunAt:  nextRunAt,
		LastStatus: domain.ScheduleStatusTriggered,
	}
	if nextRunAt == nil {
		// 手動執行不移動時間軸；沿用排程目前的下一次時間。
		state.NextRunAt = schedule.NextRunAt
	}
	if err != nil {
		state.LastStatus = domain.ScheduleStatusFailed
		state.LastError = err.Error()
	} else {
		state.LastRunID = run.ID
		state.LastSessionID = run.SessionID
	}
	if _, markErr := s.schedules.MarkTriggered(ctx, schedule.ID, state); markErr != nil {
		s.logger.Error("record schedule trigger failed", "schedule_id", schedule.ID, "error", markErr)
	}
	return run, err
}

func (s *Service) startScheduleRun(ctx context.Context, schedule domain.Schedule) (domain.Run, error) {
	agentID := strings.TrimSpace(schedule.AgentID)
	if agentID == "" {
		agentID = s.defaultAgentID()
	}
	session, err := s.CreateSession(ctx, agentID, domain.CreateSessionInput{
		Title:             scheduleSessionTitle(schedule, s.now()),
		WorkspaceID:       schedule.WorkspaceID,
		ProviderID:        schedule.ProviderID,
		Model:             schedule.Model,
		PermissionProfile: schedule.PermissionProfile,
		Metadata: map[string]any{
			"schedule_id":   schedule.ID,
			"schedule_name": schedule.Name,
		},
	})
	if err != nil {
		return domain.Run{}, err
	}
	// SandboxRoots 走 RunInput 的後端專用欄位：排程的目錄在寫入 Repository 時已經
	// 驗證過，HTTP 呼叫端無法自行帶入這個欄位。
	return s.StartRun(ctx, domain.RunInput{
		SessionID:    session.ID,
		UserInput:    schedule.Prompt,
		ProviderID:   schedule.ProviderID,
		Model:        schedule.Model,
		SandboxRoots: schedule.SandboxRoots,
		Metadata: map[string]any{
			"schedule_id":   schedule.ID,
			"schedule_name": schedule.Name,
		},
	})
}

// startScheduleRunner 啟動唯一的排程檢查迴圈，隨 Service 的 rootCtx 一起結束。
func (s *Service) startScheduleRunner() {
	if s.schedules == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.rebaselineSchedules(s.rootCtx)
		ticker := time.NewTicker(scheduleTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.rootCtx.Done():
				return
			case <-ticker.C:
				s.runDueSchedules(s.rootCtx)
			}
		}
	}()
}

// rebaselineSchedules 在後端啟動時把停機期間錯過的時間點往後推。
// 只有落在寬限時間內的排程會保留原本的時間點，讓短暫重啟仍然跑得到。
func (s *Service) rebaselineSchedules(ctx context.Context) {
	values, err := s.schedules.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("load schedules failed", "error", err)
		}
		return
	}
	now := s.now().UTC()
	for _, schedule := range values {
		if !schedule.Enabled {
			continue
		}
		if schedule.NextRunAt != nil && !schedule.NextRunAt.Before(now.Add(-scheduleMissedGrace)) {
			continue
		}
		next, nextErr := schedule.Recurrence.Next(now)
		if nextErr != nil {
			s.logger.Error("resolve schedule next run failed", "schedule_id", schedule.ID, "error", nextErr)
			continue
		}
		if _, rescheduleErr := s.schedules.Reschedule(ctx, schedule.ID, &next); rescheduleErr != nil {
			s.logger.Error("reschedule failed", "schedule_id", schedule.ID, "error", rescheduleErr)
		}
	}
}

func (s *Service) runDueSchedules(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	values, err := s.schedules.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("load schedules failed", "error", err)
		}
		return
	}
	now := s.now().UTC()
	for _, schedule := range values {
		if ctx.Err() != nil {
			return
		}
		if !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		next, nextErr := schedule.Recurrence.Next(now)
		if nextErr != nil {
			s.logger.Error("resolve schedule next run failed", "schedule_id", schedule.ID, "error", nextErr)
			continue
		}
		// 先算好下一次時間再觸發：即使這次啟動失敗，時間軸仍會往前走，
		// 不會每 20 秒重試同一個到期時間點。
		if _, err := s.triggerSchedule(ctx, schedule, &next); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.logger.Error("start scheduled run failed", "schedule_id", schedule.ID, "error", err)
			continue
		}
		s.logger.Info("scheduled run started", "schedule_id", schedule.ID, "schedule_name", schedule.Name, "next_run_at", next)
	}
}

func (s *Service) defaultAgentID() string {
	descriptors := s.registry.List()
	if len(descriptors) == 0 {
		return ""
	}
	return descriptors[0].ID
}

func scheduleSessionTitle(schedule domain.Schedule, at time.Time) string {
	name := strings.TrimSpace(schedule.Name)
	if name == "" {
		name = "排程"
	}
	return fmt.Sprintf("%s · %s", name, at.Local().Format("01/02 15:04"))
}
