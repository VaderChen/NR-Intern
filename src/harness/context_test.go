package harness

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRepairToolCallPairsSynthesizesMissingResult(t *testing.T) {
	messages := sequenced(
		domain.Message{Role: "user", Content: "做點事"},
		assistantWithCalls("a1",
			domain.ToolCall{ID: "call_1", Name: "file_read"},
			domain.ToolCall{ID: "call_2", Name: "shell_exec"},
		),
		toolResult("t1", "call_1", "第一個工具的結果"),
	)

	repaired := repairToolCallPairs(messages)

	if got, want := roleSummary(repaired), "user,assistant,tool:call_1,tool:call_2"; got != want {
		t.Fatalf("roles = %q, want %q", got, want)
	}
	synthetic := repaired[3].Message
	if !synthetic.IsError {
		t.Error("synthesized tool result should be marked as an error")
	}
	if synthetic.ToolName != "shell_exec" {
		t.Errorf("tool name = %q, want shell_exec", synthetic.ToolName)
	}
	if !strings.Contains(synthetic.Content, "沒有留下結果") {
		t.Errorf("content = %q, want an explicit interruption notice", synthetic.Content)
	}
	if synthetic.Metadata["synthesized"] != true {
		t.Error("synthesized tool result should be traceable through metadata")
	}
}

func TestRepairToolCallPairsDropsUnmatchedToolMessages(t *testing.T) {
	messages := sequenced(
		toolResult("t0", "call_missing", "沒有對應 assistant 的結果"),
		domain.Message{Role: "user", Content: "hi"},
		assistantWithCalls("a1", domain.ToolCall{ID: "call_1", Name: "file_read"}),
		toolResult("t1", "call_1", "ok"),
		toolResult("t2", "call_unknown", "多出來的結果"),
	)

	repaired := repairToolCallPairs(messages)

	if got, want := roleSummary(repaired), "user,assistant,tool:call_1"; got != want {
		t.Fatalf("roles = %q, want %q", got, want)
	}
}

func TestRepairToolCallPairsDeduplicatesResults(t *testing.T) {
	messages := sequenced(
		assistantWithCalls("a1", domain.ToolCall{ID: "call_1", Name: "file_read"}),
		toolResult("t1", "call_1", "第一次"),
		toolResult("t2", "call_1", "重複寫入"),
	)

	repaired := repairToolCallPairs(messages)

	if got, want := roleSummary(repaired), "assistant,tool:call_1"; got != want {
		t.Fatalf("roles = %q, want %q", got, want)
	}
	if repaired[1].Message.Content != "第一次" {
		t.Errorf("content = %q, want the first recorded result", repaired[1].Message.Content)
	}
}

func TestRepairToolCallPairsKeepsValidSequenceUnchanged(t *testing.T) {
	messages := sequenced(
		domain.Message{Role: "user", Content: "hi"},
		assistantWithCalls("a1",
			domain.ToolCall{ID: "call_1", Name: "file_read"},
			domain.ToolCall{ID: "call_2", Name: "file_search"},
		),
		toolResult("t1", "call_1", "one"),
		toolResult("t2", "call_2", "two"),
		domain.Message{Role: "assistant", Content: "完成"},
	)

	repaired := repairToolCallPairs(messages)

	if got, want := roleSummary(repaired), "user,assistant,tool:call_1,tool:call_2,assistant"; got != want {
		t.Fatalf("roles = %q, want %q", got, want)
	}
}

// TestContextBuildRepairsTranscript 是回歸測試：中斷留下的孤兒 tool_call
// 不能被原樣送進模型，否則 Provider 會拒絕整個 session。
func TestContextBuildRepairsTranscript(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	ctx := context.Background()
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: "hi"})
	appendTestMessage(t, sessions, assistantWithCalls("a1", domain.ToolCall{ID: "call_1", Name: "shell_exec"}))

	manager := &ContextManager{Model: &scriptedModel{}, Sessions: sessions}
	window, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(window.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(window.Messages))
	}
	last := window.Messages[2]
	if !strings.EqualFold(last.Role, "tool") || last.ToolCallID != "call_1" {
		t.Fatalf("last message = %+v, want a tool result for call_1", last)
	}
}

