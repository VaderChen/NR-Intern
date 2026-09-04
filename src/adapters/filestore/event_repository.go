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
	"sync/atomic"
)

var _ ports.RunEventRepository = (*RunEventRepository)(nil)

type RunEventRepository struct {
	root string

	roots atomic.Pointer[ProjectRoots]

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

// SetProjectRoots 讓事件檔跟著所屬專案走。
//
// 事件檔是 run 期間的逐筆完整紀錄（含工具輸入與模型輸出），單檔可達數 MB，
// 而且沒有記憶體快取——不分流的話，隔離對話最詳細的內容反而全部留在硬碟上。
// 解析用 Run ID，見 domain.NewRunIDForSession。
func (r *RunEventRepository) SetProjectRoots(roots ProjectRoots) {
	if roots == nil {
		r.roots.Store(nil)
		return
	}
	r.roots.Store(&roots)
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
	// 建構時只建得了預設根的目錄；RAM disk 可能是之後才掛上來的，所以這裡補建。
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create run event store: %w", err)
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

// ListRunIDs 回傳預設根的事件目錄裡目前有檔案的所有 run ID。
//
// 清理時需要看到磁碟上真正有什麼：只比對 run 紀錄會漏掉孤兒檔（run 紀錄已淘汰、
// 事件檔還在），那正是佔空間的大宗。
//
// 刻意不掃 RAM disk：那裡的事件檔會隨磁碟卸載一起消失，不需要也不該被當成
// 孤兒清理——磁碟還掛著就代表那個專案還在用。
func (r *RunEventRepository) ListRunIDs() ([]string, error) {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read run event store: %w", err)
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		values = append(values, strings.TrimSuffix(entry.Name(), ".jsonl"))
	}
	return values, nil
}

// Delete 移除單一 run 的事件檔。
//
// 事件檔是 run 期間的完整逐筆紀錄，單檔可以到好幾 MB；run 被淘汰之後就沒有讀取
// 路徑，留著只是佔磁碟。找不到檔案視為已完成，不算錯誤。
func (r *RunEventRepository) Delete(runID string) error {
	path, err := r.eventPath(runID)
	if err != nil {
		return err
	}
	lock := r.runLock(runID)
	lock.Lock()
	removeErr := os.Remove(path)
	lock.Unlock()
	r.mu.Lock()
	delete(r.sequences, runID)
	delete(r.locks, runID)
	r.mu.Unlock()
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("delete run events: %w", removeErr)
	}
	return nil
}

func (r *RunEventRepository) eventPath(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "." || runID == ".." || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return "", fmt.Errorf("%w: invalid run id", domain.ErrInvalidInput)
	}
	return filepath.Join(r.eventRoot(runID), runID+".jsonl"), nil
}

// eventRoot 回傳指定 run 的事件目錄。
//
// 隔離專案的 RAM disk 尚未掛載（重開機後必然如此）時回到預設根：該 run 的
// 事件檔本來就不在那裡，讀取會得到空事件流，正是揮發語意要的結果。
func (r *RunEventRepository) eventRoot(runID string) string {
	roots := r.roots.Load()
	if roots == nil {
		return r.root
	}
	resolved := strings.TrimSpace((*roots).RootFor(runID))
	if resolved == "" {
		return r.root
	}
	return filepath.Join(resolved, "runs", "events")
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
