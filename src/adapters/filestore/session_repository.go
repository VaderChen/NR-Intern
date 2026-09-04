package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/valueutil"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	sessionMetadataFile = "session.json"
	sessionEntriesFile  = "entries.jsonl"
)

// ProjectRoots 決定資料要落在哪個根目錄。
//
// 記憶體隔離專案的資料要放在該專案自己的 RAM disk 上，而不是 dataDir。
// 歸屬資訊編碼在 ID 裡（對話建立後不會換專案，見
// Service.validateEphemeralSessionMove），所以 RootFor 只需要字串解析，
// 不必查詢狀態，也沒有需要失效的快取。
//
// Session、計畫、附件用 Session ID 解析，事件檔用 Run ID——Run ID 沿用所屬
// Session 的編碼，因此同一個實作能服務兩種 ID，不需要各自的解析器。
//
// 需要兩個方法而不是單一函式：讀寫單一項目靠 ID 解析就夠，但 List 要
// 列舉所有對話，必須知道還有哪些根目錄存在，否則側邊欄會看不到隔離專案的對話。
type ProjectRoots interface {
	// RootFor 回傳指定 ID 應該使用的根目錄；空字串代表使用預設根。
	RootFor(id string) string
	// AdditionalRoots 回傳預設根以外、也可能存放資料的根目錄。
	// 已經消失的根（例如 RAM disk 尚未掛載）可以直接省略。
	AdditionalRoots() []string
}

// SessionIDFactory 產生新的 Session ID。
//
// 記憶體隔離專案要把歸屬編進 ID，而判斷「這個 Project 是不是隔離的」需要查
// Project 儲存——那是 filestore 不該知道的事，所以做成注入點。
// 回傳空字串代表使用預設格式。
type SessionIDFactory func(projectID string) string

type SessionRepository struct {
	root string
	// roots 用 atomic 讀取：它在啟動時設定一次、之後只讀，而 sessionDir
	// 是每次存取都會經過的熱路徑，不值得為它加鎖。
	roots     atomic.Pointer[ProjectRoots]
	idFactory atomic.Pointer[SessionIDFactory]

	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	sequences map[string]int64
	// indexes 是各 session transcript 的位置索引，見 session_index.go。
	indexes map[string]*entryIndex
}

// SetProjectRoots 注入根目錄解析。傳入 nil 會回到單一根目錄的行為。
func (r *SessionRepository) SetProjectRoots(roots ProjectRoots) {
	if r == nil {
		return
	}
	if roots == nil {
		r.roots.Store(nil)
		return
	}
	r.roots.Store(&roots)
}

// SetSessionIDFactory 注入 Session ID 產生方式。傳入 nil 會回到預設格式。
func (r *SessionRepository) SetSessionIDFactory(factory SessionIDFactory) {
	if r == nil {
		return
	}
	if factory == nil {
		r.idFactory.Store(nil)
		return
	}
	r.idFactory.Store(&factory)
}

// newSessionID 產生新的 Session ID。
func (r *SessionRepository) newSessionID(projectID string) string {
	if factory := r.idFactory.Load(); factory != nil {
		if id := strings.TrimSpace((*factory)(projectID)); id != "" {
			return id
		}
	}
	return domain.NewID("session")
}

// rootFor 回傳指定 session 應該使用的根目錄。
func (r *SessionRepository) rootFor(sessionID string) string {
	roots := r.roots.Load()
	if roots == nil {
		return r.root
	}
	if resolved := strings.TrimSpace((*roots).RootFor(sessionID)); resolved != "" {
		return resolved
	}
	return r.root
}

// searchRoots 回傳列舉 session 時要掃描的所有根目錄，預設根排在最前面。
func (r *SessionRepository) searchRoots() []string {
	result := []string{r.root}
	roots := r.roots.Load()
	if roots == nil {
		return result
	}
	seen := map[string]bool{r.root: true}
	for _, root := range (*roots).AdditionalRoots() {
		if root = strings.TrimSpace(root); root != "" && !seen[root] {
			seen[root] = true
			result = append(result, root)
		}
	}
	return result
}

func NewSessionRepository(dataDir string) (*SessionRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	return &SessionRepository{
		root:      root,
		locks:     map[string]*sync.Mutex{},
		sequences: map[string]int64{},
		indexes:   map[string]*entryIndex{},
	}, nil
}

