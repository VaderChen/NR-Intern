package openaicompat

import (
	"AgenticService/src/domain"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	codexResetReadTimeout     = 5 * time.Second
	codexResetConsumeTimeout  = 10 * time.Second
	maxCodexResetResponseByte = 1024 * 1024
)

// 端點做成變數而非常數，測試才有辦法指向 httptest server。
// 到期時間只有明細端點會給（/wham/usage 只回總數），所以這條路徑必須測得到。
var (
	codexResetCreditsEndpoint = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetConsumeEndpoint = codexResetCreditsEndpoint + "/consume"
)

type codexResetConsumeRequest struct {
	RedeemRequestID string `json:"redeem_request_id"`
	CreditID        string `json:"credit_id,omitempty"`
}

type codexResetConsumeResponse struct {
	Code         string `json:"code"`
	WindowsReset int64  `json:"windows_reset"`
}

// ConsumeRateLimitReset 兌換一次「用量上限重置」。
//
// 這會消耗帳號層級的有限額度且無法還原，所以只在使用者按下按鈕時呼叫，
// 不放進任何自動流程。idempotencyKey 由呼叫端提供並在重試時沿用：網路中斷
// 時無法分辨「沒送出」與「送出了但沒收到回應」，重送同一把鑰匙才不會扣兩次。
//
// 先讀一次額度明細，指定最早到期的那筆；上游只給總數時不指定，交由它自己挑。
func (m *Model) ConsumeRateLimitReset(ctx context.Context, idempotencyKey string) (domain.ProviderResetResult, error) {
	if m == nil {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: provider is unavailable", domain.ErrNotFound)
	}
	if m.authMode != "oauth" {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: 用量上限重置只適用於 ChatGPT／Codex 登入的 Provider", domain.ErrConflict)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return domain.ProviderResetResult{}, fmt.Errorf("%w: idempotency key is required", domain.ErrInvalidInput)
	}

	creditID, _ := m.fetchEarliestResetCredit(ctx)
	body, err := json.Marshal(codexResetConsumeRequest{
		RedeemRequestID: idempotencyKey,
		CreditID:        creditID,
	})
	if err != nil {
		return domain.ProviderResetResult{}, err
	}
	raw, err := m.requestCodexAccountAPI(ctx, http.MethodPost, codexResetConsumeEndpoint, body, codexResetConsumeTimeout)
	if err != nil {
		return domain.ProviderResetResult{}, err
	}
	var payload codexResetConsumeResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return domain.ProviderResetResult{}, fmt.Errorf("decode Codex reset response: %w", err)
	}
	switch outcome := strings.TrimSpace(payload.Code); outcome {
	case "reset", "nothing_to_reset", "no_credit", "already_redeemed":
		// 重置後配額視窗已經不同，順手更新快照，介面不必再等下一輪輪詢。
		if outcome == "reset" {
			_ = m.RefreshProviderUsage(ctx)
		}
		return domain.ProviderResetResult{Outcome: outcome, WindowsReset: payload.WindowsReset}, nil
	default:
		// 未知代碼一律當失敗：猜錯的話會對使用者謊報「已重置」，而額度已經扣掉了。
		return domain.ProviderResetResult{}, fmt.Errorf("Codex reset response contains an unknown outcome %q", outcome)
	}
}

// fetchEarliestResetCredit 向明細端點取最早到期的可用額度。
//
// 這是取得到期時間的唯一來源：/wham/usage 只給總數。讀不到就回空字串——
// 顯示端會標示「上游未提供」，兌換端則不指定 credit_id 交由上游自己挑。
func (m *Model) fetchEarliestResetCredit(ctx context.Context) (id string, expiresAt string) {
	// 只有 ChatGPT／Codex 帳號有這項額度；其他路線連問都不必問。
	if m == nil || m.authMode != "oauth" || m.client == nil {
		return "", ""
	}
	raw, err := m.requestCodexAccountAPI(ctx, http.MethodGet, codexResetCreditsEndpoint, nil, codexResetReadTimeout)
	if err != nil {
		return "", ""
	}
	var payload codexResetCreditsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	id, expiresAt = earliestResetCredit(payload.Credits)
	// 到期時間曾經取錯過（誤用 usage 的摘要），留下可核對的紀錄：
	// 候選筆數與選中的那一筆，出問題時不必再靠猜。
	if m.logger != nil {
		m.logger.Debug("codex reset credit selected",
			"candidates", len(payload.Credits), "credit_id", id, "expires_at", expiresAt)
	}
	return id, expiresAt
}

// requestCodexAccountAPI 送出帶 OAuth 授權的帳號 API 請求。
func (m *Model) requestCodexAccountAPI(ctx context.Context, method, target string, body []byte, timeout time.Duration) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "NR-Intern/codex")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// applyAuthorization 會一併帶上 chatgpt-account-id，帳號 API 少了它會被拒。
	if err := m.applyAuthorization(requestCtx, request); err != nil {
		return nil, err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCodexResetResponseByte+1))
	if err != nil {
		return nil, fmt.Errorf("read Codex account API response: %w", err)
	}
	if len(raw) > maxCodexResetResponseByte {
		return nil, fmt.Errorf("Codex account API response exceeds %d bytes", maxCodexResetResponseByte)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return nil, fmt.Errorf("Codex account API returned status %d: %s", response.StatusCode, message)
	}
	return raw, nil
}
