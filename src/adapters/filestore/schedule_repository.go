package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var _ ports.ScheduleRepository = (*ScheduleRepository)(nil)

const scheduleMaxPromptCharacters = 8000

type ScheduleRepository struct {
	mu       sync.RWMutex
	filePath string
	items    map[string]domain.Schedule
}

type scheduleFile struct {
	Version int                        `json:"version"`
	Items   map[string]domain.Schedule `json:"items"`
}

func NewScheduleRepository(dataDir string) (*ScheduleRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "schedules")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create schedule store: %w", err)
	}
	repository := &ScheduleRepository{
		filePath: filepath.Join(root, "schedules.json"),
		items:    map[string]domain.Schedule{},
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (r *ScheduleRepository) Create(ctx context.Context, input domain.CreateScheduleInput) (domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return domain.Schedule{}, err
	}
	name := strings.TrimSpace(input.Name)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	prompt, err := normalizeSchedulePrompt(input.Prompt)
	if err != nil {
		return domain.Schedule{}, err
	}
	if name == "" || workspaceID == "" {
		return domain.Schedule{}, fmt.Errorf("%w: schedule name and workspace id are required", domain.ErrInvalidInput)
	}
	recurrence, err := input.Recurrence.Normalize()
	if err != nil {
		return domain.Schedule{}, err
	}
	// 排程沙箱與 Project 沙箱套用同一組檢查：必須是絕對路徑、解析 symlink 後存在、
	// 是目錄且不是檔案系統根目錄。
	sandboxRoots, err := normalizeProjectSandboxRoots(input.SandboxRoots)
	if err != nil {
		return domain.Schedule{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameExistsLocked(workspaceID, name, "") {
		return domain.Schedule{}, fmt.Errorf("%w: schedule name %q already exists", domain.ErrConflict, name)
	}
	position := 100
	for _, item := range r.items {
		if item.WorkspaceID == workspaceID && item.Position >= position {
			position = item.Position + 100
		}
	}
	now := time.Now().UTC()
	value := domain.Schedule{
		ID:                domain.NewID("schedule"),
		WorkspaceID:       workspaceID,
		AgentID:           strings.TrimSpace(input.AgentID),
		Name:              name,
		Prompt:            prompt,
		Enabled:           enabled,
		Recurrence:        recurrence,
		SandboxRoots:      sandboxRoots,
		ProviderID:        strings.TrimSpace(input.ProviderID),
		Model:             strings.TrimSpace(input.Model),
		PermissionProfile: strings.TrimSpace(input.PermissionProfile),
		Position:          position,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if enabled {
		next, nextErr := recurrence.Next(now)
		if nextErr != nil {
			return domain.Schedule{}, nextErr
		}
		value.NextRunAt = &next
	}
	previous := cloneSchedules(r.items)
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Schedule{}, err
	}
	return cloneSchedule(value), nil
}

func (r *ScheduleRepository) List(ctx context.Context) ([]domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	values := make([]domain.Schedule, 0, len(r.items))
	for _, value := range r.items {
		values = append(values, cloneSchedule(value))
	}
	r.mu.RUnlock()
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Position == values[j].Position {
			return strings.ToLower(values[i].Name) < strings.ToLower(values[j].Name)
		}
		return values[i].Position < values[j].Position
	})
	return values, nil
}

func (r *ScheduleRepository) Get(ctx context.Context, scheduleID string) (domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return domain.Schedule{}, err
	}
	r.mu.RLock()
	value, exists := r.items[strings.TrimSpace(scheduleID)]
	r.mu.RUnlock()
	if !exists {
		return domain.Schedule{}, fmt.Errorf("%w: schedule %q", domain.ErrNotFound, scheduleID)
	}
	return cloneSchedule(value), nil
}

