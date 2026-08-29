package domain

// ProviderSettings 是管理介面可讀取的 Provider 設定集合。
// Provider 憑證只以 HasAPIKey 呈現，API 不會回傳明文。
type ProviderSettings struct {
	DefaultProviderID string            `json:"default_provider_id"`
	Providers         []ProviderSetting `json:"providers"`
}

// UpdateProviderSettingsInput 以完整集合更新 Provider。
// 完整集合可讓 default provider、刪除與未來 Provider 類型保持一致性驗證。
type UpdateProviderSettingsInput struct {
	DefaultProviderID string            `json:"default_provider_id"`
	Providers         []ProviderSetting `json:"providers"`
}

// ProviderSetting 以 Type 決定具名設定區塊；目前只實作 openai-compatible。
// 後續新增 Provider 類型時不需要改動 Workspace、Session 或 Harness schema。
type ProviderSetting struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Enabled=nil 供舊版 Client 省略欄位時維持啟用；管理 API 回應一律提供明確值。
	Enabled          *bool                            `json:"enabled,omitempty"`
	OpenAICompatible *OpenAICompatibleProviderSetting `json:"openai_compatible,omitempty"`
}

// OpenAICompatibleProviderSetting 同時作為管理 API 的讀取模型與寫入模型。
// APIKey=nil 代表保留現有值，指向空字串則代表明確清除金鑰。
type OpenAICompatibleProviderSetting struct {
	BaseURL                      string  `json:"base_url"`
	APIKey                       *string `json:"api_key,omitempty"`
	HasAPIKey                    bool    `json:"has_api_key"`
	Model                        string  `json:"model"`
	InstructionRole              string  `json:"instruction_role"`
	DisableStreaming             bool    `json:"disable_streaming"`
	StreamIncludeUsage           bool    `json:"stream_include_usage"`
	OmitToolChoice               bool    `json:"omit_tool_choice"`
	MaxAttempts                  int     `json:"max_attempts"`
	TimeoutSeconds               int     `json:"timeout_seconds"`
	ConnectTimeoutSeconds        int     `json:"connect_timeout_seconds"`
	ResponseHeaderTimeoutSeconds int     `json:"response_header_timeout_seconds"`
	ContextWindow                int     `json:"context_window,omitempty"`
	MaxOutputTokens              int     `json:"max_output_tokens,omitempty"`
}

// ProviderModels 是 Provider 可使用的模型目錄。
// Models 可能為空；部分 OpenAI-compatible 服務不提供 /models 端點。
type ProviderModels struct {
	ProviderID string   `json:"provider_id"`
	Models     []string `json:"models"`
}

// ProviderTestResult 是管理介面的最小實際模型請求結果。
// 模型目錄載入失敗不會推翻已成功的 Chat Completions 測試，而會放在 Warning。
type ProviderTestResult struct {
	OK                   bool     `json:"ok"`
	ToolCalling          bool     `json:"tool_calling"`
	ProviderID           string   `json:"provider_id"`
	Model                string   `json:"model"`
	ProviderRequestID    string   `json:"provider_request_id,omitempty"`
	ResponsePreview      string   `json:"response_preview,omitempty"`
	DurationMilliseconds int64    `json:"duration_milliseconds"`
	Usage                Usage    `json:"usage"`
	Models               []string `json:"models,omitempty"`
	Warning              string   `json:"warning,omitempty"`
}
