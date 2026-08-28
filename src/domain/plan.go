package domain

import (
	"fmt"
	"strings"
	"time"
)

type PlanStatus string

const (
	PlanStatusQueued    PlanStatus = "queued"
	PlanStatusActive    PlanStatus = "active"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusCanceled  PlanStatus = "canceled"
)

type PlanStepStatus string

const (
	PlanStepStatusPending    PlanStepStatus = "pending"
	PlanStepStatusInProgress PlanStepStatus = "in_progress"
	PlanStepStatusVerifying  PlanStepStatus = "verifying"
	PlanStepStatusCompleted  PlanStepStatus = "completed"
	PlanStepStatusBlocked    PlanStepStatus = "blocked"
	PlanStepStatusSkipped    PlanStepStatus = "skipped"
)

const (
	PlanCreatedByUser  = "user"
	PlanCreatedByAgent = "agent"
	MaxPlanSteps       = 50
)

// Plan 是 Session 內可持久化的工作契約。它不混入聊天訊息，讓 UI、Agent
// 與稽核流程都能讀取同一份結構化狀態。
type Plan struct {
	ID            string     `json:"id"`
	SessionID     string     `json:"session_id"`
	Title         string     `json:"title"`
	Objective     string     `json:"objective,omitempty"`
	Status        PlanStatus `json:"status"`
	CurrentStepID string     `json:"current_step_id,omitempty"`
	CreatedBy     string     `json:"created_by"`
	Steps         []PlanStep `json:"steps"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Position      int        `json:"position"`
}

type PlanStep struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	Verification string         `json:"verification"`
	Status       PlanStepStatus `json:"status"`
	Evidence     string         `json:"evidence,omitempty"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

type CreatePlanInput struct {
	Title     string                `json:"title"`
	Objective string                `json:"objective,omitempty"`
	CreatedBy string                `json:"created_by,omitempty"`
	Steps     []CreatePlanStepInput `json:"steps"`
}

type CreatePlanStepInput struct {
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Verification string `json:"verification"`
}

type UpdatePlanStepInput struct {
	Status   PlanStepStatus `json:"status"`
	Evidence string         `json:"evidence,omitempty"`
}

type ReorderPlansInput struct {
	PlanIDs []string `json:"plan_ids"`
}

func NewPlan(sessionID string, input CreatePlanInput, now time.Time) (Plan, error) {
	sessionID = strings.TrimSpace(sessionID)
	input.Title = strings.TrimSpace(input.Title)
	input.Objective = strings.TrimSpace(input.Objective)
	input.CreatedBy = strings.ToLower(strings.TrimSpace(input.CreatedBy))
	if sessionID == "" || input.Title == "" {
		return Plan{}, fmt.Errorf("%w: session_id and plan title are required", ErrInvalidInput)
	}
	if len(input.Steps) == 0 || len(input.Steps) > MaxPlanSteps {
		return Plan{}, fmt.Errorf("%w: plan must contain between 1 and %d steps", ErrInvalidInput, MaxPlanSteps)
	}
	if input.CreatedBy == "" {
		input.CreatedBy = PlanCreatedByUser
	}
	if input.CreatedBy != PlanCreatedByUser && input.CreatedBy != PlanCreatedByAgent {
		return Plan{}, fmt.Errorf("%w: unsupported plan creator %q", ErrInvalidInput, input.CreatedBy)
	}
	now = now.UTC()
	plan := Plan{
		ID:        NewID("plan"),
		SessionID: sessionID,
		Title:     input.Title,
		Objective: input.Objective,
		Status:    PlanStatusActive,
		CreatedBy: input.CreatedBy,
		Steps:     make([]PlanStep, 0, len(input.Steps)),
		CreatedAt: now,
		UpdatedAt: now,
	}
	for index, inputStep := range input.Steps {
		title := strings.TrimSpace(inputStep.Title)
		verification := strings.TrimSpace(inputStep.Verification)
		if title == "" || verification == "" {
			return Plan{}, fmt.Errorf("%w: step %d requires title and verification criteria", ErrInvalidInput, index+1)
		}
		plan.Steps = append(plan.Steps, PlanStep{
			ID:           NewID("step"),
			Title:        title,
			Description:  strings.TrimSpace(inputStep.Description),
			Verification: verification,
			Status:       PlanStepStatusPending,
		})
	}
	plan.CurrentStepID = plan.Steps[0].ID
	return plan, nil
}

// TransitionPlanStep 強制逐步生命週期。完成步驟前一定要先進入 verifying，
// 並附上由實際檢查得到的 evidence；後續步驟在前一步結束前不能開始。
func TransitionPlanStep(plan Plan, stepID string, input UpdatePlanStepInput, now time.Time) (Plan, error) {
	if err := ValidatePlan(plan); err != nil {
		return Plan{}, err
	}
	if plan.Status != PlanStatusActive {
		return Plan{}, fmt.Errorf("%w: plan is %s", ErrConflict, plan.Status)
	}
	stepID = strings.TrimSpace(stepID)
	input.Evidence = strings.TrimSpace(input.Evidence)
	index := -1
	for stepIndex := range plan.Steps {
		if plan.Steps[stepIndex].ID == stepID {
			index = stepIndex
			break
		}
	}
	if index < 0 {
		return Plan{}, fmt.Errorf("%w: plan step %q", ErrNotFound, stepID)
	}
	for previousIndex := 0; previousIndex < index; previousIndex++ {
		status := plan.Steps[previousIndex].Status
		if status != PlanStepStatusCompleted && status != PlanStepStatusSkipped {
			return Plan{}, fmt.Errorf("%w: step %d must finish before step %d can change", ErrConflict, previousIndex+1, index+1)
		}
	}
	step := plan.Steps[index]
	now = now.UTC()
	switch input.Status {
	case PlanStepStatusInProgress:
		if step.Status != PlanStepStatusPending && step.Status != PlanStepStatusBlocked {
			return Plan{}, invalidPlanTransition(step.Status, input.Status)
		}
		if step.StartedAt == nil {
			step.StartedAt = timePointer(now)
		}
		step.Evidence = ""
		step.CompletedAt = nil
	case PlanStepStatusVerifying:
		if step.Status != PlanStepStatusInProgress {
			return Plan{}, invalidPlanTransition(step.Status, input.Status)
		}
	case PlanStepStatusCompleted:
		if step.Status != PlanStepStatusVerifying {
			return Plan{}, invalidPlanTransition(step.Status, input.Status)
		}
		if input.Evidence == "" {
			return Plan{}, fmt.Errorf("%w: completing a plan step requires verification evidence", ErrInvalidInput)
		}
		step.Evidence = input.Evidence
		step.CompletedAt = timePointer(now)
	case PlanStepStatusBlocked:
		if step.Status != PlanStepStatusInProgress && step.Status != PlanStepStatusVerifying {
			return Plan{}, invalidPlanTransition(step.Status, input.Status)
		}
		if input.Evidence == "" {
			return Plan{}, fmt.Errorf("%w: blocking a plan step requires the blocker as evidence", ErrInvalidInput)
		}
		step.Evidence = input.Evidence
	case PlanStepStatusSkipped:
		if step.Status != PlanStepStatusPending {
			return Plan{}, invalidPlanTransition(step.Status, input.Status)
		}
		if input.Evidence == "" {
			return Plan{}, fmt.Errorf("%w: skipping a plan step requires a reason", ErrInvalidInput)
		}
		step.Evidence = input.Evidence
		step.CompletedAt = timePointer(now)
	default:
		return Plan{}, fmt.Errorf("%w: unsupported plan step status %q", ErrInvalidInput, input.Status)
	}
	step.Status = input.Status
	plan.Steps[index] = step
	plan.UpdatedAt = now
	plan.CurrentStepID = ""
	allFinished := true
	for _, candidate := range plan.Steps {
		if candidate.Status != PlanStepStatusCompleted && candidate.Status != PlanStepStatusSkipped {
			allFinished = false
			if plan.CurrentStepID == "" {
				plan.CurrentStepID = candidate.ID
			}
		}
	}
	if allFinished {
		plan.Status = PlanStatusCompleted
	}
	return plan, nil
}

func ValidatePlan(plan Plan) error {
	if strings.TrimSpace(plan.ID) == "" || strings.TrimSpace(plan.SessionID) == "" || strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("%w: plan id, session id and title are required", ErrInvalidInput)
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > MaxPlanSteps {
		return fmt.Errorf("%w: plan must contain between 1 and %d steps", ErrInvalidInput, MaxPlanSteps)
	}
	switch plan.Status {
	case PlanStatusQueued, PlanStatusActive, PlanStatusCompleted, PlanStatusCanceled:
	default:
		return fmt.Errorf("%w: unsupported plan status %q", ErrInvalidInput, plan.Status)
	}
	activeSteps := 0
	ids := map[string]struct{}{}
	for index, step := range plan.Steps {
		if strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.Title) == "" || strings.TrimSpace(step.Verification) == "" {
			return fmt.Errorf("%w: plan step %d is incomplete", ErrInvalidInput, index+1)
		}
		if _, exists := ids[step.ID]; exists {
			return fmt.Errorf("%w: duplicate plan step id %q", ErrInvalidInput, step.ID)
		}
		ids[step.ID] = struct{}{}
		if step.Status == PlanStepStatusInProgress || step.Status == PlanStepStatusVerifying || step.Status == PlanStepStatusBlocked {
			activeSteps++
		}
		if step.Status == PlanStepStatusCompleted && strings.TrimSpace(step.Evidence) == "" {
			return fmt.Errorf("%w: completed plan step %d has no verification evidence", ErrInvalidInput, index+1)
		}
	}
	if activeSteps > 1 {
		return fmt.Errorf("%w: only one plan step can be active", ErrConflict)
	}
	return nil
}

func PlanHasProgress(plan Plan) bool {
	for _, step := range plan.Steps {
		if step.Status != PlanStepStatusPending {
			return true
		}
	}
	return false
}

func PlanIsTerminal(plan Plan) bool {
	return plan.Status == PlanStatusCompleted || plan.Status == PlanStatusCanceled
}

func invalidPlanTransition(from, to PlanStepStatus) error {
	return fmt.Errorf("%w: plan step cannot transition from %s to %s", ErrConflict, from, to)
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
