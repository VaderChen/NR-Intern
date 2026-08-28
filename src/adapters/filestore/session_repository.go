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
	"time"
)

const (
	sessionMetadataFile = "session.json"
	sessionEntriesFile  = "entries.jsonl"
)

type SessionRepository struct {
	root string

	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	sequences map[string]int64
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
	localNow := time.Now()
	now := localNow.UTC()
	sessionID := domain.NewID("session")
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
		WorkspaceID:       strings.TrimSpace(input.WorkspaceID),
		ProjectID:         strings.TrimSpace(input.ProjectID),
		Title:             valueutil.FirstNonEmpty(input.Title, localNow.Format("01/02 15:04")),
		ProviderID:        strings.TrimSpace(input.ProviderID),
		Model:             strings.TrimSpace(input.Model),
		PermissionProfile: permissionProfile,
		Pinned:            input.Pinned,
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
	items, err := os.ReadDir(r.root)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	sessions := make([]domain.Session, 0, len(items))
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		session, readErr := r.Get(ctx, item.Name())
		if readErr != nil {
			continue
		}
		if strings.TrimSpace(agentID) != "" && session.AgentID != strings.TrimSpace(agentID) {
			continue
		}
		sessions = append(sessions, session)
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
	data, err := json.Marshal(entry)
	if err != nil {
		return domain.SessionEntry{}, fmt.Errorf("encode session entry: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, sessionEntriesFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
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
		if err := visit(entry); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan session transcript: %w", err)
	}
	return nil
}

func (r *SessionRepository) ListEntriesAfter(ctx context.Context, sessionID string, afterSequence int64) ([]domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := []domain.SessionEntry{}
	err := r.scanEntries(ctx, sessionID,
		func(header entryHeader) bool { return header.Sequence > afterSequence },
		func(entry domain.SessionEntry) error {
			entries = append(entries, entry)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *SessionRepository) LatestEntryOfType(ctx context.Context, sessionID, entryType string) (domain.SessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionEntry{}, err
	}
	entryType = strings.TrimSpace(entryType)
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
		entries = append(entries, entry)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan session transcript: %w", err)
	}
	return entries, nil
}

func (r *SessionRepository) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	entries, err := r.ListEntries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.Message, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		messages = append(messages, *entry.Message)
	}
	return messages, nil
}

func (r *SessionRepository) sessionDir(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", fmt.Errorf("%w: invalid session id", domain.ErrInvalidInput)
	}
	return filepath.Join(r.root, sessionID), nil
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
