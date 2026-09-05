package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 上游沒有回報 rate_limit_reset_credits 時必須維持 Available=false。
// 回成「0 次」會讓使用者以為自己用完了，而實際上是這條路線沒有這項功能。
func TestResetCreditsAbsentStaysUnavailable(t *testing.T) {
	model := &Model{}
	var payload codexUsageResponse
	if err := json.Unmarshal([]byte(`{"rate_limit":{}}`), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)
	if credits := model.ProviderUsage().ResetCredits; credits.Available || credits.Count != 0 {
		t.Fatalf("沒有回報時不該顯示可用：%+v", credits)
	}
}

// 明確回報 0 次與「沒有這個欄位」是不同的狀態，介面要分得出來。
func TestResetCreditsZeroIsStillAvailable(t *testing.T) {
	model := &Model{}
	var payload codexUsageResponse
	if err := json.Unmarshal([]byte(`{"rate_limit_reset_credits":{"available_count":0}}`), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)
	credits := model.ProviderUsage().ResetCredits
	if !credits.Available || credits.Count != 0 {
		t.Fatalf("明確回報 0 應為「可用但次數為 0」：%+v", credits)
	}
}

// 到期提醒要指向最早到期的那一筆，否則使用者會以為還很久。
func TestResetCreditsUseEarliestExpiry(t *testing.T) {
	model := &Model{}
	body := `{"rate_limit_reset_credits":{"available_count":2,"credits":[
		{"id":"c1","status":"available","expires_at":"2026-12-01T00:00:00Z"},
		{"id":"c2","status":"available","expires_at":"2026-09-10T00:00:00Z"},
		{"id":"c3","status":"redeemed","expires_at":"2026-01-01T00:00:00Z"}]}}`
	var payload codexUsageResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)
	credits := model.ProviderUsage().ResetCredits
	if credits.Count != 2 {
		t.Fatalf("次數不對：%d", credits.Count)
	}
	// 已兌換的那筆到期更早，但不該被選中。
	if credits.NextExpiresAt != "2026-09-10T00:00:00Z" {
		t.Fatalf("應取最早到期的「可用」額度，得到 %q", credits.NextExpiresAt)
	}
}

// 非 OAuth 的 Provider 不能走這條路：API Key 路線沒有帳號層級的重置額度。
func TestConsumeRateLimitResetRequiresOAuth(t *testing.T) {
	model := &Model{authMode: "api_key"}
	if _, err := model.ConsumeRateLimitReset(t.Context(), "key"); err == nil {
		t.Fatal("非 OAuth Provider 應被擋下")
	}
}

// 少了 idempotency key 就不該送出：連線中斷重送時會扣掉第二次額度。
func TestConsumeRateLimitResetRequiresIdempotencyKey(t *testing.T) {
	model := &Model{authMode: "oauth"}
	if _, err := model.ConsumeRateLimitReset(t.Context(), "  "); err == nil {
		t.Fatal("空白的 idempotency key 應被擋下")
	}
}

