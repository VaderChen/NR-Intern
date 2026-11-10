package harness

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHistoryTrimPreservesLatestToolGroupSmoke(t *testing.T) {
	runner := &Runner{Context: &ContextManager{Config: ContextConfig{MaxHistoryCharacters: 100}}}
	messages := []domain.Message{
		{Role: "user", Content: strings.Repeat("較早資料", 100)},
		assistantWithCalls("latest", domain.ToolCall{ID: "write", Name: "mutate", Arguments: map[string]any{"content": strings.Repeat("新內容", 100)}}),
		toolResult("result", "write", "已保存，不可重做"),
	}
	trimmed := runner.enforceHistoryCharacterLimit(messages, logging.Discard(), 1)
	if len(trimmed) != 2 || trimmed[0].ID != "latest" || trimmed[1].ID != "result" {
		t.Fatalf("最新工具呼叫被拆散或遺失：%+v", trimmed)
	}
	assertToolCallProtocol(t, trimmed)
}

func TestCompactionPreservesLatestToolGroupSmoke(t *testing.T) {
	sessions := newMemorySessions(testSession())
	manager := &ContextManager{Sessions: sessions, Model: &scriptedModel{responses: []domain.ModelResponse{{Content: "較早摘要"}}}}
	messages := sequenced(
		domain.Message{Role: "user", Content: "較早工作"},
		assistantWithCalls("latest", domain.ToolCall{ID: "write", Name: "mutate", Arguments: map[string]any{"content": strings.Repeat("新內容", 500)}}),
		toolResult("result", "write", "已保存，不可重做"),
	)
	config := normalizeContextConfig(ContextConfig{RetainMessages: 2})
	_, retained, _, _, err := manager.compactMessages(context.Background(), testSession(), "", nil, config, manager.counter(), "", 0, messages, 100, 5000, 0.9, "smoke", false)
	if err != nil || len(retained) != 2 || retained[0].Message.ID != "latest" {
		t.Fatalf("壓縮拆散最新工具批次：retained=%+v err=%v", retained, err)
	}
	assertToolCallProtocol(t, sequencedToMessages(retained))
}

func TestSummaryRefusalUsesFallbackSmoke(t *testing.T) {
	manager := &ContextManager{Model: &scriptedModel{responses: []domain.ModelResponse{{Content: "拒絕摘要", StopReason: "refusal"}}}, Logger: logging.Discard()}
	summary, err := manager.summarize(context.Background(), testSession(), "", sequenced(domain.Message{Role: "user", Content: "必須保存的任務限制"}), normalizeContextConfig(ContextConfig{}))
	if err != nil || !strings.Contains(summary, "必須保存的任務限制") || summary == "拒絕摘要" {
		t.Fatalf("拒絕文字被當成摘要：%q err=%v", summary, err)
	}
}

func TestHistoryCharactersCountsContinuationSmoke(t *testing.T) {
	message := domain.Message{Role: "assistant", Continuation: &domain.ModelContinuation{Items: []json.RawMessage{json.RawMessage(`{"opaque":"state"}`)}}}
	if got := historyCharacters([]domain.Message{message}); got < message.Continuation.Bytes() {
		t.Fatalf("未計入 continuation：%d", got)
	}
}

func TestHistoryTrimUsesEffectiveProviderLimitSmoke(t *testing.T) {
	catalog := &fakeCapabilities{capabilities: domain.ModelCapabilities{MaxHistoryCharacters: 1000}}
	runner := &Runner{Context: &ContextManager{Config: ContextConfig{MaxHistoryCharacters: 100}, Capabilities: catalog}}
	messages := []domain.Message{{Role: "user", Content: strings.Repeat("a", 150)}, {Role: "assistant", Content: strings.Repeat("b", 150)}}
	session := testSession()
	session.ProviderID, session.Model = "override", "large"
	trimmed := runner.enforceHistoryCharacterLimit(messages, logging.Discard(), 1, session)
	if len(trimmed) != 2 || len(catalog.asked) != 1 || catalog.asked[0] != "override/large" {
		t.Fatalf("字元限制未使用當次 Provider：kept=%d asked=%v", len(trimmed), catalog.asked)
	}
}

func TestSummaryInputBoundsArgumentsAndOpaqueStateSmoke(t *testing.T) {
	messages := []domain.Message{
		assistantWithCalls("call", domain.ToolCall{ID: "write", Name: "file_write", Arguments: map[string]any{"content": strings.Repeat("資料", 5000)}}),
		toolResult("result", "write", "已成功保存"),
	}
	messages[0].Continuation = &domain.ModelContinuation{Items: []json.RawMessage{json.RawMessage(`{"opaque":"` + strings.Repeat("x", 5000) + `"}`)}}
	for _, limit := range []int{300, 1} {
		limited := limitSummaryInput(messages, limit)
		if got := historyCharacters(limited); got > limit {
			t.Fatalf("摘要輸入仍超量：got=%d limit=%d", got, limit)
		}
		for _, message := range limited {
			if len(message.ToolCalls) > 0 || message.Role == "tool" || message.Continuation != nil {
				t.Fatalf("摘要資料仍夾帶可執行協定或不透明狀態：role=%s", message.Role)
			}
		}
	}
	if len(messages[0].ToolCalls) != 1 || messages[0].Continuation == nil {
		t.Fatal("改寫了原始 transcript")
	}
}