func (r *ScheduleRepository) Update(ctx context.Context, scheduleID string, input domain.UpdateScheduleInput) (domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return domain.Schedule{}, err
	}
	if input.Name == nil && input.Prompt == nil && input.Enabled == nil && input.Recurrence == nil &&
		input.SandboxRoots == nil && input.ProviderID == nil && input.Model == nil &&
		input.PermissionProfile == nil && input.Position == nil {
		return domain.Schedule{}, fmt.Errorf("%w: at least one schedule field is required", domain.ErrInvalidInput)
	}
	scheduleID = strings.TrimSpace(scheduleID)
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.items[scheduleID]
	if !exists {
		return domain.Schedule{}, fmt.Errorf("%w: schedule %q", domain.ErrNotFound, scheduleID)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return domain.Schedule{}, fmt.Errorf("%w: schedule name cannot be empty", domain.ErrInvalidInput)
		}
		if r.nameExistsLocked(value.WorkspaceID, name, scheduleID) {
			return domain.Schedule{}, fmt.Errorf("%w: schedule name %q already exists", domain.ErrConflict, name)
		}
		value.Name = name
	}
	if input.Prompt != nil {
		prompt, err := normalizeSchedulePrompt(*input.Prompt)
		if err != nil {
			return domain.Schedule{}, err
		}
		value.Prompt = prompt
	}
	if input.Recurrence != nil {
		recurrence, err := input.Recurrence.Normalize()
		if err != nil {
			return domain.Schedule{}, err
		}
		value.Recurrence = recurrence
	}
	if input.SandboxRoots != nil {
		sandboxRoots, err := normalizeProjectSandboxRoots(*input.SandboxRoots)
		if err != nil {
			return domain.Schedule{}, err
		}
		value.SandboxRoots = sandboxRoots
	}
	if input.ProviderID != nil {
		value.ProviderID = strings.TrimSpace(*input.ProviderID)
	}
	if input.Model != nil {
		value.Model = strings.TrimSpace(*input.Model)
	}
	if input.PermissionProfile != nil {
		value.PermissionProfile = strings.TrimSpace(*input.PermissionProfile)
	}
	if input.Position != nil {
		if *input.Position < 0 {
			return domain.Schedule{}, fmt.Errorf("%w: schedule position cannot be negative", domain.ErrInvalidInput)
		}
		value.Position = *input.Position
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	now := time.Now().UTC()
	// 週期或啟用狀態變動後，舊的 next_run_at 已經沒有意義，必須立刻重新計算，
	// 否則停用再啟用的排程會沿用一個過期時間點而立刻觸發。
	if input.Recurrence != nil || input.Enabled != nil {
		if value.Enabled {
			next, err := value.Recurrence.Next(now)
			if err != nil {
				return domain.Schedule{}, err
			}
			value.NextRunAt = &next
		} else {
			value.NextRunAt = nil
		}
	}
	value.UpdatedAt = now
	previous := cloneSchedules(r.items)
	r.items[scheduleID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Schedule{}, err
	}
	return cloneSchedule(value), nil
}

func (r *ScheduleRepository) Delete(ctx context.Context, scheduleID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scheduleID = strings.TrimSpace(scheduleID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[scheduleID]; !exists {
		return fmt.Errorf("%w: schedule %q", domain.ErrNotFound, scheduleID)
	}
	previous := cloneSchedules(r.items)
	delete(r.items, scheduleID)
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return err
	}
	return nil
}

func (r *ScheduleRepository) MarkTriggered(ctx context.Context, scheduleID string, state domain.ScheduleRunState) (domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return domain.Schedule{}, err
	}
	scheduleID = strings.TrimSpace(scheduleID)
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.items[scheduleID]
	if !exists {
		return domain.Schedule{}, fmt.Errorf("%w: schedule %q", domain.ErrNotFound, scheduleID)
	}
	lastRunAt := state.LastRunAt.UTC()
	value.LastRunAt = &lastRunAt
	value.LastRunID = strings.TrimSpace(state.LastRunID)
	value.LastSessionID = strings.TrimSpace(state.LastSessionID)
	value.LastStatus = strings.TrimSpace(state.LastStatus)
	value.LastError = strings.TrimSpace(state.LastError)
	if state.NextRunAt != nil {
		next := state.NextRunAt.UTC()
		value.NextRunAt = &next
	} else {
		value.NextRunAt = nil
	}
	value.UpdatedAt = time.Now().UTC()
	previous := cloneSchedules(r.items)
	r.items[scheduleID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Schedule{}, err
	}
	return cloneSchedule(value), nil
}

