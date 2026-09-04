package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"AgenticService/src/domain"
)

// ListRecentMessages 的成本應該由 limit 決定，而不是由對話長度決定。
//
// 這是這個方法存在的理由：retrievalQuery 每個 Run 都會呼叫一次，如果成本隨
// transcript 成長，長對話就會愈用愈慢——那正是先前用 ListMessages 的問題。
//
// 用「讀取起點的位移」而不是計時來驗證：它是這個性質的直接來源，不受機器
// 效能與快取影響，退化成整份掃描時位移會掉回 0，一眼就看得出來。
func TestListRecentMessagesReadsOnlyTheTail(t *testing.T) {
	for _, total := range []int{100, 500} {
		repository, sessionID := newSeededSession(t, total)
		ctx := context.Background()
		directory, err := repository.sessionDir(sessionID)
		if err != nil {
			t.Fatalf("session dir: %v", err)
		}
		path := filepath.Join(directory, sessionEntriesFile)
		index, err := repository.entryIndexFor(ctx, sessionID, path)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		offset, complete := index.offsetOfRecentType(domain.SessionEntryMessage, 50)
		if complete {
			t.Fatalf("%d 則時不該判定為需要完整掃描", total)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		// 只讀尾端 50 則，跳過的比例應該隨 transcript 變長而提高。
		skipped := float64(offset) / float64(info.Size())
		if skipped < 0.4 {
			t.Fatalf("%d 則時只跳過 %.0f%%，讀取範圍沒有隨長度收斂", total, skipped*100)
		}
		t.Logf("%4d 則：跳過檔案前 %.0f%%（位移 %d／%d bytes）", total, skipped*100, offset, info.Size())
	}
}
