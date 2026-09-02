package domain

// MCPSettings 是「使用 MCP」管理頁可讀取的完整 Server 清單。
// API Key、Environment 與 Headers 只接受寫入，不會透過 API 回傳明文。
type MCPSettings struct {
	Servers []MCPServerSetting `json:"servers"`
}

type UpdateMCPSettingsInput struct {
	Servers []MCPServerSetting `json:"servers"`
}

type MCPServerSetting struct {
	ID                    string            `json:"id"`
	DisplayName           string            `json:"display_name,omitempty"`
	Enabled               bool              `json:"enabled"`
	Transport             string            `json:"transport"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	HasEnvironment        bool              `json:"has_environment"`
	WorkDir               string            `json:"work_dir,omitempty"`
	URL                   string            `json:"url,omitempty"`
	APIKey                *string           `json:"api_key,omitempty"`
	HasAPIKey             bool              `json:"has_api_key"`
	Username              *string           `json:"username,omitempty"`
	Password              *string           `json:"password,omitempty"`
	HasBasicAuth          bool              `json:"has_basic_auth"`
	Headers               map[string]string `json:"headers,omitempty"`
	HasHeaders            bool              `json:"has_headers"`
	StartupTimeoutSeconds int               `json:"startup_timeout_seconds"`
	CallTimeoutSeconds    int               `json:"call_timeout_seconds"`
	TrustAnnotations      bool              `json:"trust_annotations"`
	// AuthMode 是後端實際會送出的驗證方式（none／bearer／basic／headers）。
	// 憑證明文不會回傳，這個欄位讓使用者確認儲存的金鑰確實有被採用。
	AuthMode  string           `json:"auth_mode,omitempty"`
	Status    string           `json:"status,omitempty"`
	Error     string           `json:"error,omitempty"`
	ToolCount int              `json:"tool_count"`
	Tools     []MCPToolSetting `json:"tools,omitempty"`
	// AvailableTools 是 Server 提供的完整工具清單；Tools 只含目前公開給模型的工具。
	AvailableTools []MCPToolSetting `json:"available_tools,omitempty"`
	// EnabledTools 空集合代表全部公開；更新時省略代表保留目前設定。
	EnabledTools *[]string `json:"enabled_tools,omitempty"`
	UpdatedAt    string    `json:"updated_at,omitempty"`
}

type MCPToolSetting struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

type MCPTestResult struct {
	ID        string           `json:"id"`
	OK        bool             `json:"ok"`
	Status    string           `json:"status"`
	ToolCount int              `json:"tool_count"`
	Tools     []MCPToolSetting `json:"tools,omitempty"`
	Error     string           `json:"error,omitempty"`
}