// oauthTestModel 組出一個能通過 applyAuthorization 的 Model。
// access token 必須是 JWT 形狀且帶 chatgpt_account_id，否則授權會被擋下。
func oauthTestModel(t *testing.T, endpoint string) *Model {
	t.Helper()
	claims := base64.RawURLEncoding.EncodeToString([]byte(
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_test"}}`))
	token := "h." + claims + ".s"
	previousCredits, previousConsume := codexResetCreditsEndpoint, codexResetConsumeEndpoint
	codexResetCreditsEndpoint = endpoint
	codexResetConsumeEndpoint = endpoint + "/consume"
	t.Cleanup(func() {
		codexResetCreditsEndpoint, codexResetConsumeEndpoint = previousCredits, previousConsume
	})
	return &Model{
		authMode:    "oauth",
		tokenSource: func(context.Context) (string, error) { return token, nil },
		client:      &http.Client{},
	}
}

// 這是使用者回報的那個 bug：/wham/usage 只回 available_count、不含 credits 明細，
// 到期時間因此永遠是空的，介面顯示「上游未提供」。明細必須另外向端點取。
func TestResetCreditsFallBackToDetailEndpointForExpiry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if got := request.Header.Get("chatgpt-account-id"); got != "acct_test" {
			t.Errorf("帳號 API 需要 chatgpt-account-id，得到 %q", got)
		}
		_, _ = writer.Write([]byte(`{"available_count":2,"credits":[
			{"id":"c1","status":"available","expires_at":"2026-12-01T00:00:00Z"},
			{"id":"c2","status":"available","expires_at":"2026-09-10T00:00:00Z"}]}`))
	}))
	defer server.Close()
	model := oauthTestModel(t, server.URL)

	// usage 只給總數，沒有 credits 陣列——這正是上游的實際行為。
	var payload codexUsageResponse
	if err := json.Unmarshal([]byte(`{"rate_limit_reset_credits":{"available_count":2}}`), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)

	credits := model.ProviderUsage().ResetCredits
	if credits.NextExpiresAt != "2026-09-10T00:00:00Z" {
		t.Fatalf("到期時間應由明細端點補上，得到 %q", credits.NextExpiresAt)
	}
	if requests != 1 {
		t.Fatalf("應只向明細端點要一次，實際 %d 次", requests)
	}
}

// 沒有額度就不該多打一次明細端點——沒有東西會到期。
func TestResetCreditsSkipDetailFetchWhenNoneAvailable(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	model := oauthTestModel(t, server.URL)

	var payload codexUsageResponse
	if err := json.Unmarshal([]byte(`{"rate_limit_reset_credits":{"available_count":0}}`), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)

	if requests != 0 {
		t.Fatalf("沒有額度時不該查明細，實際查了 %d 次", requests)
	}
	if credits := model.ProviderUsage().ResetCredits; !credits.Available || credits.Count != 0 {
		t.Fatalf("仍應標記為可用但次數 0：%+v", credits)
	}
}

// 到期時間一律以明細端點為準。/wham/usage 的 credits 有時只帶一筆摘要，
// 拿它當主要來源會顯示成比實際更晚的到期時間，而且看起來完全正常。
func TestResetCreditsPreferDetailEndpointOverUsageSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"available_count":2,"credits":[
			{"id":"early","status":"available","expires_at":"2026-09-10T00:00:00Z"},
			{"id":"late","status":"available","expires_at":"2026-10-04T02:09:00Z"}]}`))
	}))
	defer server.Close()
	model := oauthTestModel(t, server.URL)

	// usage 只帶了較晚的那一筆——先前的實作會直接採用它，永遠問不到明細。
	var payload codexUsageResponse
	body := `{"rate_limit_reset_credits":{"available_count":2,"credits":[
		{"id":"late","status":"available","expires_at":"2026-10-04T02:09:00Z"}]}}`
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)

	if got := model.ProviderUsage().ResetCredits.NextExpiresAt; got != "2026-09-10T00:00:00Z" {
		t.Fatalf("應採用明細端點裡最早到期的那筆，得到 %q", got)
	}
}

// 明細端點讀不到時才退回 usage 帶的那份，總比什麼都不顯示好。
func TestResetCreditsFallBackToUsageWhenDetailUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	model := oauthTestModel(t, server.URL)

	var payload codexUsageResponse
	body := `{"rate_limit_reset_credits":{"available_count":1,"credits":[
		{"id":"only","status":"available","expires_at":"2026-10-04T02:09:00Z"}]}}`
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	model.storeResetCredits(t.Context(), payload.ResetCredits)

	if got := model.ProviderUsage().ResetCredits.NextExpiresAt; got != "2026-10-04T02:09:00Z" {
		t.Fatalf("明細端點失敗時應退回 usage 那份，得到 %q", got)
	}
}
