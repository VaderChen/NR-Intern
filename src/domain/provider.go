package domain

// ProviderSettings 是管理介面可讀取的 Provider 設定集合。
// Provider 憑證只以 HasAPIKey／HasOAuthToken 呈現，API 不會回傳明文。
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

// ProviderSetting 以 Type 決定具名設定區塊；Chat Completions 與 Codex Responses
// 分開建模，避免混用不相容的驗證與請求欄位。
// 後續新增 Provider 類型時不需要改動 Workspace、Session 或 Harness schema。
type ProviderSetting struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Type        string `json:"type"`
	// Enabled=nil 供舊版 Client 省略欄位時維持啟用；管理 API 回應一律提供明確值。
	Enabled              *bool                                `json:"enabled,omitempty"`
	OpenAICompatible     *OpenAICompatibleProviderSetting     `json:"openai_compatible,omitempty"`
	OpenAICodexResponses *OpenAICodexResponsesProviderSetting `json:"openai_codex_responses,omitempty"`
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

// OpenAICodexResponsesProviderSetting 使用 ChatGPT/Codex OAuth 與固定的
// Codex Responses 端點。OAuth Token 僅以 HasOAuthToken 呈現。
type OpenAICodexResponsesProviderSetting struct {
	HasOAuthToken                bool   `json:"has_oauth_token"`
	Model                        string `json:"model"`
	MaxAttempts                  int    `json:"max_attempts"`
	TimeoutSeconds               int    `json:"timeout_seconds"`
	ConnectTimeoutSeconds        int    `json:"connect_timeout_seconds"`
	ResponseHeaderTimeoutSeconds int    `json:"response_header_timeout_seconds"`
	ContextWindow                int    `json:"context_window,omitempty"`
	MaxOutputTokens              int    `json:"max_output_tokens,omitempty"`
}

// ProviderOAuthStartResult 僅包含啟動互動驗證所需的公開資料。
// access token、refresh token 與 PKCE verifier 永遠不會透過管理 API 回傳。
type ProviderOAuthStartResult struct {
	ProviderID       string `json:"provider_id"`
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorization_url"`
	CallbackURI      string `json:"callback_uri"`
	BrowserOpened    bool   `json:"browser_opened"`
	ExpiresAt        string `json:"expires_at"`
}

// ProviderOAuthStatus 是可安全顯示於管理頁的 ChatGPT/Codex OAuth 連線狀態。
type ProviderOAuthStatus struct {
	ProviderID   string `json:"provider_id"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
	AccountName  string `json:"account_name,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// ProviderModels 是 Provider 可使用的模型目錄。
// Models 可能為空；Codex Responses 與部分相容服務不提供模型目錄。
type ProviderModels struct {
	ProviderID string   `json:"provider_id"`
	Models     []string `json:"models"`
}

// ProviderUsageWindow 是 Provider 回傳的單一配額視窗快照。
// Available=false 代表上游沒有提供資料，前端應顯示「-」，不可推測為 100%。
type ProviderUsageWindow struct {
	Available        bool    `json:"available"`
	RemainingPercent float64 `json:"remaining_percent"`
	WindowMinutes    int     `json:"window_minutes,omitempty"`
	ResetAt          string  `json:"reset_at,omitempty"`
}

// ProviderUsage 保存每個 Provider 最近一次實際回應的用量標頭。
// Codex primary／secondary 視窗分別呈現為 5 小時與 7 天；未回傳的視窗保持不可用。
type ProviderUsage struct {
	ProviderID string              `json:"provider_id"`
	UpdatedAt  string              `json:"updated_at,omitempty"`
	FiveHour   ProviderUsageWindow `json:"five_hour"`
	SevenDay   ProviderUsageWindow `json:"seven_day"`
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
