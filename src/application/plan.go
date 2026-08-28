package application

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
)

func (s *Service) ListPlans(ctx context.Context, sessionID string) ([]domain.Plan, error) {
	if _, _, err := s.resolveSession(ctx, sessionID); err != nil {
		return nil, err
	}
	return s.plans.List(ctx, sessionID)
}

// GetPlan 保留舊版單計畫 API 的相容行為，回傳有序列表中的第一份計畫。
func (s *Service) GetPlan(ctx context.Context, sessionID string) (*domain.Plan, error) {
	values, err := s.ListPlans(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	return compatibilityPlan(values), nil
}

func (s *Service) CreatePlan(ctx context.Context, sessionID string, input domain.CreatePlanInput) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return domain.Plan{}, err
	}
	input.CreatedBy = domain.PlanCreatedByUser
	value, err := domain.NewPlan(sessionID, input, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	return s.plans.Create(ctx, value)
}

func (s *Service) UpdatePlan(ctx context.Context, sessionID, planID string, input domain.CreatePlanInput) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return domain.Plan{}, err
	}
	existing, err := s.plans.Get(ctx, sessionID, planID)
	if err != nil {
		return domain.Plan{}, err
	}
	input.CreatedBy = domain.PlanCreatedByUser
	value, err := domain.NewPlan(sessionID, input, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	value.ID = existing.ID
	value.Position = existing.Position
	value.CreatedAt = existing.CreatedAt
	return s.plans.Update(ctx, value)
}

func (s *Service) DeletePlanByID(ctx context.Context, sessionID, planID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return err
	}
	return s.plans.Delete(ctx, sessionID, planID)
}

func (s *Service) ReorderPlans(ctx context.Context, sessionID string, input domain.ReorderPlansInput) ([]domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return nil, err
	}
	return s.plans.Reorder(ctx, sessionID, input.PlanIDs)
}

// PutPlan 保留舊版 PUT /plan：沒有計畫時建立，有計畫時重建第一份。
func (s *Service) PutPlan(ctx context.Context, sessionID string, input domain.CreatePlanInput) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return domain.Plan{}, err
	}
	values, err := s.plans.List(ctx, sessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	input.CreatedBy = domain.PlanCreatedByUser
	value, err := domain.NewPlan(sessionID, input, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	if len(values) == 0 {
		return s.plans.Create(ctx, value)
	}
	target := compatibilityPlan(values)
	value.ID = target.ID
	value.Position = target.Position
	value.CreatedAt = target.CreatedAt
	return s.plans.Update(ctx, value)
}

// DeletePlan 保留舊版 DELETE /plan：刪除列表中的第一份計畫。
func (s *Service) DeletePlan(ctx context.Context, sessionID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.preparePlanMutation(ctx, sessionID); err != nil {
		return err
	}
	values, err := s.plans.List(ctx, sessionID)
	if err != nil || len(values) == 0 {
		return err
	}
	return s.plans.Delete(ctx, sessionID, compatibilityPlan(values).ID)
}

func (s *Service) preparePlanMutation(ctx context.Context, sessionID string) error {
	if _, _, err := s.resolveSession(ctx, sessionID); err != nil {
		return err
	}
	if s.hasActiveSession(sessionID) {
		return fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	return nil
}

func compatibilityPlan(values []domain.Plan) *domain.Plan {
	for index := range values {
		if values[index].Status == domain.PlanStatusActive {
			return &values[index]
		}
	}
	return &values[0]
}
