package filestore

import (
	"AgenticService/src/domain"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func sessionIndexFixture(t *testing.T, count int) (*SessionRepository, string, string, []byte) {
	t.Helper()
	r, session := newTestRepository(t)
	for i := 0; i < count; i++ {
		_, err := r.AppendEntry(context.Background(), session.ID, domain.SessionEntry{
			Type:    domain.SessionEntryMessage,
			Message: &domain.Message{ID: domain.NewID("msg"), Role: "user", Content: "synthetic"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	directory, err := r.sessionDir(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, sessionEntriesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return r, session.ID, path, data
}

func TestSessionIndexReplacementSmoke(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-size-rewrite", true: "same-size-replacement"}[replace], func(t *testing.T) {
			r, id, path, data := sessionIndexFixture(t, 1)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := bytes.ReplaceAll(data, []byte(`"type":"message"`), []byte(`"type":"changed"`))
			changed = bytes.ReplaceAll(changed, []byte(`"sequence":1`), []byte(`"sequence":9`))
			if bytes.Equal(changed, data) || len(changed) != len(data) {
				t.Fatal("fixture must change without changing file size")
			}
			target := path
			stamp := before.ModTime().Add(2 * time.Second)
			if replace {
				target = filepath.Join(filepath.Dir(path), "replacement.jsonl")
				stamp = before.ModTime() // 同大小、同時間的替換仍須驗證 inode。
			}
			if err := os.WriteFile(target, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(target, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			if replace {
				if err := os.Rename(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.LatestEntryOfType(context.Background(), id, domain.SessionEntryMessage); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("stale type lookup: %v", err)
			}
			entry, err := r.AppendEntry(context.Background(), id, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
			if err != nil || entry.Sequence != 10 {
				t.Fatalf("append after replacement = %d, %v", entry.Sequence, err)
			}
		})
	}
}

func TestSessionIndexTruncationAndAppendBoundarySmoke(t *testing.T) {
	r, id, path, data := sessionIndexFixture(t, 3)
	first := data[:bytes.IndexByte(data, '\n')] // 合法尾筆，刻意不留換行。
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, more, err := r.ListEntriesPage(context.Background(), id, 0, 1)
	if err != nil || more || len(entries) != 1 {
		t.Fatalf("truncated page: count=%d more=%v err=%v", len(entries), more, err)
	}
	entry, err := r.AppendEntry(context.Background(), id, domain.SessionEntry{Type: domain.SessionEntryTurnStarted})
	if err != nil || entry.Sequence != 2 {
		t.Fatalf("append after truncation = %d, %v", entry.Sequence, err)
	}
	entries, err = r.ListEntries(context.Background(), id)
	if err != nil || len(entries) != 2 {
		t.Fatalf("new line boundary: count=%d err=%v", len(entries), err)
	}
	latest, err := r.LatestEntryOfType(context.Background(), id, domain.SessionEntryTurnStarted)
	if err != nil || latest.Sequence != 2 {
		t.Fatalf("new index offset: %d, %v", latest.Sequence, err)
	}
}

func TestSessionAppendRefusesIncompleteTailSmoke(t *testing.T) {
	r, id, path, _ := sessionIndexFixture(t, 1)
	broken := []byte(`{"sequence":8,"type":"message","message":`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AppendEntry(context.Background(), id, domain.SessionEntry{Type: domain.SessionEntryTurnStarted}); err == nil {
		t.Fatal("append must not silently extend a broken transcript")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, broken) {
		t.Fatalf("broken transcript was modified: %v", err)
	}
}

func TestSessionIndexConcurrentReadersAndAppendSmoke(t *testing.T) {
	r, id, _, _ := sessionIndexFixture(t, 4)
	ctx := context.Background()
	var group sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for i := 0; i < 24; i++ {
				var err error
				switch worker {
				case 0:
					_, err = r.AppendEntry(ctx, id, domain.SessionEntry{Type: domain.SessionEntryMessage, Message: &domain.Message{Role: "user", Content: "synthetic"}})
				case 1:
					_, _, err = r.ListEntriesPage(ctx, id, 0, 2)
				case 2:
					_, err = r.ListRecentMessages(ctx, id, 3)
				case 3:
					_, err = r.LatestEntryOfType(ctx, id, domain.SessionEntryMessage)
				}
				if err != nil {
					t.Errorf("worker %d: %v", worker, err)
					return
				}
			}
		}(worker)
	}
	group.Wait()
	entries, err := r.ListEntries(ctx, id)
	if err != nil || len(entries) != 28 {
		t.Fatalf("final transcript count=%d err=%v", len(entries), err)
	}
	for i, entry := range entries {
		if entry.Sequence != int64(i+1) {
			t.Fatalf("sequence[%d] = %d", i, entry.Sequence)
		}
	}
}
