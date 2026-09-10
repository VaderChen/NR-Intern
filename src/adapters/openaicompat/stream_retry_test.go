package openaicompat

import (
	"AgenticService/src/domain"
	"testing"
)

// 這是實際遇到的封包：上游在串流中回一段通用的暫時性錯誤，訊息裡明白寫著
// 可以重試。舊的判斷只認「overloaded」與「temporarily unavailable」兩種措辭，
// 所以這一種被當成永久失敗，整個 Run 直接結束。
func TestRetryableStreamErrorAcceptsTheUpstreamsOwnRetryAdvice(t *testing.T) {
	message := "An error occurred while processing your request. You can retry your request, " +
		"or contact us through our help center at help.openai.com if the error persists."
	if !retryableStreamError("", message) {
		t.Fatal("上游自己說可以重試，卻被判成不可重試")
	}
}

// 永久性失敗不能因為這次放寬而變成可重試——重試一把錯的金鑰只是浪費三次。
func TestRetryableStreamErrorStillRejectsPermanentFailures(t *testing.T) {
	cases := []struct{ code, message string }{
		{"invalid_api_key", "Incorrect API key provided"},
		{"insufficient_quota", "You exceeded your current quota"},
		{"permission_denied", "You do not have access to this model"},
		{"", "The model `gpt-nonexistent` does not exist"},
	}
	for _, test := range cases {
		if retryableStreamError(test.code, test.message) {
			t.Fatalf("%q／%q 不該被判成可重試", test.code, test.message)
		}
	}
}

// 思考內容不再阻擋重試；回答文字與工具呼叫參數仍要阻擋，那才是重試會真的
// 變成兩份的東西。
func TestThinkingDeltaDoesNotBlockRetry(t *testing.T) {
	if observableModelOutput(domain.ModelEventThinkingDelta) {
		t.Fatal("思考內容不該阻擋重試")
	}
	for _, eventType := range []string{domain.ModelEventTextDelta, domain.ModelEventToolCallDelta} {
		if !observableModelOutput(eventType) {
			t.Fatalf("%s 應該阻擋重試", eventType)
		}
	}
}
