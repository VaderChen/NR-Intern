// Package valueutil 集中跨層都會用到、沒有領域語意的微型值操作。
// 這裡刻意不放 repository、HTTP 或 Harness 行為，避免 internal 變成新的雜物層。
package valueutil

import "strings"

func CloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func FirstError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
