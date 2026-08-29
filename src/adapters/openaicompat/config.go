// Package openaicompat 實作 OpenAI Chat Completions 相容介面，並在
// ChatGPT/Codex OAuth 模式下切換為 Codex Responses 協定。
package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"AgenticService/src/providerauth"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	CodexResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	CodexUsageEndpoint     = "https://chatgpt.com/backend-api/wham/usage"
)

type Config struct {
	BaseURL                      string                                `json:"base_url"`
	APIKey                       string                                `json:"api_key,omitempty"`
	AuthMode                     string                                `json:"auth_mode,omitempty"`
	OAuth                        providerauth.Config                   `json:"oauth,omitempty"`
	Model                        string                                `json:"model"`
	InstructionRole              string                                `json:"instruction_role,omitempty"`
	ExtraHeaders                 map[string]string                     `json:"extra_headers,omitempty"`
	DisableStreaming             bool                                  `json:"disable_streaming,omitempty"`
	StreamIncludeUsage           bool                                  `json:"stream_include_usage"`
	OmitToolChoice               bool                                  `json:"omit_tool_choice,omitempty"`
	MaxAttempts                  int                                   `json:"max_attempts,omitempty"`
	TimeoutSeconds               int                                   `json:"timeout_seconds,omitempty"`
	ConnectTimeoutSeconds        int                                   `json:"connect_timeout_seconds,omitempty"`
	ResponseHeaderTimeoutSeconds int                                   `json:"response_header_timeout_seconds,omitempty"`
	Timeout                      time.Duration                         `json:"-"`
	Logger                       *slog.Logger                          `json:"-"`
	TokenSource                  func(context.Context) (string, error) `json:"-"`

	// ContextWindow 與 MaxOutputTokens 是這個 Provider 預設模型的限制。
	// 相容端點無法可靠探測，而用模型名稱字串比對又太脆弱，所以由設定宣告；
	// 留空代表未知，context 預算會退回 context.max_estimated_tokens。
	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	// ModelLimits 覆寫個別模型的限制。Workspace、Session 與 Run 都能改用同一 Provider 的其他模型，
	// 只有 Provider 層級的宣告會在這種情況下失準。
	ModelLimits map[string]ModelLimits `json:"model_limits,omitempty"`
}

