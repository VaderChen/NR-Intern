package filestore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

// stubRoots 依 ID 前綴決定根目錄，模擬「歸屬編碼在 session ID 裡」。
type stubRoots struct {
	prefix string
	root   string
}

func (s stubRoots) RootFor(sessionID string) string {
	if strings.HasPrefix(sessionID, s.prefix) {
		return s.root
	}
	return ""
}

func (s stubRoots) AdditionalRoots() []string { return []string{s.root} }

// createIn 用指定 ID 建立 session；正式實作會由 ID 工廠產生帶歸屬的 ID，
// 這裡直接指定以便單獨驗證根目錄解析。
func createIn(t *testing.T, repository *SessionRepository, sessionID string) domain.Session {
	t.Helper()
	directory, err := repository.sessionDir(sessionID)
	if err != nil {
		t.Fatalf("session dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "workspace"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	session := domain.Session{ID: sessionID, AgentID: "agent", Title: "t"}
	if err := repository.writeSessionLocked(session); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return session
}

func twoRootRepository(t *testing.T) (*SessionRepository, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	volatileRoot := t.TempDir()
	repository, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	repository.SetProjectRoots(stubRoots{prefix: "session_e", root: volatileRoot})
	return repository, filepath.Join(dataDir, "sessions"), volatileRoot
}

// 同一個 repository 實例要能同時讀寫兩個根目錄下的 session。
func TestProjectRootsPlaceDirectoriesInResolvedRoot(t *testing.T) {
	repository, defaultRoot, volatileRoot := twoRootRepository(t)
	ctx := context.Background()

	normal := createIn(t, repository, "session_normal01")
	volatile := createIn(t, repository, "session_e_proj01")

	if _, err := os.Stat(filepath.Join(defaultRoot, normal.ID)); err != nil {
		t.Fatalf("一般對話應落在預設根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(volatileRoot, volatile.ID)); err != nil {
		t.Fatalf("隔離對話應落在解析出的根：%v", err)
	}
	// 關鍵：隔離對話絕不能同時出現在預設根。
	if _, err := os.Stat(filepath.Join(defaultRoot, volatile.ID)); err == nil {
		t.Fatal("隔離對話不該出現在預設根")
	}

	for _, session := range []domain.Session{normal, volatile} {
		if _, err := repository.Get(ctx, session.ID); err != nil {
			t.Fatalf("讀取 %s：%v", session.ID, err)
		}
	}
}

// transcript 的讀寫路徑在兩個根下必須行為一致。
func TestProjectRootsKeepTranscriptOperationsConsistent(t *testing.T) {
	repository, _, _ := twoRootRepository(t)
	ctx := context.Background()
	for _, sessionID := range []string{"session_normal02", "session_e_proj02"} {
		createIn(t, repository, sessionID)
		for index := 0; index < 12; index++ {
			message := domain.Message{ID: sessionID + "-m", Role: "user", Content: "內容"}
			entryType := domain.SessionEntryMessage
			if index == 5 {
				entryType = domain.SessionEntryCompaction
			}
			entry := domain.SessionEntry{Type: entryType, Message: &message}
			if entryType == domain.SessionEntryCompaction {
				entry.Message = nil
				entry.Data = map[string]any{"summary": "摘要"}
			}
			if _, err := repository.AppendEntry(ctx, sessionID, entry); err != nil {
				t.Fatalf("%s append %d: %v", sessionID, index, err)
			}
		}
		page, hasMore, err := repository.ListEntriesPage(ctx, sessionID, 0, 5)
		if err != nil || len(page) != 5 || !hasMore {
			t.Fatalf("%s 分頁異常：%d 筆 hasMore=%v err=%v", sessionID, len(page), hasMore, err)
		}
		if _, err := repository.LatestEntryOfType(ctx, sessionID, domain.SessionEntryCompaction); err != nil {
			t.Fatalf("%s 找不到壓縮記錄：%v", sessionID, err)
		}
		recent, err := repository.ListRecentMessages(ctx, sessionID, 3)
		if err != nil || len(recent) == 0 {
			t.Fatalf("%s 尾端讀取異常：%d 則 err=%v", sessionID, len(recent), err)
		}
	}
}

// List 必須涵蓋所有根，否則側邊欄會看不到隔離專案的對話。
func TestProjectRootsListSpansEveryRoot(t *testing.T) {
	repository, _, _ := twoRootRepository(t)
	ctx := context.Background()
	createIn(t, repository, "session_normal03")
	createIn(t, repository, "session_e_proj03")

	sessions, err := repository.List(ctx, "agent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := map[string]bool{}
	for _, session := range sessions {
		found[session.ID] = true
	}
	for _, want := range []string{"session_normal03", "session_e_proj03"} {
		if !found[want] {
			t.Fatalf("List 少了 %s：%v", want, found)
		}
	}
}

// 額外的根尚未掛載時（RAM disk 還沒建立）不該讓整份清單失敗。
func TestProjectRootsToleratesMissingAdditionalRoot(t *testing.T) {
	dataDir := t.TempDir()
	repository, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	repository.SetProjectRoots(stubRoots{prefix: "session_e", root: filepath.Join(dataDir, "not-mounted")})
	createIn(t, repository, "session_normal04")

	sessions, err := repository.List(context.Background(), "agent")
	if err != nil {
		t.Fatalf("額外的根不存在時仍應成功：%v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session_normal04" {
		t.Fatalf("清單內容不正確：%d 筆", len(sessions))
	}
}

// 沒有注入 ProjectRoots 時，行為必須與改動前完全相同。
func TestProjectRootsDefaultToSingleRoot(t *testing.T) {
	dataDir := t.TempDir()
	repository, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	session, err := repository.Create(context.Background(), "agent", domain.CreateSessionInput{Title: "t"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "sessions", session.ID)); err != nil {
		t.Fatalf("未注入時應落在預設根：%v", err)
	}
	repository.SetProjectRoots(nil)
	if _, err := repository.Get(context.Background(), session.ID); err != nil {
		t.Fatalf("傳入 nil 應回到單一根行為：%v", err)
	}
}