func (r *SessionRepository) Create(ctx context.Context, agentID string, input domain.CreateSessionInput) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return domain.Session{}, fmt.Errorf("%w: agent id is required", domain.ErrInvalidInput)
	}
	thinkingMode, err := domain.NormalizeThinkingMode(input.ThinkingMode)
	if err != nil {
		return domain.Session{}, err
	}
	input.ThinkingMode = thinkingMode
	position := 0
	existing, err := r.List(ctx, agentID)
	if err != nil {
		return domain.Session{}, err
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	projectID := strings.TrimSpace(input.ProjectID)
	for _, value := range existing {
		if value.WorkspaceID == workspaceID && value.ProjectID == projectID && value.Position >= position {
			position = value.Position + 1
		}
	}
	localNow := time.Now()
	now := localNow.UTC()
	// ID 決定目錄落在哪個根，所以要在建立目錄之前產生。
	sessionID := r.newSessionID(projectID)
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	workspace := filepath.Join(directory, "workspace")
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		return domain.Session{}, fmt.Errorf("create session workspace: %w", err)
	}
	metadata := valueutil.CloneMap(input.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	delete(metadata, "sandbox_roots")
	metadata["workspace_root"] = workspace
	permissionProfile := strings.TrimSpace(input.PermissionProfile)
	if permissionProfile == "" {
		permissionProfile = "default"
	}
	session := domain.Session{
		ID:                sessionID,
		AgentID:           agentID,
		WorkspaceID:       workspaceID,
		ProjectID:         projectID,
		Title:             valueutil.FirstNonEmpty(input.Title, localNow.Format("01/02 15:04")),
		ProviderID:        strings.TrimSpace(input.ProviderID),
		Model:             strings.TrimSpace(input.Model),
		ThinkingMode:      input.ThinkingMode,
		LockPlans:         input.LockPlans,
		PermissionProfile: permissionProfile,
		Pinned:            input.Pinned,
		Position:          position,
		Metadata:          metadata,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if session.Pinned {
		session.PinnedAt = &now
	}
	lock := r.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := r.writeSessionLocked(session); err != nil {
		return domain.Session{}, err
	}
	entriesPath := filepath.Join(directory, sessionEntriesFile)
	file, err := os.OpenFile(entriesPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session transcript: %w", err)
	}
	if err := file.Close(); err != nil {
		return domain.Session{}, fmt.Errorf("close session transcript: %w", err)
	}
	r.mu.Lock()
	r.sequences[sessionID] = 0
	r.mu.Unlock()
	return session, nil
}

func (r *SessionRepository) List(ctx context.Context, agentID string) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessions := []domain.Session{}
	seen := map[string]bool{}
	for index, root := range r.searchRoots() {
		items, err := os.ReadDir(root)
		if err != nil {
			// 預設根讀不到是真的故障；額外的根可能只是 RAM disk 還沒掛載，
			// 那不該讓整份清單失敗。
			if index == 0 {
				return nil, fmt.Errorf("list sessions: %w", err)
			}
			continue
		}
		for _, item := range items {
			if !item.IsDir() || seen[item.Name()] {
				continue
			}
			session, readErr := r.Get(ctx, item.Name())
			if readErr != nil {
				continue
			}
			if strings.TrimSpace(agentID) != "" && session.AgentID != strings.TrimSpace(agentID) {
				continue
			}
			seen[item.Name()] = true
			sessions = append(sessions, session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	return sessions, nil
}

func (r *SessionRepository) Get(ctx context.Context, sessionID string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	data, err := os.ReadFile(filepath.Join(directory, sessionMetadataFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.Session{}, fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
		}
		return domain.Session{}, fmt.Errorf("read session: %w", err)
	}
	var session domain.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return domain.Session{}, fmt.Errorf("decode session: %w", err)
	}
	// transcript 的 append 不重寫 session.json，因此最後活動時間取兩者較晚者。
	if info, statErr := os.Stat(filepath.Join(directory, sessionEntriesFile)); statErr == nil {
		if modified := info.ModTime().UTC(); modified.After(session.UpdatedAt) {
			session.UpdatedAt = modified
		}
	}
	return session, nil
}

func (r *SessionRepository) Update(ctx context.Context, session domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	lock := r.sessionLock(session.ID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := r.Get(ctx, session.ID); err != nil {
		return err
	}
	session.UpdatedAt = time.Now().UTC()
	return r.writeSessionLocked(session)
}

func (r *SessionRepository) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return err
	}
	lock := r.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Stat(filepath.Join(directory, sessionMetadataFile)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: session %q", domain.ErrNotFound, sessionID)
		}
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	r.mu.Lock()
	delete(r.sequences, sessionID)
	delete(r.locks, sessionID)
	delete(r.indexes, sessionID)
	r.mu.Unlock()
	return nil
}

func (r *SessionRepository) AppendEntry(ctx context.Context, sessionID string, entry domain.SessionEntry) (domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionEntry{}, err
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return domain.SessionEntry{}, err
	}
	lock := r.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	session, err := r.Get(ctx, sessionID)
	if err != nil {
		return domain.SessionEntry{}, err
	}
	sequence, err := r.nextSequenceLocked(sessionID, filepath.Join(directory, sessionEntriesFile))
	if err != nil {
		return domain.SessionEntry{}, err
	}
	if strings.TrimSpace(entry.ID) == "" {
		entry.ID = domain.NewID("entry")
	}
	entry.SessionID = sessionID
	entry.Sequence = sequence
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if entry.Message != nil {
		entry.Message.SessionID = sessionID
		if entry.Message.CreatedAt.IsZero() {
			entry.Message.CreatedAt = entry.CreatedAt
		}
	}
	entry = withoutPersistedReasoning(entry)
	data, err := json.Marshal(entry)
	if err != nil {
		return domain.SessionEntry{}, fmt.Errorf("encode session entry: %w", err)
	}
	entriesPath := filepath.Join(directory, sessionEntriesFile)
	// 先取寫入前的檔案大小：那就是這一筆在索引裡的位移。
	offset, err := transcriptSize(entriesPath)
	if err != nil {
		return domain.SessionEntry{}, err
	}
	file, err := os.OpenFile(entriesPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return domain.SessionEntry{}, fmt.Errorf("open session transcript: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return domain.SessionEntry{}, fmt.Errorf("append session transcript: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return domain.SessionEntry{}, fmt.Errorf("sync session transcript: %w", err)
	}
	if err := file.Close(); err != nil {
		return domain.SessionEntry{}, fmt.Errorf("close session transcript: %w", err)
	}
	r.mu.Lock()
	r.sequences[sessionID] = sequence
	r.mu.Unlock()
	// 索引就地延長，不必為了下一次讀取重掃整個檔案。
	r.noteAppendedEntry(sessionID, sequence, entry.Type, offset, len(data)+1)
	// 這裡刻意不重寫 session.json。一次 run 會 append 十幾筆 entry，
	// 每筆都做一次完整 marshal + 暫存檔 + rename 只為了更新 UpdatedAt，
	// 是長 session 變慢的主因之一。UpdatedAt 改由讀取時併入 transcript 的 mtime。
	_ = session
	return entry, nil
}

// entryHeader 讓掃描 transcript 時能先判斷是否需要完整解碼。
// 大型工具輸出佔了 transcript 絕大部分體積，跳過它們是主要的節省來源。
type entryHeader struct {
	Sequence int64
	Type     string
}

// decodeEntryHeader 只讀到取得 sequence 與 type 為止就停止解析。
//
// 用 json.Unmarshal 解成小結構仍然會掃過整行（包含幾十 KB 的工具輸出），
// 因此這裡改用 streaming decoder：SessionEntry 的欄位順序讓這兩個欄位出現在
// message 與 data 之前，實務上只會讀到行首一小段。欄位順序不同也仍然正確，
// 只是要多跳過幾個值。
func decodeEntryHeader(line []byte) (entryHeader, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil {
		return entryHeader{}, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return entryHeader{}, fmt.Errorf("session entry is not a JSON object")
	}
	header := entryHeader{}
	haveSequence := false
	haveType := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return entryHeader{}, err
		}
		key, _ := keyToken.(string)
		switch key {
		case "sequence":
			if err := decoder.Decode(&header.Sequence); err != nil {
				return entryHeader{}, err
			}
			haveSequence = true
		case "type":
			if err := decoder.Decode(&header.Type); err != nil {
				return entryHeader{}, err
			}
			haveType = true
		default:
			var skipped json.RawMessage
			if err := decoder.Decode(&skipped); err != nil {
				return entryHeader{}, err
			}
		}
		if haveSequence && haveType {
			return header, nil
		}
	}
	return header, nil
}

