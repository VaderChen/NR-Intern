package toolutil

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func String(values map[string]any, key string) string {
	value := values[key]
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case nil:
		return ""
	default:
		data, _ := json.Marshal(typed)
		return strings.TrimSpace(string(data))
	}
}

func Int(values map[string]any, key string, fallback, minimum, maximum int) int {
	value := fallback
	switch typed := values[key].(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		value = int(typed)
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil {
			value = parsed
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			value = parsed
		}
	}
	if value < minimum {
		return minimum
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

func Float(values map[string]any, key string, fallback, minimum, maximum float64) float64 {
	value := fallback
	switch typed := values[key].(type) {
	case float64:
		value = typed
	case float32:
		value = float64(typed)
	case int:
		value = float64(typed)
	case int64:
		value = float64(typed)
	case json.Number:
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			value = parsed
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			value = parsed
		}
	}
	if value < minimum {
		return minimum
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

func Bool(values map[string]any, key string, fallback bool) bool {
	switch typed := values[key].(type) {
	case bool:
		return typed
	case string:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

func StringSlice(values map[string]any, key string) []string {
	raw := values[key]
	result := []string{}
	switch typed := raw.(type) {
	case []string:
		for _, value := range typed {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	case []any:
		for _, value := range typed {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}
