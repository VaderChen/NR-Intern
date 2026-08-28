// Package openaicompat 實作單一 OpenAI Chat Completions 相容模型介面。
package openaicompat

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/logging"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	BaseURL                      string            `json:"base_url"`
	APIKey                       string            `json:"api_key,omitempty"`
	Model                        string            `json:"model"`
	InstructionRole              string            `json:"instruction_role,omitempty"`
	ExtraHeaders                 map[string]string `json:"extra_headers,omitempty"`
	DisableStreaming             bool              `json:"disable_streaming,omitempty"`
	StreamIncludeUsage           bool              `json:"stream_include_usage"`
	OmitToolChoice               bool              `json:"omit_tool_choice,omitempty"`
	MaxAttempts                  int               `json:"max_attempts,omitempty"`
	TimeoutSeconds               int               `json:"timeout_seconds,omitempty"`
	ConnectTimeoutSeconds        int               `json:"connect_timeout_seconds,omitempty"`
	ResponseHeaderTimeoutSeconds int               `json:"response_header_timeout_seconds,omitempty"`
	Timeout                      time.Duration     `json:"-"`
	Logger                       *slog.Logger      `json:"-"`

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
	ContextWindow      int    `json:"context_window,omitempty"`
	MaxOutputTokens    int    `json:"max_output_tokens,omitempty"`
}

type Model struct {
	endpoint           string
	apiKey             string
	defaultModel       string
	instructionRole    string
	extraHeaders       map[string]string
	disableStreaming   bool
	streamIncludeUsage bool
	omitToolChoice     bool
	maxAttempts        int
	contextWindow      int
	maxOutputTokens    int
	modelLimits        map[string]ModelLimits
	logger             *slog.Logger
	client             *http.Client
}

func New(config Config) (*Model, error) {
	endpoint, err := completionEndpoint(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("OpenAI-compatible model is required")
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
		defaultModel:       strings.TrimSpace(config.Model),
		instructionRole:    role,
		extraHeaders:       headers,
		disableStreaming:   config.DisableStreaming,
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
		ContextWindow:      m.contextWindow,
		MaxOutputTokens:    m.maxOutputTokens,
	}
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
		Streaming:       !m.disableStreaming,
	}
	if limits, exists := m.modelLimits[model]; exists {
		if limits.ContextWindow > 0 {
			capabilities.ContextWindow = limits.ContextWindow
		}
		if limits.MaxOutputTokens > 0 {
			capabilities.MaxOutputTokens = limits.MaxOutputTokens
		}
	}
	return capabilities
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