func TestContextBuildCountsToolSchemas(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: "hi"})
	manager := &ContextManager{Model: &scriptedModel{}, Sessions: sessions}

	bare, err := manager.Build(context.Background(), session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	withTools, err := manager.Build(context.Background(), session, "system", []domain.ToolDefinition{{
		Name:        "shell_exec",
		Description: "執行 shell 指令",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if withTools.EstimatedTokens <= bare.EstimatedTokens {
		t.Fatalf("tool schemas were not counted: %d <= %d", withTools.EstimatedTokens, bare.EstimatedTokens)
	}
}

func TestSplitForCompactionKeepsToolResultsWithTheirCall(t *testing.T) {
	messages := sequenced(
		domain.Message{Role: "user", Content: "1"},
		domain.Message{Role: "assistant", Content: "2"},
		assistantWithCalls("a1", domain.ToolCall{ID: "call_1", Name: "file_read"}),
		toolResult("t1", "call_1", "result"),
	)

	older, retained := splitForCompaction(messages, 1)

	if got, want := roleSummary(retained), "assistant,tool:call_1"; got != want {
		t.Fatalf("retained = %q, want %q", got, want)
	}
	if got, want := roleSummary(older), "user,assistant"; got != want {
		t.Fatalf("older = %q, want %q", got, want)
	}
}

func appendTestMessage(t *testing.T, sessions *memorySessions, message domain.Message) {
	t.Helper()
	copyMessage := message
	if _, err := sessions.AppendEntry(context.Background(), sessions.session.ID, domain.SessionEntry{
		Type:    domain.SessionEntryMessage,
		Message: &copyMessage,
	}); err != nil {
		t.Fatalf("append entry: %v", err)
	}
}

// TestBudgetFallsBackWhenModelWindowUnknown 保留「未宣告限制」的既有行為。
func TestBudgetFallsBackWhenModelWindowUnknown(t *testing.T) {
	manager := &ContextManager{Capabilities: &fakeCapabilities{}}
	config := normalizeContextConfig(ContextConfig{MaxEstimatedTokens: 32_000})

	if got, want := manager.budget(config, testSession()), 32_000; got != want {
		t.Fatalf("budget = %d, want the configured fallback %d", got, want)
	}
}

func TestDefaultBudgetIs512K(t *testing.T) {
	config := normalizeContextConfig(ContextConfig{})
	if got, want := config.MaxEstimatedTokens, 512*1024; got != want {
		t.Fatalf("default budget = %d, want %d", got, want)
	}
}

func TestBudgetWithoutCapabilityLookupUsesConfig(t *testing.T) {
	manager := &ContextManager{}
	config := normalizeContextConfig(ContextConfig{MaxEstimatedTokens: 32_000})

	if got, want := manager.budget(config, testSession()), 32_000; got != want {
		t.Fatalf("budget = %d, want %d", got, want)
	}
}

// TestBudgetDerivesFromDeclaredContextWindow 是這項修正的重點：
// 預算必須跟著當次實際使用的模型走，而不是綁在單一全域設定值。
func TestBudgetDerivesFromDeclaredContextWindow(t *testing.T) {
	manager := &ContextManager{Capabilities: &fakeCapabilities{capabilities: domain.ModelCapabilities{
		ContextWindow:   128_000,
		MaxOutputTokens: 16_384,
	}}}
	config := normalizeContextConfig(ContextConfig{MaxEstimatedTokens: 32_000})

	if got, want := manager.budget(config, testSession()), 128_000-16_384; got != want {
		t.Fatalf("budget = %d, want %d", got, want)
	}
}

func TestBudgetUsesReservedOutputWhenModelDeclaresNoOutputLimit(t *testing.T) {
	manager := &ContextManager{Capabilities: &fakeCapabilities{capabilities: domain.ModelCapabilities{ContextWindow: 32_768}}}
	config := normalizeContextConfig(ContextConfig{MaxEstimatedTokens: 32_000, ReservedOutputTokens: 8_000})

	if got, want := manager.budget(config, testSession()), 32_768-8_000; got != want {
		t.Fatalf("budget = %d, want %d", got, want)
	}
}

func TestBudgetNeverInventsCapacityForTinyContextWindows(t *testing.T) {
	manager := &ContextManager{Capabilities: &fakeCapabilities{capabilities: domain.ModelCapabilities{ContextWindow: 4_096}}}
	config := normalizeContextConfig(ContextConfig{MaxEstimatedTokens: 32_000})

	if got := manager.budget(config, testSession()); got != 0 {
		t.Fatalf("budget = %d, want 0 because output reserve consumes the whole model window", got)
	}
}

func TestBudgetAsksForTheSessionProviderAndModel(t *testing.T) {
	lookup := &fakeCapabilities{}
	manager := &ContextManager{Capabilities: lookup}
	session := testSession()
	session.ProviderID = "local"
	session.Model = "qwen"

	manager.budget(normalizeContextConfig(ContextConfig{}), session)

	if len(lookup.asked) != 1 || lookup.asked[0] != "local/qwen" {
		t.Fatalf("asked = %v, want the session provider and model", lookup.asked)
	}
}

// TestContextBuildReadsOnlyAfterTheCompactionPoint 保護 O(N²) 修正：
// 已經被摘要涵蓋的 entry 不應該每個 turn 重新讀出來。
func TestContextBuildReadsOnlyAfterTheCompactionPoint(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	ctx := context.Background()
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: "很早以前的訊息"})
	compaction, err := sessions.AppendEntry(ctx, session.ID, domain.SessionEntry{
		Type: domain.SessionEntryCompaction,
		Data: map[string]any{"summary": "先前的摘要", "through_sequence": int64(1)},
	})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: "摘要之後的訊息"})

	manager := &ContextManager{Model: &scriptedModel{}, Sessions: sessions}
	window, err := manager.Build(ctx, session, "system", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if compaction.Sequence == 0 {
		t.Fatal("compaction entry was not sequenced")
	}
	if len(window.Messages) != 1 || window.Messages[0].Content != "摘要之後的訊息" {
		t.Fatalf("messages = %+v, want only the message after the compaction point", window.Messages)
	}
	if !strings.Contains(window.SystemPrompt, "先前的摘要") {
		t.Error("the compaction summary must be carried into the system prompt")
	}
}