// scanEntries 逐行掃描 transcript，只對 accept 回傳 true 的行做完整解碼。
func (r *SessionRepository) scanEntries(ctx context.Context, sessionID string, accept func(entryHeader) bool, visit func(domain.SessionEntry) error) error {
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return err
	}
	if _, err := r.Get(ctx, sessionID); err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(directory, sessionEntriesFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open session transcript: %w", err)
	}
	defer file.Close()
	if err := readJSONLines(ctx, file, func(line []byte) error {
		if len(line) == 0 {
			return nil
		}
		header, err := decodeEntryHeader(line)
		if err != nil {
			return fmt.Errorf("decode session entry header: %w", err)
		}
		if accept != nil && !accept(header) {
			return nil
		}
		var entry domain.SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("decode session entry: %w", err)
		}
		entry = withoutPersistedReasoning(entry)
		if err := visit(entry); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan session transcript: %w", err)
	}
	return nil
}

// ListEntriesAfter 只讀取序號大於 afterSequence 的部分。
//
// 用索引直接 seek 到起點，不從檔頭掃起：壓縮後每個 turn 需要的通常只是最後幾筆，
// 但檔案本身不會因為壓縮而變小，從頭掃的成本會隨 session 長度無上限成長。
func (r *SessionRepository) ListEntriesAfter(ctx context.Context, sessionID string, afterSequence int64) ([]domain.SessionEntry, error) {
	entries, _, err := r.entriesPage(ctx, sessionID, afterSequence, 0)
	return entries, err
}

