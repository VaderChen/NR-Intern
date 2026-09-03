package filestore

import (
	"context"
	"os"
	"testing"
	"time"

	"AgenticService/src/domain"
)

// 量測真實 transcript 的讀取成本。沒有設環境變數時跳過，CI 不受影響。
//
// 保留這個測試是因為「長 session 變慢」不會在功能測試裡出現：行為完全正確，
// 只是越來越慢。要驗證索引有沒有退化，得對著真的很大的檔案量。
func TestTranscriptReadCost(t *testing.T) {
	dataDir := os.Getenv("SCAN_DATA_DIR")
	sessionID := os.Getenv("SCAN_SESSION_ID")
	if dataDir == "" || sessionID == "" {
		t.Skip("set SCAN_DATA_DIR and SCAN_SESSION_ID to measure a real transcript")
	}
	repository, err := NewSessionRepository(dataDir)
	if err != nil {
		t.Fatalf("new session repository: %v", err)
	}
	ctx := context.Background()

	start := time.Now()
	if _, _, err := repository.ListEntriesPage(ctx, sessionID, 0, 1000); err != nil {
		t.Fatalf("first page: %v", err)
	}
	firstPage := time.Since(start)

	pages, entries := 0, 0
	after := int64(0)
	start = time.Now()
	for {
		page, hasMore, err := repository.ListEntriesPage(ctx, sessionID, after, 1000)
		if err != nil {
			t.Fatalf("page after %d: %v", after, err)
		}
		pages++
		entries += len(page)
		if !hasMore || len(page) == 0 {
			break
		}
		after = page[len(page)-1].Sequence
	}
	allPages := time.Since(start)

	start = time.Now()
	tail, err := repository.ListEntriesAfter(ctx, sessionID, 1<<60)
	tailCost := time.Since(start)
	if err != nil {
		t.Fatalf("list entries after: %v", err)
	}

	start = time.Now()
	_, latestErr := repository.LatestEntryOfType(ctx, sessionID, domain.SessionEntryCompaction)
	latestCost := time.Since(start)

	t.Logf("首頁（含建索引）：%v", firstPage)
	t.Logf("翻完 %d 頁共 %d 筆：%v", pages, entries, allPages)
	t.Logf("ListEntriesAfter（尾端無資料）：%v，回傳 %d 筆", tailCost, len(tail))
	t.Logf("LatestEntryOfType：%v (err=%v)", latestCost, latestErr)
}
