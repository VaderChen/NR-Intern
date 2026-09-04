package filestore

import (
	"context"
	"fmt"
	"testing"

	"AgenticService/src/domain"
)

func seedMessages(t *testing.T, repository *SessionRepository, sessionID string, count int) []domain.Message {
	t.Helper()
	created := make([]domain.Message, 0, count)
	for index := 0; index < count; index++ {
		role := "assistant"
		if index%4 == 0 {
			role = "user"
		}
		message := domain.Message{
			ID:      fmt.Sprintf("msg_%03d", index),
			Role:    role,
			Content: fmt.Sprintf("第 %d 則", index),
		}
		if _, err := repository.AppendEntry(context.Background(), sessionID, domain.SessionEntry{
			Type: domain.SessionEntryMessage, Message: &message,
		}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
		created = append(created, message)
	}
	return created
}

func newSeededSession(t *testing.T, count int) (*SessionRepository, string) {
	t.Helper()
	repository, err := NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	session, err := repository.Create(context.Background(), "agent", domain.CreateSessionInput{Title: "t"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	seedMessages(t, repository, session.ID, count)
	return repository, session.ID
}

// 尾端讀取的結果必須與完整掃描的尾端完全相同——這是取代 ListMessages 的前提。
func TestListRecentMessagesMatchesFullScanTail(t *testing.T) {
	repository, sessionID := newSeededSession(t, 60)
	ctx := context.Background()
	all, err := repository.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	for _, limit := range []int{1, 3, 10, 59} {
		recent, err := repository.ListRecentMessages(ctx, sessionID, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(recent) < limit {
			t.Fatalf("limit %d 只回傳 %d 則", limit, len(recent))
		}
		// 尾端逐則比對；實作可以多給（同一次讀取裡的其他訊息），但不能少或錯位。
		for offset := 1; offset <= limit; offset++ {
			want := all[len(all)-offset]
			got := recent[len(recent)-offset]
			if got.ID != want.ID {
				t.Fatalf("limit %d 倒數第 %d 則 = %s，應為 %s", limit, offset, got.ID, want.ID)
			}
		}
	}
}

// limit 超過總數時等同完整掃描。
func TestListRecentMessagesReturnsEverythingWhenLimitExceedsTotal(t *testing.T) {
	repository, sessionID := newSeededSession(t, 12)
	ctx := context.Background()
	all, _ := repository.ListMessages(ctx, sessionID)
	recent, err := repository.ListRecentMessages(ctx, sessionID, 100)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != len(all) {
		t.Fatalf("回傳 %d 則，應為全部 %d 則", len(recent), len(all))
	}
}

// 撤回發生在讀取範圍內時，截斷結果必須與完整掃描一致。
func TestListRecentMessagesAppliesRetractionInsideRange(t *testing.T) {
	repository, sessionID := newSeededSession(t, 40)
	ctx := context.Background()
	// 撤回倒數第 5 則之後的內容。
	if _, err := repository.AppendEntry(ctx, sessionID, domain.SessionEntry{
		Type: domain.SessionEntryMessagesRetracted,
		Data: map[string]any{"from_message_id": "msg_035"},
	}); err != nil {
		t.Fatalf("append retraction: %v", err)
	}
	all, err := repository.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	recent, err := repository.ListRecentMessages(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) == 0 || len(all) == 0 {
		t.Fatalf("撤回後不該全空：all=%d recent=%d", len(all), len(recent))
	}
	if recent[len(recent)-1].ID != all[len(all)-1].ID {
		t.Fatalf("最後一則 = %s，完整掃描為 %s", recent[len(recent)-1].ID, all[len(all)-1].ID)
	}
	for _, message := range recent {
		if message.ID >= "msg_035" {
			t.Fatalf("已撤回的 %s 不該出現", message.ID)
		}
	}
}

// 撤回指向範圍外的訊息時，局部讀取算不出正確截斷點，必須退回完整掃描。
// 寧可慢一次，也不要回傳與稽核紀錄不一致的內容。
func TestListRecentMessagesFallsBackWhenRetractionPointsOutsideRange(t *testing.T) {
	repository, sessionID := newSeededSession(t, 80)
	ctx := context.Background()
	// 撤回點在很前面，但撤回記錄本身在尾端——局部範圍內看不到 msg_002。
	if _, err := repository.AppendEntry(ctx, sessionID, domain.SessionEntry{
		Type: domain.SessionEntryMessagesRetracted,
		Data: map[string]any{"from_message_id": "msg_002"},
	}); err != nil {
		t.Fatalf("append retraction: %v", err)
	}
	all, err := repository.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	recent, err := repository.ListRecentMessages(ctx, sessionID, 5)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != len(all) {
		t.Fatalf("應退回完整掃描：recent=%d all=%d", len(recent), len(all))
	}
	for index := range all {
		if recent[index].ID != all[index].ID {
			t.Fatalf("退回後第 %d 則不一致：%s vs %s", index, recent[index].ID, all[index].ID)
		}
	}
}

// limit <= 0 等同完整掃描，呼叫端不必自己判斷。
func TestListRecentMessagesTreatsNonPositiveLimitAsFullScan(t *testing.T) {
	repository, sessionID := newSeededSession(t, 20)
	ctx := context.Background()
	all, _ := repository.ListMessages(ctx, sessionID)
	for _, limit := range []int{0, -1} {
		recent, err := repository.ListRecentMessages(ctx, sessionID, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(recent) != len(all) {
			t.Fatalf("limit %d 應等同完整掃描，得到 %d 則", limit, len(recent))
		}
	}
}
