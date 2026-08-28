package filestore

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newTestRepository(t *testing.T) (*SessionRepository, domain.Session) {
	t.Helper()
	repository, err := NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionRepository: %v", err)
	}
	session, err := repository.Create(context.Background(), "agent_test", domain.CreateSessionInput{
		Title:       "test",
		WorkspaceID: "workspace_1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return repository, session
}

func TestCreateAssignsSessionWorkspaceRoot(t *testing.T) {
	_, session := newTestRepository(t)

	root, _ := session.Metadata["workspace_root"].(string)
	if root == "" {
		t.Fatal("session workspace root was not recorded")
	}
}

// TestCreateOverridesClientSuppliedWorkspaceRoot 保護沙箱根目錄：
// workspace_root 決定所有檔案工具的邊界，不能由建立 Session 的 request 決定。
func TestCreateOverridesClientSuppliedWorkspaceRoot(t *testing.T) {
	repository, err := NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionRepository: %v", err)
	}

	session, err := repository.Create(context.Background(), "agent_test", domain.CreateSessionInput{
		WorkspaceID: "workspace_1",
		Metadata:    map[string]any{"workspace_root": "/"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if root, _ := session.Metadata["workspace_root"].(string); root == "/" {
		t.Fatal("client-supplied workspace_root was honoured; the tool sandbox root must come from the backend")
	}
}

func TestAppendEntryAssignsMonotonicSequences(t *testing.T) {
	repository, session := newTestRepository(t)
	ctx := context.Background()

	for index := 0; index < 5; index++ {
		entry, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
		if err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
		if entry.Sequence != int64(index+1) {
			t.Fatalf("sequence = %d, want %d", entry.Sequence, index+1)
		}
	}
}

func TestAppendEntryIsSafeUnderConcurrency(t *testing.T) {
	repository, session := newTestRepository(t)
	ctx := context.Background()
	const writers = 24

	var group sync.WaitGroup
	sequences := make(chan int64, writers)
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			entry, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
			if err != nil {
				t.Errorf("AppendEntry: %v", err)
				return
			}
			sequences <- entry.Sequence
		}()
	}
	group.Wait()
	close(sequences)

	seen := map[int64]bool{}
	for sequence := range sequences {
		if seen[sequence] {
			t.Fatalf("sequence %d was handed out twice", sequence)
		}
		seen[sequence] = true
	}
	if len(seen) != writers {
		t.Fatalf("got %d distinct sequences, want %d", len(seen), writers)
	}

	entries, err := repository.ListEntries(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != writers {
		t.Fatalf("persisted %d entries, want %d", len(entries), writers)
	}
}

// TestSequenceSurvivesRepositoryRestart 覆蓋重啟：序號來自檔案而不是只有記憶體計數。
func TestSequenceSurvivesRepositoryRestart(t *testing.T) {
	directory := t.TempDir()
	first, err := NewSessionRepository(directory)
	if err != nil {
		t.Fatalf("NewSessionRepository: %v", err)
	}
	session, err := first.Create(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for index := 0; index < 3; index++ {
		if _, err := first.AppendEntry(context.Background(), session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted}); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
	}

	second, err := NewSessionRepository(directory)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	entry, err := second.AppendEntry(context.Background(), session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if entry.Sequence != 4 {
		t.Fatalf("sequence = %d, want 4 after restart", entry.Sequence)
	}
}

func TestAppendEntryRefusesCanceledContext(t *testing.T) {
	repository, session := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted}); err == nil {
		t.Fatal("AppendEntry accepted a canceled context")
	}
}

func TestSessionDirectoryRejectsTraversalIdentifiers(t *testing.T) {
	repository, _ := newTestRepository(t)

	for _, id := range []string{"..", "../escape", "a/b", ""} {
		if _, err := repository.Get(context.Background(), id); err == nil {
			t.Errorf("Get(%q) succeeded; want a rejection", id)
		}
	}
}

func TestGetReportsNotFoundForUnknownSession(t *testing.T) {
	repository, _ := newTestRepository(t)

	if _, err := repository.Get(context.Background(), "session_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListMessagesReturnsOnlyMessageEntries(t *testing.T) {
	repository, session := newTestRepository(t)
	ctx := context.Background()
	message := domain.Message{ID: "msg_1", Role: "user", Content: "hi"}
	if _, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryMessage, Message: &message}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if _, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	messages, err := repository.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "hi" {
		t.Fatalf("messages = %+v, want only the user message", messages)
	}
}

func appendMessageEntry(t *testing.T, repository *SessionRepository, sessionID, content string) domain.SessionEntry {
	t.Helper()
	message := domain.Message{ID: domain.NewID("msg"), Role: "tool", Content: content}
	entry, err := repository.AppendEntry(context.Background(), sessionID, domain.SessionEntry{
		Type:    domain.SessionEntryMessage,
		Message: &message,
	})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	return entry
}

func TestListEntriesAfterSkipsEarlierEntries(t *testing.T) {
	repository, session := newTestRepository(t)
	appendMessageEntry(t, repository, session.ID, "first")
	cut := appendMessageEntry(t, repository, session.ID, "second")
	appendMessageEntry(t, repository, session.ID, "third")

	entries, err := repository.ListEntriesAfter(context.Background(), session.ID, cut.Sequence)
	if err != nil {
		t.Fatalf("ListEntriesAfter: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Message.Content != "third" {
		t.Fatalf("content = %q, want third", entries[0].Message.Content)
	}
}

func TestListEntriesAfterZeroReturnsEverything(t *testing.T) {
	repository, session := newTestRepository(t)
	appendMessageEntry(t, repository, session.ID, "a")
	appendMessageEntry(t, repository, session.ID, "b")

	entries, err := repository.ListEntriesAfter(context.Background(), session.ID, 0)
	if err != nil {
		t.Fatalf("ListEntriesAfter: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}

func TestLatestEntryOfTypeReturnsHighestSequence(t *testing.T) {
	repository, session := newTestRepository(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		if _, err := repository.AppendEntry(ctx, session.ID, domain.SessionEntry{
			Type: domain.SessionEntryCompaction,
			Data: map[string]any{"summary": fmt.Sprintf("summary %d", index), "through_sequence": index},
		}); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
		appendMessageEntry(t, repository, session.ID, "noise")
	}

	entry, err := repository.LatestEntryOfType(ctx, session.ID, domain.SessionEntryCompaction)
	if err != nil {
		t.Fatalf("LatestEntryOfType: %v", err)
	}
	if entry.Data["summary"] != "summary 2" {
		t.Fatalf("summary = %v, want the newest compaction", entry.Data["summary"])
	}
}

func TestLatestEntryOfTypeReportsNotFound(t *testing.T) {
	repository, session := newTestRepository(t)
	appendMessageEntry(t, repository, session.ID, "a")

	if _, err := repository.LatestEntryOfType(context.Background(), session.ID, domain.SessionEntryCompaction); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestLargeTranscriptEntryRemainsReadableAfterRestart 防止單一大型 tool arguments/result
// 超過 bufio.Scanner 固定上限後，整個 Session 從此無法讀取或繼續追加。
func TestLargeTranscriptEntryRemainsReadableAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	first, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("NewSessionRepository: %v", err)
	}
	session, err := first.Create(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	large := strings.Repeat("參數", 3*1024*1024) // UTF-8 與 JSON 後明確超過原本 8 MiB Scanner 上限。
	if _, err := first.AppendEntry(context.Background(), session.ID, domain.SessionEntry{
		Type: domain.SessionEntryToolStarted,
		Data: map[string]any{"arguments": map[string]any{"content": large}},
	}); err != nil {
		t.Fatalf("AppendEntry(large): %v", err)
	}

	second, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	entries, err := second.ListEntries(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListEntries after restart: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry, err := second.AppendEntry(context.Background(), session.ID, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
	if err != nil {
		t.Fatalf("AppendEntry after large entry: %v", err)
	}
	if entry.Sequence != 2 {
		t.Fatalf("sequence = %d, want 2", entry.Sequence)
	}
}

// TestUpdatedAtTracksTranscriptWithoutRewritingSession 保護 append 路徑的成本：
// UpdatedAt 不再靠每筆 entry 重寫 session.json 維持。
func TestUpdatedAtTracksTranscriptWithoutRewritingSession(t *testing.T) {
	repository, session := newTestRepository(t)
	before := session.UpdatedAt

	appendMessageEntry(t, repository, session.ID, "activity")

	reloaded, err := repository.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.UpdatedAt.Before(before) {
		t.Fatalf("updated_at went backwards: %v < %v", reloaded.UpdatedAt, before)
	}
}

func benchmarkTranscript(b *testing.B, entries int) (*SessionRepository, domain.Session, int64) {
	b.Helper()
	repository, err := NewSessionRepository(b.TempDir())
	if err != nil {
		b.Fatalf("NewSessionRepository: %v", err)
	}
	session, err := repository.Create(context.Background(), "agent_test", domain.CreateSessionInput{WorkspaceID: "workspace_1"})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	// 大型工具輸出佔了 transcript 絕大部分體積，也是重複解碼的主要成本。
	payload := strings.Repeat("工具輸出內容 ", 2_000)
	cut := int64(0)
	for index := 0; index < entries; index++ {
		message := domain.Message{ID: domain.NewID("msg"), Role: "tool", Content: payload}
		entry, err := repository.AppendEntry(context.Background(), session.ID, domain.SessionEntry{
			Type:    domain.SessionEntryMessage,
			Message: &message,
		})
		if err != nil {
			b.Fatalf("AppendEntry: %v", err)
		}
		if index == entries-5 {
			cut = entry.Sequence
		}
	}
	return repository, session, cut
}

func BenchmarkListEntriesFullTranscript(b *testing.B) {
	repository, session, _ := benchmarkTranscript(b, 200)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := repository.ListEntries(context.Background(), session.ID); err != nil {
			b.Fatalf("ListEntries: %v", err)
		}
	}
}

func BenchmarkListEntriesAfterCompaction(b *testing.B) {
	repository, session, cut := benchmarkTranscript(b, 200)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := repository.ListEntriesAfter(context.Background(), session.ID, cut); err != nil {
			b.Fatalf("ListEntriesAfter: %v", err)
		}
	}
}