type ModelLimits struct {
	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

type Diagnostics struct {
	Endpoint           string `json:"endpoint"`
	DefaultModel       string `json:"default_model"`
	InstructionRole    string `json:"instruction_role"`
	Streaming          bool   `json:"streaming"`
	StreamIncludeUsage bool   `json:"stream_include_usage"`
	MaxAttempts        int    `json:"max_attempts"`
	HasAPIKey          bool   `json:"has_api_key"`
	AuthMode           string `json:"auth_mode"`
	ContextWindow      int    `json:"context_window,omitempty"`
	MaxOutputTokens    int    `json:"max_output_tokens,omitempty"`
}

type Model struct {
	endpoint            string
	apiKey              string
	authMode            string
	tokenSource         func(context.Context) (string, error)
	defaultModel        string
	instructionRole     string
	extraHeaders        map[string]string
	disableStreaming    bool
	streamIncludeUsage  bool
	omitToolChoice      bool
	maxAttempts         int
	contextWindow       int
	maxOutputTokens     int
	modelLimits         map[string]ModelLimits
	limitsMu            sync.RWMutex
	reportedModelLimits map[string]ModelLimits
	logger              *slog.Logger
	client              *http.Client
	usageMu             sync.RWMutex
	providerUsage       domain.ProviderUsage
}

func New(config Config) (*Model, error) {
	authMode := strings.ToLower(strings.TrimSpace(config.AuthMode))
	if authMode == "" {
		authMode = "api_key"
	}
	if authMode != "api_key" && authMode != "oauth" {
		return nil, fmt.Errorf("auth_mode must be api_key or oauth")
	}
	if authMode == "oauth" && config.TokenSource == nil {
		return nil, fmt.Errorf("ChatGPT/Codex OAuth token source is required")
	}
	endpoint := CodexResponsesEndpoint
	if authMode != "oauth" {
		var err error
		endpoint, err = completionEndpoint(config.BaseURL)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("provider model is required")
	}
	role := strings.ToLower(strings.TrimSpace(config.InstructionRole))
	if role == "" {
		role = "system"
	}
	if role != "system" && role != "developer" {
		return nil, fmt.Errorf("instruction_role must be system or developer")
	}
	maxAttempts := config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if maxAttempts > 3 {
		return nil, fmt.Errorf("max_attempts cannot exceed 3")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = time.Duration(config.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	connectTimeout := time.Duration(config.ConnectTimeoutSeconds) * time.Second
	if connectTimeout <= 0 {
		connectTimeout = 20 * time.Second
	}
	responseHeaderTimeout := time.Duration(config.ResponseHeaderTimeoutSeconds) * time.Second
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = 2 * time.Minute
	}
	headers := cloneStringMap(config.ExtraHeaders)
	for name, value := range headers {
		if textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)) == "" || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid extra HTTP header %q", name)
		}
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	return &Model{
		endpoint:           endpoint,
		apiKey:             strings.TrimSpace(config.APIKey),
		authMode:           authMode,
		tokenSource:        config.TokenSource,
		defaultModel:       strings.TrimSpace(config.Model),
		instructionRole:    role,
		extraHeaders:       headers,
		disableStreaming:   config.DisableStreaming && authMode != "oauth",
		streamIncludeUsage: config.StreamIncludeUsage,
		omitToolChoice:     config.OmitToolChoice,
		maxAttempts:        maxAttempts,
		contextWindow:      config.ContextWindow,
		maxOutputTokens:    config.MaxOutputTokens,
		modelLimits:        cloneModelLimits(config.ModelLimits),
		logger:             logging.Or(config.Logger),
		client:             &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

func (m *Model) Diagnostics() Diagnostics {
	if m == nil {
		return Diagnostics{}
	}
	return Diagnostics{
		Endpoint:           m.endpoint,
		DefaultModel:       m.defaultModel,
		InstructionRole:    m.instructionRole,
		Streaming:          !m.disableStreaming,
		StreamIncludeUsage: m.streamIncludeUsage,
		MaxAttempts:        m.maxAttempts,
		HasAPIKey:          m.apiKey != "",
		AuthMode:           m.authMode,
		ContextWindow:      m.contextWindow,
		MaxOutputTokens:    m.maxOutputTokens,
	}
}

func (m *Model) applyAuthorization(ctx context.Context, request *http.Request) error {
	if m == nil || request == nil {
		return fmt.Errorf("OpenAI-compatible authorization is unavailable")
	}
	token := strings.TrimSpace(m.apiKey)
	if m.authMode == "oauth" {
		if m.tokenSource == nil {
			return fmt.Errorf("ChatGPT/Codex OAuth token source is unavailable")
		}
		value, err := m.tokenSource(ctx)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(value)
		if token == "" {
			return fmt.Errorf("ChatGPT/Codex OAuth returned an empty access token")
		}
		accountID := codexAccountID(token)
		if accountID == "" {
			return fmt.Errorf("ChatGPT/Codex OAuth token is missing chatgpt_account_id")
		}
		request.Header.Set("chatgpt-account-id", accountID)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// Capabilities 回報指定模型的宣告限制；未指定模型時使用 Provider 預設模型。
func (m *Model) Capabilities(model string) domain.ModelCapabilities {
	if m == nil {
		return domain.ModelCapabilities{}
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = m.defaultModel
	}
	capabilities := domain.ModelCapabilities{
		ContextWindow:   m.contextWindow,
		MaxOutputTokens: m.maxOutputTokens,
		SupportsTools:   true,
		Streaming:       m.authMode == "oauth" || !m.disableStreaming,
	}
	if limits, exists := m.modelLimits[model]; exists {
		if limits.ContextWindow > 0 {
			capabilities.ContextWindow = limits.ContextWindow
		}
		if limits.MaxOutputTokens > 0 {
			capabilities.MaxOutputTokens = limits.MaxOutputTokens
		}
	}
	// Provider 模型目錄回傳的限制是實際後端參考值，優先於人工設定。
	// 人工設定只在目錄沒有提供對應欄位時作為後備。
	m.limitsMu.RLock()
	reported, reportedExists := m.reportedModelLimits[model]
	m.limitsMu.RUnlock()
	if reportedExists {
		if reported.ContextWindow > 0 {
			capabilities.ContextWindow = reported.ContextWindow
		}
		if reported.MaxOutputTokens > 0 {
			capabilities.MaxOutputTokens = reported.MaxOutputTokens
		}
	}
	return capabilities
}

func (m *Model) replaceReportedModelLimits(values map[string]ModelLimits) {
	if m == nil {
		return
	}
	m.limitsMu.Lock()
	m.reportedModelLimits = cloneModelLimits(values)
	m.limitsMu.Unlock()
}

func cloneModelLimits(input map[string]ModelLimits) map[string]ModelLimits {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]ModelLimits, len(input))
	for key, value := range input {
		result[strings.TrimSpace(key)] = value
	}
	return result
}

func completionEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid OpenAI-compatible base URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("OpenAI-compatible base URL must use http or https")
	}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/chat/completions") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	return parsed.String(), nil
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[strings.TrimSpace(key)] = value
	}
	return result
}
