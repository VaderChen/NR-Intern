package interaction_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"AgenticService/src/domain"
	"AgenticService/src/question"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/interaction"
)

func askInvocation(arguments map[string]any) tools.Invocation {
	return tools.Invocation{
		Session: domain.Session{ID: "session-1"},
		Call:    domain.ToolCall{ID: "call-1", Name: interaction.AskUserToolName, Arguments: arguments},
	}
}

// 收集工具送出的事件，並取出第一個問題。
func collect(questions chan<- domain.UserQuestion) func(domain.ToolExecution) error {
	sent := false
	return func(update domain.ToolExecution) error {
		value, ok := update.Details["question"].(domain.UserQuestion)
		if !ok || sent {
			return nil
		}
		sent = true
		questions <- value
		return nil
	}
}

func TestAskUserReturnsTheSelectedOption(t *testing.T) {
	coordinator := question.NewCoordinator()
	tool := interaction.New(coordinator, 5*time.Second)
	asked := make(chan domain.UserQuestion, 1)

	done := make(chan domain.ToolExecution, 1)
	go func() {
		result, err := tool.Execute(context.Background(), askInvocation(map[string]any{
			"question": "要用哪一種格式輸出？",
			"options":  []any{map[string]any{"label": "Excel", "description": "適合後續計算"}, map[string]any{"label": "PDF"}},
		}), collect(asked))
		if err != nil {
			t.Errorf("execute: %v", err)
		}
		done <- result
	}()

	asked1 := <-asked
	if len(asked1.Options) != 2 || asked1.Options[0].Label != "Excel" {
		t.Fatalf("question options = %#v", asked1.Options)
	}
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: asked1.ID, Selected: "PDF"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	result := <-done
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	var payload struct {
		Answer   string `json:"answer"`
		Source   string `json:"source"`
		Answered bool   `json:"answered"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Answer != "PDF" || payload.Source != "option" || !payload.Answered {
		t.Fatalf("payload = %#v", payload)
	}
}

// 自訂輸入是這個功能的重點之一：選項是 Agent 想到的，答案不必被限制在裡面。
func TestAskUserReturnsCustomText(t *testing.T) {
	coordinator := question.NewCoordinator()
	tool := interaction.New(coordinator, 5*time.Second)
	asked := make(chan domain.UserQuestion, 1)
	done := make(chan domain.ToolExecution, 1)
	go func() {
		result, _ := tool.Execute(context.Background(), askInvocation(map[string]any{
			"question": "輸出到哪裡？",
			"options":  []any{"專案根目錄", "桌面"},
		}), collect(asked))
		done <- result
	}()
	asked1 := <-asked
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: asked1.ID, Custom: "~/Documents/報表"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	result := <-done
	var payload struct {
		Answer string `json:"answer"`
		Source string `json:"source"`
	}
	_ = json.Unmarshal([]byte(result.Content), &payload)
	if payload.Answer != "~/Documents/報表" || payload.Source != "custom" {
		t.Fatalf("payload = %#v", payload)
	}
}

// 取消不是錯誤：把它當失敗會讓迴圈防護開始計數，也會讓模型以為自己做錯了。
func TestAskUserTreatsCancelAsAnAnswerNotAnError(t *testing.T) {
	coordinator := question.NewCoordinator()
	tool := interaction.New(coordinator, 5*time.Second)
	asked := make(chan domain.UserQuestion, 1)
	done := make(chan domain.ToolExecution, 1)
	go func() {
		result, _ := tool.Execute(context.Background(), askInvocation(map[string]any{
			"question": "要繼續嗎？", "options": []any{"繼續", "停下"},
		}), collect(asked))
		done <- result
	}()
	asked1 := <-asked
	if err := coordinator.Answer(domain.UserQuestionAnswer{QuestionID: asked1.ID, Canceled: true}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	result := <-done
	if result.IsError {
		t.Fatalf("cancel must not be an error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "不要重複詢問") {
		t.Fatalf("the result should tell the model how to continue: %s", result.Content)
	}
	if answered, _ := result.Details["answered"].(bool); answered {
		t.Fatal("details.answered should be false after a cancel")
	}
}

// 沒有人回答時 Run 不能永遠掛著。
func TestAskUserTimesOutWithoutHanging(t *testing.T) {
	tool := interaction.New(question.NewCoordinator(), 80*time.Millisecond)
	result, err := tool.Execute(context.Background(), askInvocation(map[string]any{
		"question": "在嗎？", "options": []any{"在"},
	}), func(domain.ToolExecution) error { return nil })
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("a timeout must not be an error result: %s", result.Content)
	}
	if answered, _ := result.Details["answered"].(bool); answered {
		t.Fatal("details.answered should be false after a timeout")
	}
}

// Run 被取消時工具必須跟著結束，不能留下等待中的 goroutine。
func TestAskUserStopsWhenTheRunIsCanceled(t *testing.T) {
	tool := interaction.New(question.NewCoordinator(), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(ctx, askInvocation(map[string]any{
			"question": "在嗎？", "options": []any{"在"},
		}), func(domain.ToolExecution) error { return nil })
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the tool did not stop after the run was canceled")
	}
}

func TestAskUserValidatesInput(t *testing.T) {
	tool := interaction.New(question.NewCoordinator(), time.Second)
	sink := func(domain.ToolExecution) error { return nil }
	for name, arguments := range map[string]map[string]any{
		"no question":      {"options": []any{"a"}},
		"no options":       {"question": "?"},
		"empty options":    {"question": "?", "options": []any{}},
		"blank label":      {"question": "?", "options": []any{"  "}},
		"duplicate labels": {"question": "?", "options": []any{"a", "a"}},
	} {
		result, err := tool.Execute(context.Background(), askInvocation(arguments), sink)
		if err != nil {
			t.Fatalf("%s: execute: %v", name, err)
		}
		if !result.IsError {
			t.Fatalf("%s: expected a validation error, got %s", name, result.Content)
		}
	}
}
