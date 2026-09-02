package application

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"strings"
)

func (s *Service) ListPlans(ctx context.Context, sessionID string) ([]domain.Plan, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.plans.Reconcile(ctx, session.ID, session.LockPlans)
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
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	input.CreatedBy = domain.PlanCreatedByUser
	value, err := domain.NewPlan(sessionID, input, s.now())
	if err != nil {
		return domain.Plan{}, err
	}
	if value, err = s.plans.Create(ctx, value); err != nil {
		return domain.Plan{}, err
	}
	return s.reconciledPlan(ctx, session, value.ID)
}

func (s *Service) UpdatePlan(ctx context.Context, sessionID, planID string, input domain.CreatePlanInput) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
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
	if value, err = s.plans.Update(ctx, value); err != nil {
		return domain.Plan{}, err
	}
	return s.reconciledPlan(ctx, session, value.ID)
}

func (s *Service) DeletePlanByID(ctx context.Context, sessionID, planID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.plans.Delete(ctx, sessionID, planID); err != nil {
		return err
	}
	_, err = s.plans.Reconcile(ctx, session.ID, session.LockPlans)
	return err
}

func (s *Service) ReorderPlans(ctx context.Context, sessionID string, input domain.ReorderPlansInput) ([]domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	_, err = s.plans.ReorderWithPolicy(ctx, sessionID, input.PlanIDs, session.LockPlans)
	if err != nil {
		return nil, err
	}
	return s.plans.Reconcile(ctx, session.ID, session.LockPlans)
}

// PutPlan 保留舊版 PUT /plan：沒有計畫時建立，有計畫時重建第一份。
func (s *Service) PutPlan(ctx context.Context, sessionID string, input domain.CreatePlanInput) (domain.Plan, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
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
		if value, err = s.plans.Create(ctx, value); err != nil {
			return domain.Plan{}, err
		}
		return s.reconciledPlan(ctx, session, value.ID)
	}
	target := compatibilityPlan(values)
	value.ID = target.ID
	value.Position = target.Position
	value.CreatedAt = target.CreatedAt
	if value, err = s.plans.Update(ctx, value); err != nil {
		return domain.Plan{}, err
	}
	return s.reconciledPlan(ctx, session, value.ID)
}

// DeletePlan 保留舊版 DELETE /plan：刪除列表中的第一份計畫。
func (s *Service) DeletePlan(ctx context.Context, sessionID string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	session, err := s.preparePlanMutation(ctx, sessionID)
	if err != nil {
		return err
	}
	values, err := s.plans.List(ctx, sessionID)
	if err != nil || len(values) == 0 {
		return err
	}
	if err := s.plans.Delete(ctx, sessionID, compatibilityPlan(values).ID); err != nil {
		return err
	}
	_, err = s.plans.Reconcile(ctx, session.ID, session.LockPlans)
	return err
}

func (s *Service) preparePlanMutation(ctx context.Context, sessionID string) (domain.Session, error) {
	_, session, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if s.hasActiveSession(sessionID) {
		return domain.Session{}, fmt.Errorf("%w: session has a queued or running run", domain.ErrConflict)
	}
	if _, err := s.plans.Reconcile(ctx, session.ID, session.LockPlans); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Service) reconciledPlan(ctx context.Context, session domain.Session, planID string) (domain.Plan, error) {
	values, err := s.plans.Reconcile(ctx, session.ID, session.LockPlans)
	if err != nil {
		return domain.Plan{}, err
	}
	for _, value := range values {
		if value.ID == strings.TrimSpace(planID) {
			return value, nil
		}
	}
	return domain.Plan{}, fmt.Errorf("%w: plan %q", domain.ErrNotFound, planID)
}

func compatibilityPlan(values []domain.Plan) *domain.Plan {
	for index := range values {
		if values[index].Status == domain.PlanStatusActive {
			return &values[index]
		}
	}
	return &values[0]
}
