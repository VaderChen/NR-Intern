package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var _ ports.RunEventRepository = (*RunEventRepository)(nil)

type RunEventRepository struct {
	root string

	mu        sync.Mutex
	locks     map[string]*sync.RWMutex
	sequences map[string]int64
}

func NewRunEventRepository(dataDir string) (*RunEventRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "runs", "events")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create run event store: %w", err)
	}
	return &RunEventRepository{
		root:      root,
		locks:     map[string]*sync.RWMutex{},
		sequences: map[string]int64{},
	}, nil
}

func (r *RunEventRepository) Append(ctx context.Context, event domain.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := r.eventPath(event.RunID)
	if err != nil {
		return err
	}
	if event.Sequence <= 0 || strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("%w: event id, type and positive sequence are required", domain.ErrInvalidInput)
	}
	lock := r.runLock(event.RunID)
	lock.Lock()
	defer lock.Unlock()
	last, err := r.lastSequenceLocked(event.RunID, path)
	if err != nil {
		return err
	}
	if event.Sequence != last+1 {
		return fmt.Errorf("%w: run %q event sequence must be %d, got %d", domain.ErrConflict, event.RunID, last+1, event.Sequence)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode run event: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open run event log: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("append run event: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync run event: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close run event: %w", err)
	}
	r.mu.Lock()
	r.sequences[event.RunID] = event.Sequence
	r.mu.Unlock()
	return nil
}

func (r *RunEventRepository) List(ctx context.Context, runID string, afterSequence int64) ([]domain.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := r.eventPath(runID)
	if err != nil {
		return nil, err
	}
	lock := r.runLock(runID)
	lock.RLock()
	defer lock.RUnlock()
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []domain.Event{}, nil
		}
		return nil, fmt.Errorf("open run event log: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	values := []domain.Event{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event domain.Event
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode run event log: %w", err)
		}
		if event.Sequence > afterSequence {
			values = append(values, event)
		}
	}
	return values, nil
}

func (r *RunEventRepository) lastSequenceLocked(runID, path string) (int64, error) {
	r.mu.Lock()
	sequence, exists := r.sequences[runID]
	r.mu.Unlock()
	if exists {
		return sequence, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open run event log: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	last := int64(0)
	for {
		var event domain.Event
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, fmt.Errorf("decode run event log: %w", err)
		}
		if event.Sequence > last {
			last = event.Sequence
		}
	}
	r.mu.Lock()
	r.sequences[runID] = last
	r.mu.Unlock()
	return last, nil
}

func (r *RunEventRepository) eventPath(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "." || runID == ".." || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return "", fmt.Errorf("%w: invalid run id", domain.ErrInvalidInput)
	}
	return filepath.Join(r.root, runID+".jsonl"), nil
}

func (r *RunEventRepository) runLock(runID string) *sync.RWMutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock := r.locks[runID]
	if lock == nil {
		lock = &sync.RWMutex{}
		r.locks[runID] = lock
	}
	return lock
}
