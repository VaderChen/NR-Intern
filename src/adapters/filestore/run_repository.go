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

// defaultRunRetention 是 runs.json 保留的 run 筆數上限。
//
// 這個檔案每次 Save 都整份重寫，而一次 run 會 Save 好幾次（啟動、狀態轉換、完成）。
// 沒有上限時，寫入成本正比於「這台機器跑過的 run 總數」，只會越來越慢也永遠不會
// 回落。500 筆足夠涵蓋任何實際的回顧需求，完整紀錄仍在各 session 的 transcript 裡。
const defaultRunRetention = 500

type RunRepository struct {
	mu        sync.RWMutex
	filePath  string
	runs      map[string]domain.Run
	retention int
	// pruned 記下被淘汰的 run，讓呼叫端有機會清掉對應的事件檔。
	pruned []string
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
		filePath:  filepath.Join(root, "runs.json"),
		runs:      map[string]domain.Run{},
		retention: defaultRunRetention,
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	changed := repository.markInterruptedRuns()
	if repository.pruneLocked() {
		changed = true
	}
	if changed {
		if err := repository.persistLocked(); err != nil {
			return nil, err
		}
	}
	return repository, nil
}

// TakePrunedRunIDs 取出並清空自上次呼叫以來被淘汰的 run ID。
//
// RunRepository 不知道事件檔放在哪裡，所以不自己刪；由同時握有兩個 store 的
// 呼叫端接手。回傳後就從清單移除，避免重複處理。
func (r *RunRepository) TakePrunedRunIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pruned) == 0 {
		return nil
	}
	values := r.pruned
	r.pruned = nil
	return values
}

// terminalRunStatus 判斷 run 是否已經結束、不會再被寫入。
func terminalRunStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled:
		return true
	default:
		return false
	}
}

// pruneLocked 把 run 數量壓回上限，最舊的先淘汰。回傳是否有變動。
func (r *RunRepository) pruneLocked() bool {
	if r.retention <= 0 || len(r.runs) <= r.retention {
		return false
	}
	ordered := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		ordered = append(ordered, run)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CreatedAt.Before(ordered[j].CreatedAt) })
	for index := 0; index < len(ordered)-r.retention; index++ {
		// 只淘汰已經結束的 run。paused 與 waiting_approval 看起來像停住了，
		// 其實都還會寫回狀態——用「是否為終態」判斷才不會漏掉。
		if !terminalRunStatus(ordered[index].Status) {
			continue
		}
		delete(r.runs, ordered[index].ID)
		r.pruned = append(r.pruned, ordered[index].ID)
	}
	return len(r.pruned) > 0
}

func (r *RunRepository) Save(_ context.Context, run domain.Run) error {
	if strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("%w: run id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, existed := r.runs[run.ID]
	r.runs[run.ID] = cloneRun(run)
	r.pruneLocked()
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

// DeleteSession 在記憶體隔離 Project 重啟清理時移除整個 Session 的 Run。
// 回傳 ID 讓呼叫端同步刪除分開保存的事件檔，避免只剩無法從介面抵達的稽核資料。
func (r *RunRepository) DeleteSession(ctx context.Context, sessionID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := make(map[string]domain.Run, len(r.runs))
	for id, run := range r.runs {
		previous[id] = cloneRun(run)
	}
	deleted := []string{}
	for id, run := range r.runs {
		if run.SessionID != sessionID {
			continue
		}
		deleted = append(deleted, id)
		delete(r.runs, id)
	}
	if len(deleted) == 0 {
		return nil, nil
	}
	if err := r.persistLocked(); err != nil {
		r.runs = previous
		return nil, err
	}
	sort.Strings(deleted)
	return deleted, nil
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

// persistableRunsLocked 回傳可以寫進 runs.json 的 run。
//
// 記憶體隔離對話的 run 一律不落地。runs.json 是單一檔案、每次 Save 整份重寫，
// 沒辦法像 session 那樣按根目錄分流；而 Run.Input 存的是使用者提問原文，正是
// 這個功能最不想留在硬碟上的東西。留在記憶體裡不影響本次執行期間的任何讀取，
// 程序結束就消失——RAM disk 上的對話本來也活不過重啟，語意一致。
//
// 沒有隔離 run 時直接回傳原 map，避免每次 Save 都多複製一份。
func (r *RunRepository) persistableRunsLocked() map[string]domain.Run {
	volatile := 0
	for _, run := range r.runs {
		if domain.EphemeralProjectCodeFromID(run.SessionID) != "" {
			volatile++
		}
	}
	if volatile == 0 {
		return r.runs
	}
	values := make(map[string]domain.Run, len(r.runs)-volatile)
	for id, run := range r.runs {
		if domain.EphemeralProjectCodeFromID(run.SessionID) != "" {
			continue
		}
		values[id] = run
	}
	return values
}

func (r *RunRepository) persistLocked() error {
	data, err := json.MarshalIndent(runFile{Version: 1, Runs: r.persistableRunsLocked()}, "", "  ")
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
