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
// 限制含本文的明細大小，避免大型工具結果不斷拖慢狀態寫入；精簡用量快照另行
// 保留至 Session 刪除，因此淘汰明細不會讓累計值倒退。
const defaultRunRetention = 500

type RunRepository struct {
	mu        sync.RWMutex
	filePath  string
	runs      map[string]domain.Run
	usage     map[string]runUsageSnapshot
	retention int
	protected map[string]bool
	// pruned 記下被淘汰的 run，讓呼叫端有機會清掉對應的事件檔。
	pruned []string
}

type runFile struct {
	Version int                         `json:"version"`
	Runs    map[string]domain.Run       `json:"runs"`
	Usage   map[string]runUsageSnapshot `json:"usage,omitempty"`
}

// 只保留統計所需欄位，不延長提示、工具參數或結果本文的保留期。
// 用 Run ID 覆寫快照，而不是增量相加，取消收尾或重複 Save 才不會重複計費。
type runUsageSnapshot struct {
	SessionID string          `json:"session_id"`
	Usage     domain.RunUsage `json:"usage"`
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
		usage:     map[string]runUsageSnapshot{},
		retention: defaultRunRetention,
		protected: map[string]bool{},
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

// volatileRun 判斷這個 run 是否屬於記憶體隔離對話（因此不會寫進 runs.json）。
func volatileRun(run domain.Run) bool {
	return domain.EphemeralProjectCodeFromID(run.SessionID) != ""
}

// pruneLocked 把 run 數量壓回上限，最舊的先淘汰。回傳是否有變動。
//
// 揮發與持久分兩組各自計算上限。混在一起算的話，根本不會落地的 run 會把該落地
// 的擠出 runs.json——使用者在隔離專案跑得越多，一般專案的歷史消失得越快，而且
// 完全看不出原因。分開之後兩邊互不影響，揮發那組也仍有上限，不會無限成長。
func (r *RunRepository) pruneLocked() bool {
	if r.retention <= 0 {
		return false
	}
	before := len(r.pruned)
	r.pruneGroupLocked(false)
	r.pruneGroupLocked(true)
	return len(r.pruned) > before
}

func (r *RunRepository) pruneGroupLocked(volatile bool) {
	ordered := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		if volatileRun(run) != volatile {
			continue
		}
		ordered = append(ordered, run)
	}
	if len(ordered) <= r.retention {
		return
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CreatedAt.Before(ordered[j].CreatedAt) })
	for index := 0; index < len(ordered)-r.retention; index++ {
		// 只淘汰已經結束的 run。paused 與 waiting_approval 看起來像停住了，
		// 其實都還會寫回狀態——用「是否為終態」判斷才不會漏掉。
		if !terminalRunStatus(ordered[index].Status) || r.protected[ordered[index].ID] {
			continue
		}
		delete(r.runs, ordered[index].ID)
		r.pruned = append(r.pruned, ordered[index].ID)
	}
}

func (r *RunRepository) ProtectRun(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.protected == nil {
		r.protected = make(map[string]bool)
	}
	r.protected[id] = true
}

func (r *RunRepository) ReleaseRun(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.protected, id)
	// 留待下一次 Save 整理，釋放生命週期保護本身不觸發不可回報的磁碟寫入。
}

