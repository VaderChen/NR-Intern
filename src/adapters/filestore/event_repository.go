package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
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
	locks     [64]sync.RWMutex
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
	// 任何寫入／Sync 失敗都可能留下未知尾筆；下次必須重新掃描，不能沿用快取。
	written := false
	defer func() {
		if !written {
			r.mu.Lock()
			delete(r.sequences, event.RunID)
			r.mu.Unlock()
		}
	}()
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
	written = true
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
	lock.Lock()
	defer lock.Unlock()
	values, last, err := r.readEventLogLocked(ctx, runID, path, afterSequence, true)
	r.mu.Lock()
	if err != nil {
		// 發現中段損壞後不能讓 Append 以舊快取繼續寫入。
		delete(r.sequences, runID)
	} else {
		r.sequences[runID] = last
	}
	r.mu.Unlock()
	return values, err
}

func (r *RunEventRepository) lastSequenceLocked(runID, path string) (int64, error) {
	r.mu.Lock()
	sequence, exists := r.sequences[runID]
	r.mu.Unlock()
	if exists {
		return sequence, nil
	}
	_, last, err := r.readEventLogLocked(context.Background(), runID, path, 0, false)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.sequences[runID] = last
	r.mu.Unlock()
	return last, nil
}

// 逐行驗證，只修復缺少換行且 JSON 未完成的最後一筆。中段錯誤與序號異常不能跳過。
func (r *RunEventRepository) readEventLogLocked(ctx context.Context, runID, path string, after int64, collect bool) ([]domain.Event, int64, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Event{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	values := []domain.Event{}
	var offset, last int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, last, err
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, last, readErr
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if errors.Is(readErr, io.EOF) {
				break
			}
			offset += int64(len(line))
			continue
		}
		var event domain.Event
		decodeErr := json.Unmarshal(line, &event)
		if decodeErr != nil {
			var probe domain.Event
			tailErr := json.NewDecoder(bytes.NewReader(line)).Decode(&probe)
			if errors.Is(readErr, io.EOF) && errors.Is(tailErr, io.ErrUnexpectedEOF) {
				if err := repairEventTail(path, offset, false); err != nil {
					return nil, last, err
				}
				break
			}
			return nil, last, fmt.Errorf("run %s 的事件紀錄在位移 %d 損壞，已隔離讀寫，原檔保持不變", runID, offset)
		}
		if event.RunID != runID || event.ID == "" || event.Type == "" || event.Sequence != last+1 {
			return nil, last, fmt.Errorf("run %s 的事件身分或序號在位移 %d 不一致，原檔保持不變", runID, offset)
		}
		last = event.Sequence
		if collect && event.Sequence > after {
			values = append(values, event)
		}
		offset += int64(len(line))
		if errors.Is(readErr, io.EOF) {
			if err := repairEventTail(path, offset, true); err != nil {
				return nil, last, err
			}
			break
		}
	}
	r.mu.Lock()
	r.sequences[runID] = last
	r.mu.Unlock()
	return values, last, nil
}

// 同目錄備份可隨 ProjectRoots 留在 RAM，不把隔離資料複製到持久儲存。
func repairEventTail(path string, offset int64, appendNewline bool) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	backupPath := path + ".recovery-" + domain.NewID("tail") + ".bak"
	backup, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(backup, source)
	syncErr := backup.Sync()
	closeErr := backup.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if appendNewline {
		_, err = file.WriteAt([]byte{'\n'}, offset)
	} else {
		err = file.Truncate(offset)
	}
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	slog.Warn("已備份並修復事件尾筆", "backup_path", backupPath, "valid_bytes", offset)
	return nil
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
	defer lock.Unlock()
	return r.deleteLocked(runID, path)
}

func (r *RunEventRepository) deleteLocked(runID, path string) error {
	removeErr := os.Remove(path)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("delete run events: %w", removeErr)
	}
	r.mu.Lock()
	delete(r.sequences, runID)
	// 固定分片鎖不隨事件刪除而換身分，也不隨歷史 Run 數量無限成長。
	r.mu.Unlock()
	return nil
}

// DeleteSession 不能只依賴 Run 清單，否則已淘汰明細的事件會留在磁碟上。
// 第一筆事件就帶有 Session ID；持有同一把事件鎖直到刪除，避免讀取與清理交錯。
func (r *RunEventRepository) DeleteSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	root, err := r.eventRoot(sessionID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".jsonl")
		path := filepath.Join(root, entry.Name())
		if err := r.deleteSessionEventFile(runID, path, sessionID); err != nil {
			return err
		}
	}
	return nil
}

func (r *RunEventRepository) deleteSessionEventFile(runID, path, sessionID string) error {
	lock := r.runLock(runID)
	lock.Lock()
	defer lock.Unlock()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var header struct {
		SessionID string `json:"session_id"`
	}
	decodeErr := json.NewDecoder(bufio.NewReader(file)).Decode(&header)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("cannot identify session of event file %s: %w", runID, decodeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if header.SessionID != sessionID {
		return nil
	}
	return r.deleteLocked(runID, path)
}

func (r *RunEventRepository) eventPath(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "." || runID == ".." || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return "", fmt.Errorf("%w: invalid run id", domain.ErrInvalidInput)
	}
	root, err := r.eventRoot(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, runID+".jsonl"), nil
}

// eventRoot 回傳指定 run 的事件目錄。
//
// 磁碟不在時回傳錯誤而不是退回 dataDir，理由見 resolveVolatileRoot。
func (r *RunEventRepository) eventRoot(runID string) (string, error) {
	root, routed, err := resolveVolatileRoot(&r.roots, runID, r.root)
	if err != nil {
		return "", err
	}
	if !routed {
		return root, nil
	}
	return filepath.Join(root, "runs", "events"), nil
}

func (r *RunEventRepository) runLock(runID string) *sync.RWMutex {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(runID))
	return &r.locks[hash.Sum32()%uint32(len(r.locks))]
}