func TestSummaryUsesConfiguredProviderAndModelOverride(t *testing.T) {
	session := testSession()
	session.ProviderID = "expensive"
	session.Model = "large-model"
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "摘要完成"}}}
	manager := &ContextManager{Model: model, Sessions: newMemorySessions(session)}
	config := normalizeContextConfig(ContextConfig{SummaryProviderID: "cheap", SummaryModel: "small-model"})

	if _, err := manager.summarize(context.Background(), session, "", sequenced(
		domain.Message{Role: "user", Content: "需要摘要的內容"},
	), config); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	request := model.lastRequest()
	if request.ProviderID != "cheap" || request.Model != "small-model" {
		t.Fatalf("summary route = %s/%s, want cheap/small-model", request.ProviderID, request.Model)
	}
}

func TestSummaryFallsBackToActiveSessionProvider(t *testing.T) {
	session := testSession()
	session.ProviderID = "active"
	session.Model = "active-model"
	model := &scriptedModel{
		responses: []domain.ModelResponse{{}, {Content: "使用目前 Session 完成摘要"}},
		errors:    []error{errors.New("configured summary provider returned 401"), nil},
	}
	manager := &ContextManager{Model: model, Sessions: newMemorySessions(session)}
	config := normalizeContextConfig(ContextConfig{SummaryProviderID: "missing-key", SummaryModel: "summary-model"})

	summary, err := manager.summarize(context.Background(), session, "", sequenced(
		domain.Message{Role: "tool", ToolName: "directory_list", Content: `{"entries":["go.mod"]}`},
	), config)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary != "使用目前 Session 完成摘要" {
		t.Fatalf("summary = %q", summary)
	}
	if len(model.requests) != 2 {
		t.Fatalf("requests = %d, want configured route plus active Session fallback", len(model.requests))
	}
	if got := model.requests[1]; got.ProviderID != "active" || got.Model != "active-model" {
		t.Fatalf("fallback route = %s/%s, want active/active-model", got.ProviderID, got.Model)
	}
}

