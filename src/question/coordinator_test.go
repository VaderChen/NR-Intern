package question_test

import (
	"context"
	"testing"
	"time"

	"AgenticService/src/domain"
	"AgenticService/src/question"
)

func waiting(t *testing.T) (*question.Coordinator, domain.UserQuestion) {
	t.Helper()
	coordinator := question.NewCoordinator()
	value := domain.UserQuestion{
		ID:      "question_1",
		Options: []domain.UserQuestionOption{{Label: "Excel"}, {Label: "PDF"}},
		AskedAt: time.Now().UTC(),
	}
	if err := coordinator.Begin(value); err != nil {
		t.Fatalf("begin: %v", err)
	}
	return coordinator, value
}

// 選項是 Agent 提供的清單。少了這道檢查，前端的任何拼字錯誤都會變成
// 模型收到的「使用者選了 X」。
func TestAnswerRejectsAnOptionThatWasNeverOffered(t *testing.T) {
	coordinator, _ := waiting(t)
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "question_1", Selected: "Word"}); err == nil {
		t.Fatal("expected an option outside the offered list to be rejected")
	}
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "question_1", Selected: "PDF"}); err != nil {
		t.Fatalf("an offered option must be accepted: %v", err)
	}
}

// 自訂輸入不受選項清單限制——那正是它存在的理由。
func TestAnswerAcceptsCustomTextOutsideTheOptions(t *testing.T) {
	coordinator, _ := waiting(t)
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "question_1", Custom: "存成 CSV 就好"}); err != nil {
		t.Fatalf("custom text must be accepted: %v", err)
	}
}

func TestAnswerRequiresSomething(t *testing.T) {
	coordinator, _ := waiting(t)
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "question_1"}); err == nil {
		t.Fatal("an empty answer that is not a cancel should be rejected")
	}
	// 取消不需要內容：使用者本來就沒有義務回答。
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "question_1", Canceled: true}); err != nil {
		t.Fatalf("cancel must be accepted with no content: %v", err)
	}
}

// Begin 必須先於事件送出，否則極快的回覆會早於 waiter 註冊而遺失。
// 這裡驗證的是另一半：回覆在 Wait 之前送達也不能掉。
func TestAnswerBeforeWaitIsNotLost(t *testing.T) {
	coordinator, value := waiting(t)
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: value.ID, Selected: "Excel"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	answer, err := coordinator.Wait(context.Background(), value.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if answer.Resolved() != "Excel" {
		t.Fatalf("resolved = %q, want Excel", answer.Resolved())
	}
}

func TestAnswerRejectsAnUnknownQuestion(t *testing.T) {
	coordinator := question.NewCoordinator()
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: "missing", Selected: "x"}); err == nil {
		t.Fatal("expected an unknown question to be rejected")
	}
}

// 一則問題只能有一個答案：重送的 heartbeat 讓使用者可能連按兩次。
func TestSecondAnswerIsRejected(t *testing.T) {
	coordinator, value := waiting(t)
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: value.ID, Selected: "Excel"}); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: value.ID, Selected: "PDF"}); err == nil {
		t.Fatal("expected the second answer to be rejected")
	}
}

// Custom 優先於 Selected：使用者特地打了字，那就是他要的答案。
func TestResolvedPrefersCustomText(t *testing.T) {
	answer := domain.UserQuestionAnswer{Selected: "Excel", Custom: "CSV"}
	if answer.Resolved() != "CSV" {
		t.Fatalf("resolved = %q, want the custom text", answer.Resolved())
	}
	canceled := domain.UserQuestionAnswer{Selected: "Excel", Canceled: true}
	if canceled.Resolved() != "" {
		t.Fatalf("a canceled answer must resolve to nothing, got %q", canceled.Resolved())
	}
}
