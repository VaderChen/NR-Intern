package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"AgenticService/src/domain"
)

// 端點是問答選單唯一的回程。這裡驗證的是 HTTP 接線本身：
// 協調器的規則有自己的單元測試，但「路由存在、參數對得上、錯誤回得出來」
// 只有從 handler 這一層才看得到。
func TestAnswerQuestionRejectsAnUnknownQuestion(t *testing.T) {
	runtime := testRuntime(t, "")
	recorder, _ := call(t, runtime, http.MethodPost, "/api/v1/questions/question_missing/answer",
		`{"selected":"Excel"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a question nobody is waiting on; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAnswerQuestionRejectsAnEmptyAnswer(t *testing.T) {
	runtime := testRuntime(t, "")
	if err := runtime.Questions.Begin(userQuestionFixture()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	recorder, _ := call(t, runtime, http.MethodPost, "/api/v1/questions/question_fixture/answer", `{}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when neither an option nor custom text was given", recorder.Code)
	}
}

// 送出的答案必須真的抵達等待中的工具。
func TestAnswerQuestionReachesTheWaiter(t *testing.T) {
	runtime := testRuntime(t, "")
	if err := runtime.Questions.Begin(userQuestionFixture()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	answered := make(chan string, 1)
	go func() {
		answer, err := runtime.Questions.Wait(t.Context(), "question_fixture")
		if err != nil {
			answered <- "error: " + err.Error()
			return
		}
		answered <- answer.Resolved()
	}()
	recorder, _ := call(t, runtime, http.MethodPost, "/api/v1/questions/question_fixture/answer",
		`{"custom":"存成 CSV 就好"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if resolved := <-answered; resolved != "存成 CSV 就好" {
		t.Fatalf("the waiting tool received %q", resolved)
	}
}

func userQuestionFixture() domain.UserQuestion {
	return domain.UserQuestion{
		ID:       "question_fixture",
		Question: "要輸出成哪一種格式？",
		Options:  []domain.UserQuestionOption{{Label: "Excel"}, {Label: "PDF"}},
		AskedAt:  time.Now().UTC(),
	}
}