// ListEntriesPage 從 afterSequence 之後最多讀取 limit 筆，並回報後面是否還有。
//
// limit <= 0 表示不限筆數。剩餘筆數由索引直接得出，不需要為了 has_more 多讀一次檔案。
func (r *SessionRepository) ListEntriesPage(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]domain.SessionEntry, bool, error) {
	return r.entriesPage(ctx, sessionID, afterSequence, limit)
}

func (r *SessionRepository) entriesPage(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]domain.SessionEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return nil, false, err
	}
	if _, err := r.Get(ctx, sessionID); err != nil {
		return nil, false, err
	}
	path := filepath.Join(directory, sessionEntriesFile)
	index, indexErr := r.entryIndexFor(ctx, sessionID, path)
	if indexErr != nil {
		// 索引建不起來（序號不遞增、檔案損毀）時退回全檔掃描，功能不受影響。
		entries := []domain.SessionEntry{}
		scanErr := r.scanEntries(ctx, sessionID,
			func(header entryHeader) bool { return header.Sequence > afterSequence },
			func(entry domain.SessionEntry) error {
				entries = append(entries, entry)
				return nil
			})
		if scanErr != nil {
			return nil, false, scanErr
		}
		if limit > 0 && len(entries) > limit {
			return entries[:limit], true, nil
		}
		return entries, false, nil
	}
	r.mu.Lock()
	offset, remaining := index.offsetAfter(afterSequence)
	r.mu.Unlock()
	if remaining == 0 {
		return []domain.SessionEntry{}, false, nil
	}
	wanted := remaining
	hasMore := false
	if limit > 0 && limit < remaining {
		wanted = limit
		hasMore = true
	}
	entries := make([]domain.SessionEntry, 0, wanted)
	if err := r.readEntriesAt(ctx, path, offset, wanted, func(entry domain.SessionEntry) {
		entries = append(entries, entry)
	}); err != nil {
		return nil, false, err
	}
	return entries, hasMore, nil
}

// readEntriesAt 從指定位移解碼最多 count 筆 entry。
func (r *SessionRepository) readEntriesAt(ctx context.Context, path string, offset int64, count int, visit func(domain.SessionEntry)) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open session transcript: %w", err)
	}
	defer file.Close()
	read := 0
	err = readJSONLinesAt(ctx, file, offset, func(_ int64, line []byte) error {
		if read >= count {
			return errStopReading
		}
		var entry domain.SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("decode session entry: %w", err)
		}
		visit(withoutPersistedReasoning(entry))
		read++
		if read >= count {
			return errStopReading
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopReading) {
		return fmt.Errorf("read session transcript: %w", err)
	}
	return nil
}

// errStopReading 讓 readJSONLinesAt 在取滿筆數後立刻停止，不再讀剩下的檔案。
var errStopReading = errors.New("stop reading session transcript")

