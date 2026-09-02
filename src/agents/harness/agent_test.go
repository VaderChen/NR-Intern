package harnessagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	harnesscore "AgenticService/src/harness"
)

func newRetractTestAgent(t *testing.T) (*Agent, domain.Session, *filestore.SessionRepository) {
	t.Helper()
	sessions, err := filestore.NewSessionRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionRepository: %v", err)
	}
	agent, err := New(domain.AgentDescriptor{ID: "general-agent", Name: "test"}, sessions, &harnesscore.Runner{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := sessions.Create(context.Background(), "general-agent", domain.CreateSessionInput{Title: "test", WorkspaceID: "workspace_1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return agent, session, sessions
}

func appendTestMessage(t *testing.T, sessions *filestore.SessionRepository, sessionID, id, role, content string) {
	t.Helper()
	if _, err := sessions.AppendEntry(context.Background(), sessionID, domain.SessionEntry{
		SessionID: sessionID,
		Type:      domain.SessionEntryMessage,
		Message:   &domain.Message{ID: id, SessionID: sessionID, Role: role, Content: content},
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
}

// 重新提問：最後一則提問與其後的回答一起離開對話，之前的內容不受影響。
func TestRetractMessagesRemovesTheLastQuestionAndItsAnswers(t *testing.T) {
	agent, session, sessions := newRetractTestAgent(t)
	appendTestMessage(t, sessions, session.ID, "msg_1", "user", "第一個問題")
	appendTestMessage(t, sessions, session.ID, "msg_2", "assistant", "第一個回答")
	appendTestMessage(t, sessions, session.ID, "msg_3", "user", "我一共有多少工序")
	appendTestMessage(t, sessions, session.ID, "msg_4", "assistant", "失敗的回答")

	remaining, err := agent.RetractMessages(context.Background(), session.ID, "msg_3")
	if err != nil {
		t.Fatalf("RetractMessages: %v", err)
	}
	if len(remaining) != 2 || remaining[0].ID != "msg_1" || remaining[1].ID != "msg_2" {
		t.Fatalf("remaining messages = %+v", remaining)
	}
}

// 只允許最後一則提問：重問中途的問題會讓後面所有回答失去依據。
func TestRetractMessagesRejectsAnythingButTheLastQuestion(t *testing.T) {
	agent, session, sessions := newRetractTestAgent(t)
	appendTestMessage(t, sessions, session.ID, "msg_1", "user", "第一個問題")
	appendTestMessage(t, sessions, session.ID, "msg_2", "assistant", "第一個回答")
	appendTestMessage(t, sessions, session.ID, "msg_3", "user", "第二個問題")

	if _, err := agent.RetractMessages(context.Background(), session.ID, "msg_1"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("retracting an earlier question returned %v", err)
	}
	// assistant 訊息也不是提問，同樣要擋下來。
	if _, err := agent.RetractMessages(context.Background(), session.ID, "msg_2"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("retracting an assistant message returned %v", err)
	}
	if _, err := agent.RetractMessages(context.Background(), session.ID, "msg_unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("retracting an unknown message returned %v", err)
	}
	messages, err := agent.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("a rejected retraction changed the conversation: %+v", messages)
	}
}

// 同一個問題可以連續重問：第二次撤回的是重問後新產生的那一則。
func TestRetractMessagesCanRepeatAfterReasking(t *testing.T) {
	agent, session, sessions := newRetractTestAgent(t)
	appendTestMessage(t, sessions, session.ID, "msg_1", "user", "我一共有多少工序")
	appendTestMessage(t, sessions, session.ID, "msg_2", "assistant", "第一次失敗")

	if _, err := agent.RetractMessages(context.Background(), session.ID, "msg_1"); err != nil {
		t.Fatalf("first RetractMessages: %v", err)
	}
	appendTestMessage(t, sessions, session.ID, "msg_3", "user", "我一共有多少工序")
	appendTestMessage(t, sessions, session.ID, "msg_4", "assistant", "第二次失敗")

	remaining, err := agent.RetractMessages(context.Background(), session.ID, "msg_3")
	if err != nil {
		t.Fatalf("second RetractMessages: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining messages = %+v", remaining)
	}
	entries, err := agent.ListEntries(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	retractions := 0
	for _, entry := range entries {
		if domain.RetractedFromMessageID(entry) != "" {
			retractions++
		}
	}
	if retractions != 2 {
		t.Fatalf("transcript kept %d retraction records, want 2", retractions)
	}
}

func TestRetractMessagesRequiresAMessageID(t *testing.T) {
	agent, session, _ := newRetractTestAgent(t)
	_, err := agent.RetractMessages(context.Background(), session.ID, "  ")
	if !errors.Is(err, domain.ErrInvalidInput) || !strings.Contains(err.Error(), "message id") {
		t.Fatalf("error = %v", err)
	}
}
