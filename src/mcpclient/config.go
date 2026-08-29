package mcpclient

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	TransportStdio          = "stdio"
	TransportStreamableHTTP = "streamable-http"
)

var serverIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ServerConfig 描述主系統要連線的單一 MCP Server。APIKey、Environment 與
// Headers 可能包含憑證，只能寫入權限限制設定檔，不得直接回傳給管理介面。
type ServerConfig struct {
	ID                    string            `json:"id"`
	DisplayName           string            `json:"display_name,omitempty"`
	Enabled               bool              `json:"enabled"`
	Transport             string            `json:"transport"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	WorkDir               string            `json:"work_dir,omitempty"`
	URL                   string            `json:"url,omitempty"`
	APIKey                string            `json:"api_key,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	StartupTimeoutSeconds int               `json:"startup_timeout_seconds,omitempty"`
	CallTimeoutSeconds    int               `json:"call_timeout_seconds,omitempty"`
	TrustAnnotations      bool              `json:"trust_annotations,omitempty"`
}

func (c ServerConfig) Normalize() (ServerConfig, error) {
	c.ID = strings.TrimSpace(c.ID)
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	c.Transport = strings.ToLower(strings.TrimSpace(c.Transport))
	c.Command = strings.TrimSpace(c.Command)
	c.WorkDir = strings.TrimSpace(c.WorkDir)
	c.URL = strings.TrimSpace(c.URL)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.ID == "" || len(c.ID) > 80 || !serverIDPattern.MatchString(c.ID) {
		return ServerConfig{}, fmt.Errorf("MCP ID 必須是 1～80 個英數字、底線或連字號")
	}
	if c.DisplayName == "" {
		c.DisplayName = c.ID
	}
	if len([]rune(c.DisplayName)) > 80 {
		return ServerConfig{}, fmt.Errorf("MCP %q 顯示名稱不得超過 80 個字元", c.ID)
	}
	if c.StartupTimeoutSeconds <= 0 {
		c.StartupTimeoutSeconds = 20
	}
	if c.StartupTimeoutSeconds > 300 {
		return ServerConfig{}, fmt.Errorf("MCP %q 啟動逾時不得超過 300 秒", c.ID)
	}
	if c.CallTimeoutSeconds <= 0 {
		c.CallTimeoutSeconds = 1800
	}
	if c.CallTimeoutSeconds > 86400 {
		return ServerConfig{}, fmt.Errorf("MCP %q 呼叫逾時不得超過 86400 秒", c.ID)
	}
	c.Args = cloneStrings(c.Args)
	c.Environment = cleanMap(c.Environment)
	c.Headers = cleanMap(c.Headers)
	switch c.Transport {
	case TransportStdio:
		if c.Command == "" {
			return ServerConfig{}, fmt.Errorf("MCP %q 的 stdio transport 必須設定 command", c.ID)
		}
		c.URL = ""
		c.APIKey = ""
		c.Headers = nil
	case TransportStreamableHTTP:
		parsed, err := url.Parse(c.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return ServerConfig{}, fmt.Errorf("MCP %q 必須設定有效的 HTTP／HTTPS URL", c.ID)
		}
		c.Command = ""
		c.Args = nil
		c.Environment = nil
		c.WorkDir = ""
	default:
		return ServerConfig{}, fmt.Errorf("MCP %q transport 必須是 %q 或 %q", c.ID, TransportStdio, TransportStreamableHTTP)
	}
	return c, nil
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func cleanMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