func TestSummaryUsesDeterministicFallbackWhenAllProvidersFail(t *testing.T) {
	session := testSession()
	session.ProviderID = "active"
	session.Model = "active-model"
	model := &scriptedModel{
		responses: []domain.ModelResponse{{}, {}},
		errors:    []error{errors.New("summary provider unavailable"), errors.New("session provider unavailable")},
	}
	manager := &ContextManager{Model: model, Sessions: newMemorySessions(session)}
	config := normalizeContextConfig(ContextConfig{SummaryProviderID: "summary", SummaryModel: "summary-model"})

	summary, err := manager.summarize(context.Background(), session, "", sequenced(
		domain.Message{Role: "user", Content: "請確認 go.mod"},
		domain.Message{Role: "tool", ToolName: "directory_list", Content: `{"entries":["go.mod"]}`},
	), config)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if !strings.Contains(summary, "請確認 go.mod") || !strings.Contains(summary, "go.mod") {
		t.Fatalf("deterministic summary lost task evidence: %s", summary)
	}
}

// TestSoftCompactionStartsBeforeHardBudget 驗證 80% soft limit 具有實際效果：
// context 尚未超過硬上限時就先產生摘要，下一輪不必同步等待摘要模型。
func TestSoftCompactionStartsBeforeHardBudget(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: strings.Repeat("中", 100)})
	appendTestMessage(t, sessions, domain.Message{Role: "assistant", Content: strings.Repeat("文", 100)})
	model := &scriptedModel{responses: []domain.ModelResponse{{Content: "背景摘要"}}}
	manager := &ContextManager{
		Model: model, Sessions: sessions,
		Config: ContextConfig{MaxEstimatedTokens: 250, RetainMessages: 1},
	}

	compacted, err := manager.compactIfNeeded(context.Background(), session, "system", nil, softCompactionRatio)
	if err != nil {
		t.Fatalf("compactIfNeeded: %v", err)
	}
	if !compacted {
		t.Fatal("context above 80% was not compacted before the hard limit")
	}
	entry, err := sessions.LatestEntryOfType(context.Background(), session.ID, domain.SessionEntryCompaction)
	if err != nil || entry.Data["summary"] != "背景摘要" || entry.Data["reason"] != "context_budget" {
		t.Fatalf("compaction entry = %+v, err = %v", entry, err)
	}
}

func TestScheduleCompactionRunsInBackgroundAndDeduplicatesSession(t *testing.T) {
	session := testSession()
	sessions := newMemorySessions(session)
	appendTestMessage(t, sessions, domain.Message{Role: "user", Content: strings.Repeat("中", 100)})
	appendTestMessage(t, sessions, domain.Message{Role: "assistant", Content: strings.Repeat("文", 100)})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	model := &scriptedModel{
		responses: []domain.ModelResponse{{Content: "非同步摘要"}},
		onStream: func(int) {
			entered <- struct{}{}
			<-release
		},
	}
	manager := &ContextManager{
		Model: model, Sessions: sessions,
		Config: ContextConfig{MaxEstimatedTokens: 250, RetainMessages: 1},
	}

	if !manager.ScheduleCompaction(context.Background(), session, "system", nil) {
		t.Fatal("first background compaction was not scheduled")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background compaction did not reach the model")
	}
	if manager.ScheduleCompaction(context.Background(), session, "system", nil) {
		t.Fatal("duplicate background compaction was scheduled for the same session")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if entry, err := sessions.LatestEntryOfType(context.Background(), session.ID, domain.SessionEntryCompaction); err == nil && entry.Data["summary"] == "非同步摘要" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background compaction did not persist its summary")
}
