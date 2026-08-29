package domain

// ServiceSettings 是管理介面可即時調整的服務、介面語言與單次工作上限。
// MaxTokens 與 MaxToolCalls 為 0 時表示不限制；時間上限仍須為正整數。
type ServiceSettings struct {
	ServiceName         string `json:"service_name"`
	UILanguage          string `json:"ui_language"`
	MaxWallClockSeconds int    `json:"max_wall_clock_seconds"`
	MaxTokens           int    `json:"max_tokens"`
	MaxToolCalls        int    `json:"max_tool_calls"`
}

type UpdateServiceSettingsInput struct {
	ServiceName         string  `json:"service_name"`
	UILanguage          *string `json:"ui_language,omitempty"`
	MaxWallClockSeconds *int    `json:"max_wall_clock_seconds,omitempty"`
	MaxTokens           *int    `json:"max_tokens,omitempty"`
	MaxToolCalls        *int    `json:"max_tool_calls,omitempty"`
}
