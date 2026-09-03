package filestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

func seededSession(t *testing.T, entries int) (*SessionRepository, string) {
	t.Helper()
	repository, err := NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new session repository: %v", err)
	}
	session, err := repository.Create(context.Background(), "agent-1", domain.CreateSessionInput{Title: "index"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for index := 0; index < entries; index++ {
		entryType := domain.SessionEntryMessage
		if index%10 == 9 {
			entryType = domain.SessionEntryCompaction
		}
		if _, err := repository.AppendEntry(context.Background(), session.ID, domain.SessionEntry{
			Type: entryType,
			Data: map[string]any{"index": index, "filler": strings.Repeat("x", 200)},
		}); err != nil {
			t.Fatalf("append entry %d: %v", index, err)
		}
	}
	return repository, session.ID
}

// 分頁必須在儲存層生效，否則翻頁就是把整份 transcript 重讀一次。
func TestListEntriesPagePagesInOrder(t *testing.T) {
	repository, sessionID := seededSession(t, 25)
	ctx := context.Background()
	after := int64(0)
	seen := 0
	pages := 0
	for {
		page, hasMore, err := repository.ListEntriesPage(ctx, sessionID, after, 10)
		if err != nil {
			t.Fatalf("page after %d: %v", after, err)
		}
		pages++
		for _, entry := range page {
			seen++
			if entry.Sequence != int64(seen) {
				t.Fatalf("entry %d has sequence %d, want %d", seen, entry.Sequence, seen)
			}
		}
		if !hasMore {
			break
		}
		if len(page) == 0 {
			t.Fatal("has_more was true but the page is empty")
		}
		after = page[len(page)-1].Sequence
	}
	if seen != 25 || pages != 3 {
		t.Fatalf("read %d entries over %d pages, want 25 over 3", seen, pages)
	}
}

func TestListEntriesPageRespectsLimit(t *testing.T) {
	repository, sessionID := seededSession(t, 12)
	page, hasMore, err := repository.ListEntriesPage(context.Background(), sessionID, 0, 5)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(page) != 5 || !hasMore {
		t.Fatalf("got %d entries hasMore=%v, want 5 and true", len(page), hasMore)
	}
	rest, hasMore, err := repository.ListEntriesPage(context.Background(), sessionID, page[4].Sequence, 100)
	if err != nil {
		t.Fatalf("list rest: %v", err)
	}
	if len(rest) != 7 || hasMore {
		t.Fatalf("got %d entries hasMore=%v, want 7 and false", len(rest), hasMore)
	}
}

// 索引是效能結構，不能改變任何一個既有讀取路徑的結果。
func TestIndexedReadsMatchFullScan(t *testing.T) {
	repository, sessionID := seededSession(t, 30)
	ctx := context.Background()
	all, err := repository.ListEntries(ctx, sessionID)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(all) != 30 {
		t.Fatalf("list entries returned %d, want 30", len(all))
	}
	after, err := repository.ListEntriesAfter(ctx, sessionID, 20)
	if err != nil {
		t.Fatalf("list entries after: %v", err)
	}
	if len(after) != 10 || after[0].Sequence != 21 {
		t.Fatalf("after 20 returned %d entries starting at %d, want 10 starting at 21", len(after), after[0].Sequence)
	}
	latest, err := repository.LatestEntryOfType(ctx, sessionID, domain.SessionEntryCompaction)
	if err != nil {
		t.Fatalf("latest compaction: %v", err)
	}
	if latest.Sequence != 30 {
		t.Fatalf("latest compaction sequence = %d, want 30", latest.Sequence)
	}
	if _, err := repository.LatestEntryOfType(ctx, sessionID, "never_written"); err == nil {
		t.Fatal("expected ErrNotFound for a type that was never written")
	}
}

// 索引是記憶體結構：程序重啟後必須自己重建，而且結果要跟寫入當下一致。
func TestIndexRebuildsForANewProcess(t *testing.T) {
	repository, sessionID := seededSession(t, 15)
	root := strings.TrimSuffix(repository.root, string(filepath.Separator)+"sessions")

	reopened, err := NewSessionRepository(root)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	after, err := reopened.ListEntriesAfter(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("list entries after on a fresh repository: %v", err)
	}
	if len(after) != 5 || after[0].Sequence != 11 {
		t.Fatalf("got %d entries starting at %d, want 5 starting at 11", len(after), after[0].Sequence)
	}
	// 重啟後第一次 append 也不能重複序號。
	entry, err := reopened.AppendEntry(context.Background(), sessionID, domain.SessionEntry{Type: domain.SessionEntryMessage})
	if err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if entry.Sequence != 16 {
		t.Fatalf("appended sequence = %d, want 16", entry.Sequence)
	}
}

// 外部改動過 transcript 時索引必須失效重建，不能拿舊位移去 seek。
func TestIndexRebuildsAfterExternalAppend(t *testing.T) {
	repository, sessionID := seededSession(t, 5)
	ctx := context.Background()
	if _, err := repository.ListEntriesAfter(ctx, sessionID, 0); err != nil {
		t.Fatalf("prime the index: %v", err)
	}
	path := filepath.Join(repository.root, sessionID, sessionEntriesFile)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := fmt.Fprintf(file, `{"id":"entry_x","session_id":%q,"sequence":6,"type":"message","created_at":"2026-01-01T00:00:00Z"}`+"\n", sessionID); err != nil {
		t.Fatalf("external append: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
	after, err := repository.ListEntriesAfter(ctx, sessionID, 5)
	if err != nil {
		t.Fatalf("list after external append: %v", err)
	}
	if len(after) != 1 || after[0].Sequence != 6 {
		t.Fatalf("got %d entries, want the externally appended entry 6", len(after))
	}
}
