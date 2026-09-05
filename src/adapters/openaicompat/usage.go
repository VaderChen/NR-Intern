package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxCodexUsageResponseBytes = 2 * 1024 * 1024

type codexUsageResponse struct {
	RateLimit    *codexUsageRateLimit      `json:"rate_limit"`
	ResetCredits *codexResetCreditsPayload `json:"rate_limit_reset_credits"`
}

type codexResetCreditsPayload struct {
	AvailableCount *int64             `json:"available_count"`
	Credits        []codexResetCredit `json:"credits"`
}

type codexResetCredit struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	ExpiresAt *string `json:"expires_at"`
}

type codexUsageRateLimit struct {
	PrimaryWindow   *codexUsageAPIWindow `json:"primary_window"`
	SecondaryWindow *codexUsageAPIWindow `json:"secondary_window"`
}

type codexUsageAPIWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int     `json:"limit_window_seconds"`
	ResetAt            *int64   `json:"reset_at"`
	ResetAfterSeconds  *float64 `json:"reset_after_seconds"`
}

// ProviderUsage 回傳最近一次由上游明確提供的 Codex 配額快照。
func (m *Model) ProviderUsage() domain.ProviderUsage {
	if m == nil {
		return domain.ProviderUsage{}
	}
	m.usageMu.RLock()
	defer m.usageMu.RUnlock()
	return m.providerUsage
}

// RefreshProviderUsage 使用 ChatGPT/Codex OAuth 的唯讀用量端點更新快照，
// 不送出模型推理請求，也不會消耗對話額度。
func (m *Model) RefreshProviderUsage(ctx context.Context) error {
	if m == nil || m.authMode != "oauth" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, CodexUsageEndpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "NR-Intern/codex")
	if err := m.applyAuthorization(ctx, request); err != nil {
		m.clearProviderUsage()
		return err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			m.clearProviderUsage()
		}
		return fmt.Errorf("Codex usage endpoint returned status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCodexUsageResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Codex usage response: %w", err)
	}
	if len(data) > maxCodexUsageResponseBytes {
		return fmt.Errorf("Codex usage response exceeds %d bytes", maxCodexUsageResponseBytes)
	}
	var payload codexUsageResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode Codex usage response: %w", err)
	}
	now := time.Now().UTC()
	// 重置額度與配額視窗來自同一份回應，但要分開存：只有額度、沒有視窗的回應
	// 仍然有意義，而 storeProviderUsage 在兩個視窗都不可用時會直接跳過。
	m.storeResetCredits(ctx, payload.ResetCredits)
	if payload.RateLimit == nil {
		return nil
	}
	m.storeProviderUsage(
		codexAPIUsageWindow(payload.RateLimit.PrimaryWindow, now),
		codexAPIUsageWindow(payload.RateLimit.SecondaryWindow, now),
		now,
	)
	return nil
}

// storeResetCredits 記下帳號目前可用的重置次數。
//
// 上游沒有回報這個欄位時保持 Available=false，讓介面完全不顯示重置入口；
// 顯示成「0 次」會讓使用者以為自己用完了，而不是「這條路線沒有這功能」。
func (m *Model) storeResetCredits(ctx context.Context, payload *codexResetCreditsPayload) {
	if m == nil {
		return
	}
	credits := domain.ProviderResetCredits{}
	if payload != nil && payload.AvailableCount != nil && *payload.AvailableCount >= 0 {
		credits.Available = true
		credits.Count = *payload.AvailableCount
		// 到期時間**一律以明細端點為準**。/wham/usage 的 credits 欄位不完整——
		// 有時整個缺席，有時只帶一筆摘要；拿它當主要來源會顯示成比實際更晚的
		// 到期時間，而且看起來完全正常，不會有任何錯誤。
		// 沒有額度就不必問——沒有東西會到期。
		if credits.Count > 0 {
			_, credits.NextExpiresAt = m.fetchEarliestResetCredit(ctx)
		}
		// 明細端點讀不到時才退回 usage 帶的那份。
		if credits.NextExpiresAt == "" {
			_, credits.NextExpiresAt = earliestResetCredit(payload.Credits)
		}
	}
	m.usageMu.Lock()
	m.providerUsage.ResetCredits = credits
	m.usageMu.Unlock()
}