func (r *RunRepository) Save(ctx context.Context, run domain.Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run.ID = strings.TrimSpace(run.ID)
	run.SessionID = strings.TrimSpace(run.SessionID)
	if run.ID == "" {
		return fmt.Errorf("%w: run id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.runs[run.ID]; ok && previous.SessionID != run.SessionID {
		return fmt.Errorf("%w: run session cannot change", domain.ErrConflict)
	}
	if previous, ok := r.usage[run.ID]; ok {
		if previous.SessionID != run.SessionID {
			return fmt.Errorf("%w: run usage session cannot change", domain.ErrConflict)
		}
		// 狀態更新省略 usage 時仍保留既有快照，避免 Run 與 Session 查詢互相矛盾。
		if run.Usage == nil {
			usage := cloneUsage(previous.Usage)
			run.Usage = &usage
		}
	}
	// 候選狀態與已提交狀態分離；磁碟失敗必須連淘汰清單一起回復。
	previousRuns, previousUsage, previousPruned := r.runs, r.usage, r.pruned
	r.runs = make(map[string]domain.Run, len(previousRuns)+1)
	for id, value := range previousRuns {
		r.runs[id] = value
	}
	r.usage = make(map[string]runUsageSnapshot, len(previousUsage)+1)
	for id, value := range previousUsage {
		r.usage[id] = value
	}
	r.pruned = append([]string(nil), previousPruned...)
	r.runs[run.ID] = cloneRun(run)
	if run.Usage != nil {
		r.usage[run.ID] = runUsageSnapshot{SessionID: run.SessionID, Usage: cloneUsage(*run.Usage)}
	}
	r.pruneLocked()
	if err := r.persistLocked(); err != nil {
		r.runs, r.usage, r.pruned = previousRuns, previousUsage, previousPruned
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

func (r *RunRepository) ListSessionRunUsage(ctx context.Context, sessionID string) ([]domain.RunUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 固定順序也避免浮點加總因 map 遍歷順序改變而跳動。
	ids := make([]string, 0)
	for id, value := range r.usage {
		if value.SessionID == sessionID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	values := make([]domain.RunUsage, 0, len(ids))
	for _, id := range ids {
		values = append(values, cloneUsage(r.usage[id].Usage))
	}
	return values, nil
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
	previousUsage := r.usage
	r.usage = make(map[string]runUsageSnapshot, len(previousUsage))
	for id, value := range previousUsage {
		if value.SessionID != sessionID {
			r.usage[id] = value
		}
	}
	deleted := []string{}
	for id, run := range r.runs {
		if run.SessionID != sessionID {
			continue
		}
		deleted = append(deleted, id)
		delete(r.runs, id)
	}
	if len(deleted) == 0 && len(r.usage) == len(previousUsage) {
		return nil, nil
	}
	if err := r.persistLocked(); err != nil {
		r.runs = previous
		r.usage = previousUsage
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
	if snapshot.Usage != nil {
		r.usage = snapshot.Usage
	}
	// 舊格式只能遷移尚存的快照，已被舊版本淘汰的 token 不可臆測補回。
	for id, run := range r.runs {
		if run.Usage != nil {
			r.usage[id] = runUsageSnapshot{SessionID: run.SessionID, Usage: cloneUsage(*run.Usage)}
		}
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
		if volatileRun(run) {
			volatile++
		}
	}
	if volatile == 0 {
		return r.runs
	}
	values := make(map[string]domain.Run, len(r.runs)-volatile)
	for id, run := range r.runs {
		if volatileRun(run) {
			continue
		}
		values[id] = run
	}
	return values
}

func (r *RunRepository) persistLocked() error {
	usage := make(map[string]runUsageSnapshot)
	for id, value := range r.usage {
		if domain.EphemeralProjectCodeFromID(value.SessionID) == "" {
			usage[id] = value
		}
	}
	data, err := json.MarshalIndent(runFile{Version: 2, Runs: r.persistableRunsLocked(), Usage: usage}, "", "  ")
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
	copyRun.AttachmentIDs = append([]string(nil), run.AttachmentIDs...)
	if run.StartedAt != nil {
		value := *run.StartedAt
		copyRun.StartedAt = &value
	}
	if run.CompletedAt != nil {
		value := *run.CompletedAt
		copyRun.CompletedAt = &value
	}
	if run.Metadata != nil {
		copyRun.Metadata = make(map[string]any, len(run.Metadata))
		for key, value := range run.Metadata {
			copyRun.Metadata[key] = value
		}
	}
	if run.Result != nil {
		result := *run.Result
		if run.Result.BudgetExceeded != nil {
			value := *run.Result.BudgetExceeded
			result.BudgetExceeded = &value
		}
		if run.Result.Completion != nil {
			value := *run.Result.Completion
			value.UnresolvedFailures = append([]domain.UnresolvedToolFailure(nil), value.UnresolvedFailures...)
			result.Completion = &value
		}
		if run.Result.Usage != nil {
			usage := cloneUsage(*run.Result.Usage)
			result.Usage = &usage
		}
		copyRun.Result = &result
	}
	if run.Usage != nil {
		usage := cloneUsage(*run.Usage)
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

func cloneUsage(value domain.RunUsage) domain.RunUsage {
	if len(value.ByModel) > 0 {
		parts := make([]domain.RunUsage, 0, len(value.ByModel))
		for _, part := range value.ByModel {
			part.ByModel = nil
			parts = append(parts, cloneUsage(part))
		}
		value.ByModel = parts
	}
	if value.EstimatedCostUSD != nil {
		cost := *value.EstimatedCostUSD
		value.EstimatedCostUSD = &cost
	}
	return value
}
