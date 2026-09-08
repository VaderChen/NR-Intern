package openaicompat

import (
	"AgenticService/src/domain"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const codexUsageReadTimeout = 30 * time.Second

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
	PrimaryWindow   json.RawMessage `json:"primary_window"`
	SecondaryWindow json.RawMessage `json:"secondary_window"`
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
	usage := m.providerUsage
	usage.ResetCredits.Items = append([]domain.ProviderResetCredit(nil), usage.ResetCredits.Items...)
	return usage
}

// RefreshProviderUsage 使用 ChatGPT/Codex OAuth 的唯讀用量端點更新快照，
// 不送出模型推理請求，也不會消耗對話額度。
func (m *Model) RefreshProviderUsage(ctx context.Context) error {
	if m == nil || m.authMode != "oauth" {
		return nil
	}
	// 手動刷新、背景輪詢與額度重置後刷新共用一條寫入順序。
	m.usageRefreshMu.Lock()
	defer m.usageRefreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := m.requestCodexAccountAPI(ctx, http.MethodGet, CodexUsageEndpoint, nil, codexUsageReadTimeout)
	if err != nil {
		return err
	}
	var payload codexUsageResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode Codex usage response: %w", err)
	}
	now := time.Now().UTC()
	windows, err := codexAccountUsageWindows(payload.RateLimit, now)
	if err != nil {
		return err
	}
	// 全部驗證成功才替換；暫時失敗不更新時間，也不把未知視窗假裝成滿額。
	m.storeProviderUsage(windows[0], windows[1], now)
	m.storeResetCredits(ctx, payload.ResetCredits)
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
		// 明細**一律以明細端點為準**。/wham/usage 的 credits 欄位不完整——
		// 有時整個缺席，有時只帶一筆摘要；拿它當主要來源會顯示成比實際更晚的
		// 到期時間，而且看起來完全正常，不會有任何錯誤。
		// 沒有額度就不必問——沒有東西會到期。
		if credits.Count > 0 {
			credits.Items = m.fetchAvailableResetCredits(ctx)
		}
		// 明細端點讀不到時才退回 usage 帶的那份。
		if len(credits.Items) == 0 {
			credits.Items = availableResetCredits(payload.Credits)
		}
		if len(credits.Items) > 0 {
			credits.NextExpiresAt = credits.Items[0].ExpiresAt
		}
	}
	m.usageMu.Lock()
	m.providerUsage.ResetCredits = credits
	m.usageMu.Unlock()
}

// availableResetCredits 取出可用的額度，依到期時間由近到遠排序。
//
// 已兌換或已過期的即使到期更早也排除：列出來會讓使用者以為還能用，
// 挑到它去兌換則會失敗。排序讓「快到期的先用掉」成為介面上的預設順序。
func availableResetCredits(credits []codexResetCredit) []domain.ProviderResetCredit {
	type entry struct {
		value   domain.ProviderResetCredit
		expires time.Time
	}
	entries := make([]entry, 0, len(credits))
	for _, credit := range credits {
		if !strings.EqualFold(strings.TrimSpace(credit.Status), "available") || credit.ExpiresAt == nil {
			continue
		}
		value := strings.TrimSpace(*credit.ExpiresAt)
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		entries = append(entries, entry{
			value:   domain.ProviderResetCredit{ID: strings.TrimSpace(credit.ID), ExpiresAt: value},
			expires: parsed,
		})
	}
	sort.SliceStable(entries, func(first, second int) bool {
		return entries[first].expires.Before(entries[second].expires)
	})
	values := make([]domain.ProviderResetCredit, 0, len(entries))
	for _, item := range entries {
		values = append(values, item.value)
	}
	return values
}

func (m *Model) clearProviderUsage() {
	if m == nil {
		return
	}
	m.usageMu.Lock()
	m.providerUsage = domain.ProviderUsage{}
	m.usageMu.Unlock()
}

// storeProviderUsage 以完整 API 快照替換兩個視窗；明確的 null 必須清掉舊值。
func (m *Model) storeProviderUsage(fiveHour, sevenDay domain.ProviderUsageWindow, now time.Time) {
	if m == nil {
		return
	}
	m.usageMu.Lock()
	m.providerUsage.FiveHour = fiveHour
	m.providerUsage.SevenDay = sevenDay
	m.providerUsage.UpdatedAt = now.Format(time.RFC3339)
	m.usageMu.Unlock()
}

// codexAccountUsageWindows 比照帳號 API 的完整視窗契約，區分欄位缺漏與明確 null。
func codexAccountUsageWindows(rateLimit *codexUsageRateLimit, now time.Time) ([2]domain.ProviderUsageWindow, error) {
	var windows [2]domain.ProviderUsageWindow
	if rateLimit == nil || len(rateLimit.PrimaryWindow) == 0 || len(rateLimit.SecondaryWindow) == 0 {
		return windows, fmt.Errorf("Codex usage response is missing complete rate-limit windows")
	}
	for index, raw := range []json.RawMessage{rateLimit.PrimaryWindow, rateLimit.SecondaryWindow} {
		var value *codexUsageAPIWindow
		if err := json.Unmarshal(raw, &value); err != nil {
			return windows, fmt.Errorf("decode Codex usage window: %w", err)
		}
		if value == nil {
			continue
		}
		window := codexAPIUsageWindow(value, now)
		if !window.Available {
			return windows, fmt.Errorf("Codex usage window has invalid used_percent")
		}
		// primary 不一定是五小時；只有週額度的帳號也可能把它放在 primary。
		switch window.WindowMinutes {
		case 300:
			index = 0
		case 10080:
			index = 1
		}
		if windows[index].Available {
			return windows, fmt.Errorf("Codex usage response contains conflicting windows")
		}
		windows[index] = window
	}
	if !windows[0].Available && !windows[1].Available {
		return windows, fmt.Errorf("Codex usage response has no available windows")
	}
	return windows, nil
}

func codexAPIUsageWindow(value *codexUsageAPIWindow, now time.Time) domain.ProviderUsageWindow {
	if value == nil || value.UsedPercent == nil || math.IsNaN(*value.UsedPercent) || math.IsInf(*value.UsedPercent, 0) ||
		*value.UsedPercent < 0 || *value.UsedPercent > 100 {
		return domain.ProviderUsageWindow{}
	}
	window := domain.ProviderUsageWindow{
		Available:        true,
		RemainingPercent: 100 - *value.UsedPercent,
	}
	if value.LimitWindowSeconds != nil && *value.LimitWindowSeconds > 0 {
		window.WindowMinutes = *value.LimitWindowSeconds / 60
	}
	// 優先使用上游絕對時間，避免刷新延遲讓倒數時間逐次往後偏移。
	if value.ResetAt != nil && *value.ResetAt > 0 && *value.ResetAt <= 253402300799 {
		window.ResetAt = time.Unix(*value.ResetAt, 0).UTC().Format(time.RFC3339)
	} else if value.ResetAfterSeconds != nil && *value.ResetAfterSeconds >= 0 &&
		*value.ResetAfterSeconds < float64(math.MaxInt64/int64(time.Second)) &&
		!math.IsNaN(*value.ResetAfterSeconds) && !math.IsInf(*value.ResetAfterSeconds, 0) {
		window.ResetAt = now.Add(time.Duration(*value.ResetAfterSeconds * float64(time.Second))).Format(time.RFC3339)
	}
	return window
}