// earliestResetCredit 回傳最早到期的「可用」額度的 ID 與到期時間。
//
// 已兌換或已過期的額度即使到期更早也不算：拿它去顯示會讓使用者以為快過期了，
// 拿它去兌換則會失敗。
func earliestResetCredit(credits []codexResetCredit) (id string, expiresAt string) {
	var earliest time.Time
	for _, credit := range credits {
		if !strings.EqualFold(strings.TrimSpace(credit.Status), "available") || credit.ExpiresAt == nil {
			continue
		}
		value := strings.TrimSpace(*credit.ExpiresAt)
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		if expiresAt == "" || parsed.Before(earliest) {
			id, expiresAt, earliest = strings.TrimSpace(credit.ID), value, parsed
		}
	}
	return id, expiresAt
}

func (m *Model) clearProviderUsage() {
	if m == nil {
		return
	}
	m.usageMu.Lock()
	m.providerUsage = domain.ProviderUsage{}
	m.usageMu.Unlock()
}

func (m *Model) recordProviderUsage(headers http.Header) {
	if m == nil {
		return
	}
	now := time.Now().UTC()
	fiveHour := codexUsageWindow(headers, "primary", now)
	sevenDay := codexUsageWindow(headers, "secondary", now)
	m.storeProviderUsage(fiveHour, sevenDay, now)
}

func (m *Model) storeProviderUsage(fiveHour, sevenDay domain.ProviderUsageWindow, now time.Time) {
	if m == nil {
		return
	}
	if !fiveHour.Available && !sevenDay.Available {
		return
	}

	m.usageMu.Lock()
	if fiveHour.Available {
		m.providerUsage.FiveHour = fiveHour
	}
	if sevenDay.Available {
		m.providerUsage.SevenDay = sevenDay
	}
	m.providerUsage.UpdatedAt = now.Format(time.RFC3339)
	m.usageMu.Unlock()
}

func codexAPIUsageWindow(value *codexUsageAPIWindow, now time.Time) domain.ProviderUsageWindow {
	if value == nil || value.UsedPercent == nil || math.IsNaN(*value.UsedPercent) || math.IsInf(*value.UsedPercent, 0) {
		return domain.ProviderUsageWindow{}
	}
	window := domain.ProviderUsageWindow{
		Available:        true,
		RemainingPercent: clampPercent(100 - *value.UsedPercent),
	}
	if value.LimitWindowSeconds != nil && *value.LimitWindowSeconds > 0 {
		window.WindowMinutes = *value.LimitWindowSeconds / 60
	}
	if value.ResetAfterSeconds != nil && *value.ResetAfterSeconds >= 0 && !math.IsNaN(*value.ResetAfterSeconds) && !math.IsInf(*value.ResetAfterSeconds, 0) {
		window.ResetAt = now.Add(time.Duration(*value.ResetAfterSeconds * float64(time.Second))).Format(time.RFC3339)
	} else if value.ResetAt != nil && *value.ResetAt > 0 {
		window.ResetAt = time.Unix(*value.ResetAt, 0).UTC().Format(time.RFC3339)
	}
	return window
}

func codexUsageWindow(headers http.Header, prefix string, now time.Time) domain.ProviderUsageWindow {
	remaining, available := percentHeader(headers, "X-Codex-"+prefix+"-Remaining-Percent")
	if !available {
		if used, ok := percentHeader(headers, "X-Codex-"+prefix+"-Used-Percent"); ok {
			remaining, available = clampPercent(100-used), true
		}
	}
	if !available {
		return domain.ProviderUsageWindow{}
	}

	window := domain.ProviderUsageWindow{
		Available:        true,
		RemainingPercent: remaining,
	}
	if minutes, ok := numberHeader(headers, "X-Codex-"+prefix+"-Window-Minutes"); ok && minutes > 0 {
		window.WindowMinutes = int(minutes)
	}
	if seconds, ok := numberHeader(headers, "X-Codex-"+prefix+"-Reset-After-Seconds"); ok && seconds >= 0 {
		window.ResetAt = now.Add(time.Duration(seconds * float64(time.Second))).Format(time.RFC3339)
	} else if epoch, ok := numberHeader(headers, "X-Codex-"+prefix+"-Reset-At"); ok && epoch > 0 {
		window.ResetAt = time.Unix(int64(epoch), 0).UTC().Format(time.RFC3339)
	}
	return window
}

func percentHeader(headers http.Header, name string) (float64, bool) {
	value, ok := numberHeader(headers, name)
	if !ok {
		return 0, false
	}
	return clampPercent(value), true
}

func numberHeader(headers http.Header, name string) (float64, bool) {
	text := strings.TrimSpace(strings.TrimSuffix(headers.Get(name), "%"))
	if text == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
