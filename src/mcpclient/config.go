package mcpclient

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	TransportStdio          = "stdio"
	TransportSSE            = "sse"
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
	Username              string            `json:"username,omitempty"`
	Password              string            `json:"password,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	StartupTimeoutSeconds int               `json:"startup_timeout_seconds,omitempty"`
	CallTimeoutSeconds    int               `json:"call_timeout_seconds,omitempty"`
	// EnabledTools 是這個 Server 要公開給模型的工具名稱（remote name）。
	// 空集合代表全部公開。工具定義會整份進入每一次請求，外掛型 MCP Server 動輒
	// 數十上百個工具，光是 schema 就可能佔掉數萬 token，因此必須能只挑要用的。
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// TrustAnnotations 是指標：未設定代表沿用預設值（信任），使用者明確關閉時才保存 false。
	// 少了這個區分，就無法把預設值從「不信任」改成「信任」而不影響已經明確關閉的設定。
	TrustAnnotations *bool `json:"trust_annotations,omitempty"`
}

// TrustsAnnotations 回傳這個 Server 是否採信自己宣告的工具屬性（readOnlyHint／
// idempotentHint）。預設為信任：MCP Server 由管理者自己加入，逐次核准唯讀查詢只會
// 把使用者訓練成無條件同意；不完全信任的 Server 應明確關閉這個選項。
// ToolEnabled 判斷某個遠端工具是否要公開給模型。
func (c ServerConfig) ToolEnabled(remoteName string) bool {
	if len(c.EnabledTools) == 0 {
		return true
	}
	remoteName = strings.TrimSpace(remoteName)
	for _, name := range c.EnabledTools {
		if strings.EqualFold(strings.TrimSpace(name), remoteName) {
			return true
		}
	}
	return false
}

func (c ServerConfig) TrustsAnnotations() bool {
	return c.TrustAnnotations == nil || *c.TrustAnnotations
}

func (c ServerConfig) Normalize() (ServerConfig, error) {
	c.ID = strings.TrimSpace(c.ID)
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	c.Transport = strings.ToLower(strings.TrimSpace(c.Transport))
	c.Command = strings.TrimSpace(c.Command)
	c.WorkDir = strings.TrimSpace(c.WorkDir)
	c.URL = strings.TrimSpace(c.URL)
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.Username = strings.TrimSpace(c.Username)
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
	c.EnabledTools = cloneStrings(c.EnabledTools)
	c.Environment = cleanMap(c.Environment)
	c.Headers = cleanMap(c.Headers)
	switch c.Transport {
	case TransportStdio:
		if c.Command == "" {
			return ServerConfig{}, fmt.Errorf("MCP %q 的 stdio transport 必須設定 command", c.ID)
		}
		c.URL = ""
		c.APIKey = ""
		c.Username = ""
		c.Password = ""
		c.Headers = nil
	case TransportSSE, TransportStreamableHTTP:
		parsed, err := url.Parse(c.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return ServerConfig{}, fmt.Errorf("MCP %q 必須設定有效的 HTTP／HTTPS URL", c.ID)
		}
		c.Command = ""
		c.Args = nil
		c.Environment = nil
		c.WorkDir = ""
	default:
		return ServerConfig{}, fmt.Errorf("MCP %q transport 必須是 %q、%q 或 %q", c.ID, TransportStdio, TransportSSE, TransportStreamableHTTP)
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