// LatestEntryOfType 回傳指定型別中序號最大的 entry。
//
// 型別存在索引裡，因此不必掃描檔案就能算出位置，只讀那一行。
func (r *SessionRepository) LatestEntryOfType(ctx context.Context, sessionID, entryType string) (domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionEntry{}, err
	}
	entryType = strings.TrimSpace(entryType)
	directory, dirErr := r.sessionDir(sessionID)
	if dirErr != nil {
		return domain.SessionEntry{}, dirErr
	}
	if _, err := r.Get(ctx, sessionID); err != nil {
		return domain.SessionEntry{}, err
	}
	path := filepath.Join(directory, sessionEntriesFile)
	if index, indexErr := r.entryIndexFor(ctx, sessionID, path); indexErr == nil {
		r.mu.Lock()
		offset, ok := index.latestOffsetOfType(entryType)
		r.mu.Unlock()
		if !ok {
			return domain.SessionEntry{}, fmt.Errorf("%w: %s entry in session %q", domain.ErrNotFound, entryType, sessionID)
		}
		var latest domain.SessionEntry
		found := false
		if err := r.readEntriesAt(ctx, path, offset, 1, func(entry domain.SessionEntry) {
			latest = entry
			found = true
		}); err != nil {
			return domain.SessionEntry{}, err
		}
		if !found {
			return domain.SessionEntry{}, fmt.Errorf("%w: %s entry in session %q", domain.ErrNotFound, entryType, sessionID)
		}
		return latest, nil
	}
	found := false
	var latest domain.SessionEntry
	err := r.scanEntries(ctx, sessionID,
		func(header entryHeader) bool { return header.Type == entryType },
		func(entry domain.SessionEntry) error {
			if !found || entry.Sequence >= latest.Sequence {
				latest = entry
				found = true
			}
			return nil
		})
	if err != nil {
		return domain.SessionEntry{}, err
	}
	if !found {
		return domain.SessionEntry{}, fmt.Errorf("%w: %s entry in session %q", domain.ErrNotFound, entryType, sessionID)
	}
	return latest, nil
}

func (r *SessionRepository) ListEntries(ctx context.Context, sessionID string) ([]domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	if _, err := r.Get(ctx, sessionID); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(directory, sessionEntriesFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []domain.SessionEntry{}, nil
		}
		return nil, fmt.Errorf("open session transcript: %w", err)
	}
	defer file.Close()
	entries := []domain.SessionEntry{}
	if err := readJSONLines(ctx, file, func(line []byte) error {
		if len(line) == 0 {
			return nil
		}
		var entry domain.SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("decode session entry: %w", err)
		}
		entry = withoutPersistedReasoning(entry)
		entries = append(entries, entry)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan session transcript: %w", err)
	}
	return entries, nil
}

// withoutPersistedReasoning 將思考內容限制在目前 Run 的記憶體生命週期內。
// 寫入端負責避免新增資料落盤，讀取端則兼容清除功能上線前已存在的 transcript，
// 確保歷史 API、Context 組裝與匯出都不會再次暴露舊的 reasoning。
func withoutPersistedReasoning(entry domain.SessionEntry) domain.SessionEntry {
	if entry.Message == nil || !strings.EqualFold(entry.Message.Role, "assistant") {
		return entry
	}
	message := *entry.Message
	message.Reasoning = ""
	entry.Message = &message
	return entry
}

// ListRecentMessages 只取尾端最多 limit 則訊息。
//
// 「最近幾則使用者說了什麼」是很常見的需求（例如工具檢索的查詢字串），但
// ListMessages 為此要把整份 transcript 解碼一遍：實測 3.6 MB 的對話要 13–18 ms、
// 解出 467 則訊息，而呼叫端只用到最後三則，且每個 Run 都付一次。
//
// 撤回記錄讓這件事不能只是「從尾端讀」：範圍內的撤回若指向範圍外的訊息，
// 截斷結果會與完整掃描不同。遇到這種情況就退回 ListMessages，
// 寧可慢一次也不要回傳與稽核紀錄不一致的內容。
func (r *SessionRepository) ListRecentMessages(ctx context.Context, sessionID string, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		return r.ListMessages(ctx, sessionID)
	}
	directory, err := r.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	if _, err := r.Get(ctx, sessionID); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, sessionEntriesFile)
	index, indexErr := r.entryIndexFor(ctx, sessionID, path)
	if indexErr != nil {
		return r.ListMessages(ctx, sessionID)
	}
	r.mu.Lock()
	offset, complete := index.offsetOfRecentType(domain.SessionEntryMessage, limit)
	r.mu.Unlock()
	if complete {
		return r.ListMessages(ctx, sessionID)
	}
	messages := []domain.Message{}
	truncated := false
	if err := r.readEntriesFrom(ctx, path, offset, func(entry domain.SessionEntry) {
		if from := domain.RetractedFromMessageID(entry); from != "" {
			index := indexOfMessageID(messages, from)
			if index < 0 {
				// 撤回指向這個範圍之外的訊息，這裡算不出正確的截斷點。
				truncated = true
				return
			}
			messages = messages[:index]
			return
		}
		if entry.Type != domain.SessionEntryMessage || entry.Message == nil {
			return
		}
		messages = append(messages, *entry.Message)
	}); err != nil {
		return nil, err
	}
	if truncated {
		return r.ListMessages(ctx, sessionID)
	}
	return messages, nil
}

