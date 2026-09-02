package domain

import (
	"fmt"
	"strings"
)

const (
	ThinkingModeNone    = "none"
	ThinkingModeMinimal = "minimal"
	ThinkingModeLow     = "low"
	ThinkingModeMedium  = "medium"
	ThinkingModeHigh    = "high"
	ThinkingModeXHigh   = "xhigh"
)

// NormalizeThinkingMode 將使用者可選的思考程度轉成 Provider adapter 使用的
// canonical 值。空字串與 auto 都代表不覆寫 Provider／後端預設值。
func NormalizeThinkingMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "auto" {
		return "", nil
	}
	switch value {
	case ThinkingModeNone, ThinkingModeMinimal, ThinkingModeLow, ThinkingModeMedium, ThinkingModeHigh, ThinkingModeXHigh:
		return value, nil
	default:
		return "", fmt.Errorf("%w: invalid thinking mode %q", ErrInvalidInput, value)
	}
}
