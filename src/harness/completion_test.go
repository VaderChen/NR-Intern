package harness

import (
	"AgenticService/src/domain"
	"context"
	"strings"
	"testing"
)

func failedResult(callID, content string) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: callID, Content: content, IsError: true}
}

func TestCompletionTrackerRecordsFailures(t *testing.T) {
	tracker := newCompletionTracker()

	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, failedResult("c1", "no such file"))

	unresolved := tracker.unresolved()
	if len(unresolved) != 1 {
		t.Fatalf("unresolved = %d, want 1", len(unresolved))
	}
	if unresolved[0].ToolName != "file_read" || unresolved[0].Summary != "no such file" {
		t.Fatalf("unresolved[0] = %+v", unresolved[0])
	}
}

// TestCompletionTrackerClearsOnLaterSuccess 是這個機制不會誤報的關鍵：
// 模型發現錯誤後改對，是正常運作，不該被當成未解決。
func TestCompletionTrackerClearsOnLaterSuccess(t *testing.T) {
	tracker := newCompletionTracker()
	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, failedResult("c1", "no such file"))

	tracker.observe(domain.ToolCall{ID: "c2", Name: "file_read"}, domain.ToolExecution{ToolCallID: "c2", Content: "ok"})

	if unresolved := tracker.unresolved(); len(unresolved) != 0 {
		t.Fatalf("unresolved = %+v, want none after a later success", unresolved)
	}
}

func TestCompletionTrackerKeepsDistinctToolsSeparate(t *testing.T) {
	tracker := newCompletionTracker()
	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, failedResult("c1", "boom"))
	tracker.observe(domain.ToolCall{ID: "c2", Name: "shell_exec"}, failedResult("c2", "exit 1"))

	tracker.observe(domain.ToolCall{ID: "c3", Name: "file_read"}, domain.ToolExecution{ToolCallID: "c3"})

	unresolved := tracker.unresolved()
	if len(unresolved) != 1 || unresolved[0].ToolName != "shell_exec" {
		t.Fatalf("unresolved = %+v, want only shell_exec", unresolved)
	}
}

func TestChallengeIsSilentWithoutFailures(t *testing.T) {
	tracker := newCompletionTracker()
	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, domain.ToolExecution{ToolCallID: "c1"})

	if directive := tracker.challenge(1); directive != "" {
		t.Fatalf("challenge = %q, want empty when nothing is unresolved", directive)
	}
}

func TestChallengeRespectsItsLimit(t *testing.T) {
	tracker := newCompletionTracker()
	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, failedResult("c1", "boom"))

	if directive := tracker.challenge(1); directive == "" {
		t.Fatal("first challenge should be issued")
	}
	if directive := tracker.challenge(1); directive != "" {
		t.Fatal("the challenge limit must stop the loop from arguing forever")
	}
}

func TestChallengeDisabledByZeroLimit(t *testing.T) {
	tracker := newCompletionTracker()
	tracker.observe(domain.ToolCall{ID: "c1", Name: "file_read"}, failedResult("c1", "boom"))

	if directive := tracker.challenge(0); directive != "" {
		t.Fatal("max=0 must disable the completion gate entirely")
	}
}

// TestRunChallengesPrematureCompletion 是本機制的核心回歸測試：
// 工具失敗之後直接宣稱完成，必須先被追問一次，而不是靜默接受。
func TestRunChallengesPrematureCompletion(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
		{Content: "都處理好了"},
		{Content: "更正：檔案不存在，工作沒有完成"},
	}}
	tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "no such file", IsError: true}, nil
	}}
	runner := newTestRunner(sessions, model, tools)
	runner.MaxCompletionChecks = 1

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀檔"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if model.index != 3 {
		t.Fatalf("model was called %d times, want 3 (the premature completion must cost a turn)", model.index)
	}
	if result.Message.Content != "更正：檔案不存在，工作沒有完成" {
		t.Fatalf("content = %q, want the corrected answer", result.Message.Content)
	}
	if result.Completion == nil || result.Completion.ChecksPerformed != 1 {
		t.Fatalf("completion = %+v, want one recorded check", result.Completion)
	}
	if len(result.Completion.UnresolvedFailures) != 1 {
		t.Fatalf("unresolved = %+v, want the still-unresolved file_read failure", result.Completion.UnresolvedFailures)
	}

	// 追問內容必須實際送進模型，而且不能偽裝成使用者訊息。
	third := model.requests[2]
	if !strings.Contains(third.ContextPrompt, "<completion_check>") {
		t.Error("the challenge must reach the model through the isolated context prompt")
	}
	for _, message := range third.History {
		if strings.EqualFold(message.Role, "user") && strings.Contains(message.Content, "completion_check") {
			t.Error("the harness challenge must not be injected as a user message")
		}
	}
	if !containsString(sessions.entryTypes(), domain.SessionEntryCompletionCheck) {
		t.Error("the challenge must leave an auditable transcript entry")
	}
}

// TestRunAcceptsCompletionAfterRecovery 確保修好之後不會被追問。
func TestRunAcceptsCompletionAfterRecovery(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_read"}}},
		{Content: "讀到了"},
	}}
	attempt := 0
	tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		attempt++
		if attempt == 1 {
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "no such file", IsError: true}, nil
		}
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "內容"}, nil
	}}
	runner := newTestRunner(sessions, model, tools)
	runner.MaxCompletionChecks = 1

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀檔"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if model.index != 3 {
		t.Fatalf("model was called %d times, want 3 with no extra challenge", model.index)
	}
	if result.Completion != nil {
		t.Fatalf("completion = %+v, want none when everything resolved", result.Completion)
	}
}

func TestRunWithoutCompletionChecksKeepsLegacyBehaviour(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
		{Content: "都處理好了"},
	}}
	tools := &fakeTools{definitions: []domain.ToolDefinition{{Name: "file_read", ReadOnly: true}}, execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
		return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "boom", IsError: true}, nil
	}}
	runner := newTestRunner(sessions, model, tools)
	runner.MaxCompletionChecks = 0

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "讀檔"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if model.index != 2 {
		t.Fatalf("model was called %d times, want 2 with the gate disabled", model.index)
	}
	// 即使停用追問，未解決狀態仍必須讓 API 看得見。
	if result.Completion == nil || len(result.Completion.UnresolvedFailures) != 1 {
		t.Fatalf("completion = %+v, want the unresolved failure still reported", result.Completion)
	}
}
