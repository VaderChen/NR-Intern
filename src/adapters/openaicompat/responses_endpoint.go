package openaicompat

import (
	"fmt"
	"net/url"
	"strings"
)

// responsesEndpoint 驗證完整端點，不增刪使用者指定的路徑或查詢參數。
func responsesEndpoint(raw string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(endpoint, "#") {
		return "", fmt.Errorf("Responses URL must be a complete HTTP(S) URL without user info or fragment")
	}
	return endpoint, nil
}