func (r *ScheduleRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read schedule store: %w", err)
	}
	var snapshot scheduleFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode schedule store: %w", err)
	}
	if snapshot.Items == nil {
		return nil
	}
	// 儲存的沙箱目錄可能在關機期間被移走；這種排程只停用而不讓整個後端起不來。
	for id, value := range snapshot.Items {
		sandboxRoots, normalizeErr := normalizeProjectSandboxRoots(value.SandboxRoots)
		if normalizeErr != nil {
			value.SandboxRoots = nil
			value.Enabled = false
			value.NextRunAt = nil
			value.LastStatus = domain.ScheduleStatusFailed
			value.LastError = normalizeErr.Error()
		} else {
			value.SandboxRoots = sandboxRoots
		}
		snapshot.Items[id] = value
	}
	r.items = snapshot.Items
	return nil
}

func (r *ScheduleRepository) persistLocked() error {
	data, err := json.MarshalIndent(scheduleFile{Version: 1, Items: r.items}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schedule store: %w", err)
	}
	temporary := r.filePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write schedule store: %w", err)
	}
	if err := replaceFile(temporary, r.filePath); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace schedule store: %w", err)
	}
	return nil
}

func (r *ScheduleRepository) nameExistsLocked(workspaceID, name, excludedID string) bool {
	for id, value := range r.items {
		if id != excludedID && value.WorkspaceID == workspaceID && strings.EqualFold(strings.TrimSpace(value.Name), name) {
			return true
		}
	}
	return false
}

func normalizeSchedulePrompt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: schedule prompt is required", domain.ErrInvalidInput)
	}
	if len([]rune(value)) > scheduleMaxPromptCharacters {
		return "", fmt.Errorf("%w: schedule prompt cannot exceed %d characters", domain.ErrInvalidInput, scheduleMaxPromptCharacters)
	}
	return value, nil
}

func cloneSchedules(values map[string]domain.Schedule) map[string]domain.Schedule {
	cloned := make(map[string]domain.Schedule, len(values))
	for key, value := range values {
		cloned[key] = cloneSchedule(value)
	}
	return cloned
}

func cloneSchedule(value domain.Schedule) domain.Schedule {
	value.SandboxRoots = append([]string(nil), value.SandboxRoots...)
	value.Recurrence.Weekdays = append([]int(nil), value.Recurrence.Weekdays...)
	if value.NextRunAt != nil {
		next := *value.NextRunAt
		value.NextRunAt = &next
	}
	if value.LastRunAt != nil {
		last := *value.LastRunAt
		value.LastRunAt = &last
	}
	return value
}

func (r *ScheduleRepository) Reschedule(ctx context.Context, scheduleID string, nextRunAt *time.Time) (domain.Schedule, error) {
	if err := ctx.Err(); err != nil {
		return domain.Schedule{}, err
	}
	scheduleID = strings.TrimSpace(scheduleID)
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.items[scheduleID]
	if !exists {
		return domain.Schedule{}, fmt.Errorf("%w: schedule %q", domain.ErrNotFound, scheduleID)
	}
	if nextRunAt != nil {
		next := nextRunAt.UTC()
		value.NextRunAt = &next
	} else {
		value.NextRunAt = nil
	}
	previous := cloneSchedules(r.items)
	r.items[scheduleID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Schedule{}, err
	}
	return cloneSchedule(value), nil
}