// readEntriesFrom 從指定位移解碼到檔案結尾。
func (r *SessionRepository) readEntriesFrom(ctx context.Context, path string, offset int64, visit func(domain.SessionEntry)) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open session transcript: %w", err)
	}
	defer file.Close()
	if err := readJSONLinesAt(ctx, file, offset, func(_ int64, line []byte) error {
		var entry domain.SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("decode session entry: %w", err)
		}
		visit(withoutPersistedReasoning(entry))
		return nil
	}); err != nil {
		return fmt.Errorf("read session transcript: %w", err)
	}
	return nil
}

func (r *SessionRepository) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	entries, err := r.ListEntries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.Message, 0, len(entries))
	for _, entry := range entries {
		// 撤回記錄把訊息串截到指定訊息之前：重新提問時，上一次的問題、回答與
		// 工具過程都不再屬於這個對話，但原始 entry 仍留在 transcript 供稽核。
		if from := domain.RetractedFromMessageID(entry); from != "" {
			if index := indexOfMessageID(messages, from); index >= 0 {
				messages = messages[:index]
			}
			continue
		}
		if entry.Type != domain.SessionEntryMessage || entry.Message == nil {
			continue
		}
		messages = append(messages, *entry.Message)
	}
	return messages, nil
}

func indexOfMessageID(messages []domain.Message, messageID string) int {
	for index, message := range messages {
		if message.ID == messageID {
			return index
		}
	}
	return -1
}

func (r *SessionRepository) sessionDir(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", fmt.Errorf("%w: invalid session id", domain.ErrInvalidInput)
	}
	return filepath.Join(r.rootFor(sessionID), sessionID), nil
}

func (r *SessionRepository) sessionLock(sessionID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock := r.locks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[sessionID] = lock
	}
	return lock
}

func (r *SessionRepository) nextSequenceLocked(sessionID string, entriesPath string) (int64, error) {
	r.mu.Lock()
	sequence, exists := r.sequences[sessionID]
	r.mu.Unlock()
	if exists {
		return sequence + 1, nil
	}
	// 快取沒命中（程序剛啟動後第一次 append）時用索引取號。
	//
	// 原本是把每一行完整 unmarshal 一次來找最大序號：那是整份 transcript 最貴的
	// 一種讀法（21 MB 約 74 ms），而需要的只是最後一筆的序號。
	if index, indexErr := r.entryIndexFor(context.Background(), sessionID, entriesPath); indexErr == nil {
		r.mu.Lock()
		last := index.lastSequence()
		r.mu.Unlock()
		return last + 1, nil
	}
	file, err := os.Open(entriesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 1, nil
		}
		return 0, err
	}
	defer file.Close()
	last := int64(0)
	if err := readJSONLines(context.Background(), file, func(line []byte) error {
		var entry domain.SessionEntry
		if json.Unmarshal(line, &entry) == nil && entry.Sequence > last {
			last = entry.Sequence
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return last + 1, nil
}

// readJSONLines 不使用 bufio.Scanner 的固定 token 上限。Tool arguments 在 Provider
// 解碼後重新 JSON 編碼，跳脫字元可能讓單筆 entry 比原始模型事件大數倍；固定 8 MiB
// 上限會讓一筆合法 entry 毒化整個 append-only transcript。
func readJSONLines(ctx context.Context, reader io.Reader, visit func([]byte) error) error {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := buffered.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if visitErr := visit(line); visitErr != nil {
				return visitErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (r *SessionRepository) writeSessionLocked(session domain.Session) error {
	directory, err := r.sessionDir(session.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	target := filepath.Join(directory, sessionMetadataFile)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	if err := replaceFile(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}
