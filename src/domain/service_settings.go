package domain

// ServiceSettings 是管理介面可即時調整的服務、介面語言與單次工作上限。
// MaxTokens 與 MaxToolCalls 為 0 時表示不限制；時間上限仍須為正整數。
type ServiceSettings struct {
	ServiceName         string            `json:"service_name"`
	UILanguage          string            `json:"ui_language"`
	MaxWallClockSeconds int               `json:"max_wall_clock_seconds"`
	MaxTokens           int               `json:"max_tokens"`
	MaxToolCalls        int               `json:"max_tool_calls"`
	HTTPFetch           HTTPFetchSettings `json:"http_fetch"`
}

// HTTPFetchSettings 是 http_fetch 工具的即時開關。
//
// 其他原生工具都只在本機沙箱內作用，這個工具會把資料送到外部網路，因此除了
// allowlist 與 elevated 權限之外，另外提供管理介面可直接關閉的開關；關閉後工具
// 不會出現在模型可用的工具清單裡。AllowPrivateNetworks 預設關閉，避免 Agent 連到
// loopback、內網或雲端 metadata 位址。
type HTTPFetchSettings struct {
	Enabled              bool `json:"enabled"`
	AllowPrivateNetworks bool `json:"allow_private_networks"`
}

type UpdateServiceSettingsInput struct {
	ServiceName         string  `json:"service_name"`
	UILanguage          *string `json:"ui_language,omitempty"`
	MaxWallClockSeconds *int    `json:"max_wall_clock_seconds,omitempty"`
	MaxTokens           *int    `json:"max_tokens,omitempty"`
	MaxToolCalls        *int    `json:"max_tool_calls,omitempty"`
	// HTTPFetchEnabled 與 HTTPFetchAllowPrivateNetworks 省略時保留目前設定。
	HTTPFetchEnabled              *bool `json:"http_fetch_enabled,omitempty"`
	HTTPFetchAllowPrivateNetworks *bool `json:"http_fetch_allow_private_networks,omitempty"`
}
