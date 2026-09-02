package filestore

import (
	"AgenticService/src/domain"
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

type RunRepository struct {
	mu       sync.RWMutex
	filePath string
	runs     map[string]domain.Run
}

type runFile struct {
	Version int                   `json:"version"`
	Runs    map[string]domain.Run `json:"runs"`
}

func NewRunRepository(dataDir string) (*RunRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "runs")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create run store: %w", err)
	}
	repository := &RunRepository{
		filePath: filepath.Join(root, "runs.json"),
		runs:     map[string]domain.Run{},
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	if repository.markInterruptedRuns() {
		if err := repository.persistLocked(); err != nil {
			return nil, err
		}
	}
	return repository, nil
}

func (r *RunRepository) Save(_ context.Context, run domain.Run) error {
	if strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("%w: run id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, existed := r.runs[run.ID]
	r.runs[run.ID] = cloneRun(run)
	if err := r.persistLocked(); err != nil {
		if existed {
			r.runs[run.ID] = previous
		} else {
			delete(r.runs, run.ID)
		}
		return err
	}
	return nil
}

func (r *RunRepository) Get(_ context.Context, id string) (domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[strings.TrimSpace(id)]
	if !ok {
		return domain.Run{}, fmt.Errorf("%w: run %q", domain.ErrNotFound, id)
	}
	return cloneRun(run), nil
}

func (r *RunRepository) List(_ context.Context, sessionID string) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		if sessionID != "" && run.SessionID != sessionID {
			continue
		}
		items = append(items, cloneRun(run))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (r *RunRepository) FindByIdempotencyKey(_ context.Context, sessionID string, key string) (domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, run := range r.runs {
		if run.SessionID == sessionID && run.IdempotencyKey == key {
			return cloneRun(run), nil
		}
	}
	return domain.Run{}, fmt.Errorf("%w: idempotency key", domain.ErrNotFound)
}

func (r *RunRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read run store: %w", err)
	}
	var snapshot runFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode run store: %w", err)
	}
	if snapshot.Runs != nil {
		r.runs = snapshot.Runs
	}
	return nil
}

func (r *RunRepository) persistLocked() error {
	data, err := json.MarshalIndent(runFile{Version: 1, Runs: r.runs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run store: %w", err)
	}
	temporary := r.filePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return fmt.Errorf("write run store: %w", err)
	}
	if err := replaceFile(temporary, r.filePath); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace run store: %w", err)
	}
	return nil
}

func (r *RunRepository) markInterruptedRuns() bool {
	changed := false
	for id, run := range r.runs {
		if run.Status != domain.RunStatusQueued && run.Status != domain.RunStatusRunning && run.Status != domain.RunStatusPaused && run.Status != domain.RunStatusWaitingApproval {
			continue
		}
		now := time.Now().UTC()
		run.Status = domain.RunStatusFailed
		run.Error = &domain.RunError{Code: "server_restarted", Message: "run interrupted by server restart", Retryable: true}
		run.CompletedAt = &now
		r.runs[id] = run
		changed = true
	}
	return changed
}

func cloneRun(run domain.Run) domain.Run {
	copyRun := run
	if run.Metadata != nil {
		copyRun.Metadata = make(map[string]any, len(run.Metadata))
		for key, value := range run.Metadata {
			copyRun.Metadata[key] = value
		}
	}
	if run.Result != nil {
		result := *run.Result
		if run.Result.Usage != nil {
			usage := *run.Result.Usage
			result.Usage = &usage
		}
		copyRun.Result = &result
	}
	if run.Usage != nil {
		usage := *run.Usage
		copyRun.Usage = &usage
	}
	if run.Error != nil {
		runError := *run.Error
		copyRun.Error = &runError
	}
	if run.PendingApproval != nil {
		approval := *run.PendingApproval
		if run.PendingApproval.Arguments != nil {
			approval.Arguments = make(map[string]any, len(run.PendingApproval.Arguments))
			for key, value := range run.PendingApproval.Arguments {
				approval.Arguments[key] = value
			}
		}
		copyRun.PendingApproval = &approval
	}
	return copyRun
}
