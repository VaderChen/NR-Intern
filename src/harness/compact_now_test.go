package harness

import (
	"context"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

func manualCompactionManager(t *testing.T, messageCount int) (*ContextManager, *memorySessions, domain.Session) {
	t.Helper()
	session := testSession()
	sessions := newMemorySessions(session)
	// 訊息要有真實份量，否則摘要會比被摘要的內容還長——那是壓縮應該放棄的情況，
	// 不是這幾個測試要涵蓋的情況。
	for index := 0; index < messageCount; index++ {
		label := string(rune('A' + index%26))
		role := "user"
		body := "第 " + label + " 個問題：" + strings.Repeat("這段內容代表一輪實際的工作紀錄。", 20)
		if index%2 == 1 {
			role = "assistant"
			body = "第 " + label + " 個回答：" + strings.Repeat("這段內容代表模型回覆與工具觀察的細節。", 20)
		}
		appendTestMessage(t, sessions, domain.Message{Role: role, Content: body})
	}
	return &ContextManager{
		// 摘要走正常路徑：由模型產出一段短摘要。用空的 scriptedModel 會落到
		// 本機 fallback，而那條路徑保留的內容量接近原文，測不到「壓縮」這件事。
		Model:    &scriptedModel{responses: []domain.ModelResponse{{Content: "使用者依序詢問了十二輪的生產狀況，Agent 已完成查詢與整理。"}}},
		Sessions: sessions,
		Config:   ContextConfig{RetainMessages: 4},
	}, sessions, session
}

// 自動壓縮只在超過門檻時才動作。使用者想在送出下一個問題之前先清出空間時，
// 那個門檻剛好擋住他——手動入口就是為了這個情況存在。
func TestCompactNowIgnoresTheAutomaticThreshold(t *testing.T) {
	manager, sessions, session := manualCompactionManager(t, 12)
	ctx := context.Background()

	// 先確認這份對話遠低於門檻：自動壓縮不會動它。
	window, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if window.Compacted {
		t.Fatal("this conversation is far below the threshold; the automatic path should not compact it")
	}

	result, err := manager.CompactNow(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if !result.Compacted {
		t.Fatalf("manual compaction did nothing: %+v", result)
	}
	if result.RetainedMessages != 4 {
		t.Fatalf("retained = %d, want the configured 4", result.RetainedMessages)
	}
	if result.CompactedMessages != 8 {
		t.Fatalf("compacted = %d, want 8", result.CompactedMessages)
	}
	if result.EstimatedTokensAfter >= result.EstimatedTokensBefore {
		t.Fatalf("tokens did not go down: %d -> %d", result.EstimatedTokensBefore, result.EstimatedTokensAfter)
	}

	// transcript 要留下紀錄，而且要標成手動觸發，才分得出這次壓縮是誰決定的。
	entry, err := sessions.LatestEntryOfType(ctx, session.ID, domain.SessionEntryCompaction)
	if err != nil {
		t.Fatalf("no compaction entry was written: %v", err)
	}
	if entry.Data["reason"] != "manual" {
		t.Fatalf("reason = %v, want manual", entry.Data["reason"])
	}
	if summary, _ := entry.Data["summary"].(string); strings.TrimSpace(summary) == "" {
		t.Fatal("the compaction entry has no summary")
	}
}

// 壓縮後下一次 Build 只會讀到摘要加保留訊息，不能又把舊的全部帶回來。
func TestCompactNowShrinksTheNextContext(t *testing.T) {
	manager, _, session := manualCompactionManager(t, 12)
	ctx := context.Background()
	before, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := manager.CompactNow(ctx, session, "system", nil); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	after, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build after compaction: %v", err)
	}
	if len(after.Messages) >= len(before.Messages) {
		t.Fatalf("messages after = %d, before = %d; compaction did not take effect on the next build",
			len(after.Messages), len(before.Messages))
	}
	if strings.TrimSpace(after.Summary) == "" {
		t.Fatal("the compacted history should come back as a summary")
	}
}

// 保留則數以內的對話沒有東西可壓。要照實回報，不要回一個「已壓縮」
// 卻什麼都沒變的結果讓使用者以為按鈕壞了。
func TestCompactNowReportsWhenThereIsNothingToCompact(t *testing.T) {
	manager, _, session := manualCompactionManager(t, 3)
	result, err := manager.CompactNow(context.Background(), session, "system", nil)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if result.Compacted {
		t.Fatalf("nothing should have been compacted: %+v", result)
	}
	if result.Reason != "nothing_to_compact" {
		t.Fatalf("reason = %q, want nothing_to_compact", result.Reason)
	}
	if result.EstimatedTokensAfter != result.EstimatedTokensBefore {
		t.Fatalf("tokens changed without compacting: %d -> %d", result.EstimatedTokensBefore, result.EstimatedTokensAfter)
	}
}

// 連續按兩次不能把保留訊息也吃掉。
func TestCompactNowTwiceKeepsTheRetainedMessages(t *testing.T) {
	manager, _, session := manualCompactionManager(t, 12)
	ctx := context.Background()
	if _, err := manager.CompactNow(ctx, session, "system", nil); err != nil {
		t.Fatalf("first compaction: %v", err)
	}
	second, err := manager.CompactNow(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("second compaction: %v", err)
	}
	if second.Compacted {
		t.Fatalf("the second pass had nothing left to compact but reported %+v", second)
	}
	if second.RetainedMessages == 0 {
		t.Fatal("the retained messages were swallowed by a repeated compaction")
	}
}

// 短對話的摘要可能比被摘要的內容還長。自動壓縮遇不到這件事（它只在 context
// 已經很大時觸發），但手動按鈕沒有那層保護：使用者在小對話上按下去看到用量
// 不減反增，只會認為功能壞了。這種情況要保持原狀。
func TestCompactNowRefusesWhenItWouldNotReduce(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	for index := 0; index < 10; index++ {
		appendTestMessage(t, sessions, domain.Message{Role: "user", Content: "好"})
	}
	manager := &ContextManager{
		// 摘要比十則「好」長得多——正是壓縮不該進行的情況。
		Model:    &scriptedModel{responses: []domain.ModelResponse{{Content: strings.Repeat("這是一段比原文還長的摘要。", 30)}}},
		Sessions: sessions,
		Config:   ContextConfig{RetainMessages: 4},
	}
	ctx := context.Background()

	result, err := manager.CompactNow(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if result.Compacted {
		t.Fatalf("compaction should have been refused: %+v", result)
	}
	if result.Reason != "no_reduction" {
		t.Fatalf("reason = %q, want no_reduction", result.Reason)
	}
	// 放棄必須是乾淨的：transcript 不能留下壓縮紀錄，對話也不能被動過。
	if _, err := sessions.LatestEntryOfType(ctx, session.ID, domain.SessionEntryCompaction); err == nil {
		t.Fatal("a refused compaction must not write a compaction entry")
	}
	window, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(window.Messages) != 10 {
		t.Fatalf("messages = %d, want the original 10 untouched", len(window.Messages))
	}
}
