package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/domain"
	"AgenticService/src/harness"
	"AgenticService/src/mcpclient"
	"AgenticService/src/memory"
	nativessh "AgenticService/src/tools/native/ssh"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

var Version = "0.1.0"

const (
	DefaultServiceName       = "永不休息的實習生"
	legacyDefaultServiceName = "聰明的實習生"
	DefaultUILanguage        = "auto"
)

type Config struct {
	ServiceName          string                  `json:"service_name"`
	UILanguage           string                  `json:"ui_language,omitempty"`
	NotificationsEnabled bool                    `json:"notifications_enabled,omitempty"`
	ListenAddress        string                  `json:"listen_address"`
	DataDir              string                  `json:"data_dir"`
	APIToken             string                  `json:"api_token,omitempty"`
	AllowedOrigins       []string                `json:"allowed_origins,omitempty"`
	AllowedTools         []string                `json:"allowed_tools,omitempty"`
	AllowElevatedTools   bool                    `json:"allow_elevated_tools"`
	Permissions          domain.PermissionPolicy `json:"permissions"`
	// ToolCallMode 預設採 instruction：由 Harness 強制 LLM 輸出結構化工具指令，
	// 不依賴各 Provider 是否正確轉換 OpenAI tool_calls。
	ToolCallMode string `json:"tool_call_mode"`
	// MaxTurns 是單次 Run 的安全回合上限，和 Context 容量及自動壓縮無關。
	MaxTurns int `json:"max_turns"`
	// MaxAutonomousToolTurns 到達後會停止擴張工具範圍；0 代表不另設固定上限。
	MaxAutonomousToolTurns int `json:"max_autonomous_tool_turns"`
	// MaxCompletionChecks 是模型宣稱完成、但仍有未解決工具失敗時的追問次數上限。
	MaxCompletionChecks int `json:"max_completion_checks"`
	MaxWallClockSeconds int `json:"max_wall_clock_seconds"`
	// MaxTokens 與 MaxToolCalls 設為 0 時不限制，仍可由設定或環境變數重新啟用。
	MaxTokens          int                                     `json:"max_tokens"`
	MaxToolCalls       int                                     `json:"max_tool_calls"`
	MaxToolOutputBytes int                                     `json:"max_tool_output_bytes"`
	MaxFileInputBytes  int                                     `json:"max_file_input_bytes"`
	Context            harness.ContextConfig                   `json:"context"`
	Memory             memory.Config                           `json:"memory"`
	DefaultProviderID  string                                  `json:"default_provider_id"`
	Providers          map[string]ProviderConfig               `json:"providers"`
	ModelPrices        map[string]map[string]domain.ModelPrice `json:"model_prices,omitempty"`
	MCPServers         map[string]mcpclient.ServerConfig       `json:"mcp_servers,omitempty"`
	SSHProfiles        map[string]nativessh.Profile            `json:"ssh_profiles,omitempty"`
	// HTTPFetch 是唯一會離開本機的原生工具，因此邊界獨立於其他工具設定，
	// 且 Enabled 與 AllowPrivateNetworks 可由管理介面即時調整。
	HTTPFetch HTTPFetchConfig `json:"http_fetch"`
	// ExtendedTools 決定要公開精簡工具集還是 AllowedTools 的完整集合。預設精簡：
	// 工具目錄每一輪都會整份進入提示，工具越多，小型與本機模型越慢也越容易挑錯。
	ExtendedTools bool `json:"extended_tools"`
	// ToolRetrieval 讓工具目錄先經檢索再進入提示，內建工具與 MCP 工具都適用。
	// 預設開啟：外掛型 MCP Server 動輒公開上百個工具，整份送出可以讓一句
	// 「HELLO」變成十萬 tokens。
	ToolRetrieval bool `json:"tool_retrieval"`
	// LegacyMCPToolRetrieval 是這個開關的舊名稱。設定檔以 DisallowUnknownFields
	// 解碼，欄位直接改名會讓既有設定檔開不起來，因此保留讀取。
	LegacyMCPToolRetrieval *bool `json:"mcp_tool_retrieval,omitempty"`
	// MemorySpace 是實驗性的跨對話共同記憶開關，預設關閉。
	// 開啟後由 memory.Manager 套用准入、去重、專案 scope、召回視窗與淘汰。
	MemorySpace bool `json:"memory_space"`
}

type HTTPFetchConfig struct {
	Enabled              bool `json:"enabled"`
	AllowPrivateNetworks bool `json:"allow_private_networks"`
	MaxResponseBytes     int  `json:"max_response_bytes"`
	TimeoutSeconds       int  `json:"timeout_seconds"`
	MaxRedirects         int  `json:"max_redirects"`
	// AllowedHosts 非空時只允許清單內的網域與其子網域；BlockedHosts 一律優先拒絕。
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	BlockedHosts []string `json:"blocked_hosts,omitempty"`
}

// ProviderConfig 以 type 作為 adapter 工廠的辨識欄位。每種協定使用自己的具名
// 設定區塊，不把 Chat Completions 與 Codex Responses 的驗證欄位混在一起。
type ProviderConfig struct {
	Type        string `json:"type"`
	DisplayName string `json:"display_name,omitempty"`
	// Enabled=nil 視為啟用，讓既有設定檔與持久化資料可直接升級。
	Enabled              *bool                `json:"enabled,omitempty"`
	OpenAICompatible     *openaicompat.Config `json:"openai_compatible,omitempty"`
	OpenAICodexResponses *openaicompat.Config `json:"openai_codex_responses,omitempty"`
}

func (config ProviderConfig) IsEnabled() bool {
	return config.Enabled == nil || *config.Enabled
}

func DefaultConfig() Config {
	return Config{
		ServiceName:            DefaultServiceName,
		UILanguage:             DefaultUILanguage,
		NotificationsEnabled:   false,
		ListenAddress:          "127.0.0.1:8787",
		DataDir:                filepath.Join("data", "ai-agent"),
		AllowedTools:           []string{"plan_get", "plan_create", "plan_step_update", "directory_list", "directory_create", "file_read", "file_search", "file_compare", "file_write", "file_edit", "document_inspect", "document_read", "document_compare", "document_validate", "document_fonts", "document_create", "document_edit", "document_convert", "pdf_pages", "document_render", "http_fetch", "shell_exec", "wait_for", "ssh_exec", "ssh_wait", "memory_search", "memory_remember", "memory_forget"},
		AllowElevatedTools:     true,
		MaxTurns:               harness.DefaultMaxTurns,
		MaxAutonomousToolTurns: harness.DefaultMaxAutonomousToolTurns,
		// 預設 native：由 Provider 的 tools／tool_calls 欄位傳遞工具，多數推論引擎會以
		// grammar 約束輸出，比要求模型自行輸出 JSON 指令可靠得多（小型與本機模型尤其明顯）。
		// Provider 不支援時可在管理介面切回 instruction。
		ToolCallMode:        string(harness.ToolCallModeNative),
		ToolRetrieval:       true,
		MaxCompletionChecks: harness.DefaultMaxCompletionChecks,
		MaxWallClockSeconds: 2 * 60 * 60,
		MaxTokens:           0,
		MaxToolCalls:        0,
		// 單機 Harness 預設允許 Sandbox 內的寫入與 Shell，但每次高風險工具仍須人工 Approval。
		// 不開放 Client 自行指定 profile；對外部署可將 AllowElevatedTools 關閉以停用全部寫入型工具。
		Permissions: domain.PermissionPolicy{
			DefaultProfile:    domain.DefaultPermissionProfile,
			ElevatedProfiles:  []string{domain.DefaultPermissionProfile, "trusted"},
			AllowClientChoice: false,
		},
		MaxToolOutputBytes: 512 * 1024,
		MaxFileInputBytes:  8 * 1024 * 1024,
		Context: harness.ContextConfig{
			MaxEstimatedTokens:      harness.DefaultMaxEstimatedTokens,
			ReservedOutputTokens:    4_096,
			RetainMessages:          16,
			MaxToolResultCharacters: 24_000,
			MaxSummaryInputChars:    120_000,
			MaxSummaryCharacters:    16_000,
		},
		Memory: memory.Config{
			Enabled:               true,
			AutoRecall:            true,
			RecallLimit:           8,
			MaxInjectedCharacters: 8_000,
			AllowWrites:           true,
		},
		DefaultProviderID: "openai-compatible",
		ModelPrices:       map[string]map[string]domain.ModelPrice{},
		Providers: map[string]ProviderConfig{
			"openai-compatible": {
				Type:        "openai-compatible",
				DisplayName: "OpenAI Compatible",
				OpenAICompatible: &openaicompat.Config{
					BaseURL:                      "https://api.openai.com/v1",
					Model:                        "gpt-4o-mini",
					InstructionRole:              "system",
					StreamIncludeUsage:           true,
					MaxAttempts:                  3,
					TimeoutSeconds:               1800,
					ConnectTimeoutSeconds:        20,
					ResponseHeaderTimeoutSeconds: 120,
				},
			},
		},
		MCPServers:  map[string]mcpclient.ServerConfig{},
		SSHProfiles: map[string]nativessh.Profile{},
		HTTPFetch: HTTPFetchConfig{
			Enabled:              true,
			AllowPrivateNetworks: true,
			MaxResponseBytes:     1024 * 1024,
			TimeoutSeconds:       30,
			MaxRedirects:         5,
		},
	}
}

// LoadConfig 先載入 JSON，再套用 AI_AGENT_* 環境變數。
func LoadConfig(path string) (Config, error) {
	config := DefaultConfig()
	path = strings.TrimSpace(path)
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&config)
		closeErr := file.Close()
		if decodeErr != nil {
			return Config{}, fmt.Errorf("decode config: %w", decodeErr)
		}
		if closeErr != nil {
			return Config{}, fmt.Errorf("close config: %w", closeErr)
		}
	}
	if config.LegacyMCPToolRetrieval != nil {
		config.ToolRetrieval = *config.LegacyMCPToolRetrieval
		config.LegacyMCPToolRetrieval = nil
	}
	applyEnvironment(&config)
	if err := loadPersistedServiceSettings(&config); err != nil {
		return Config{}, err
	}
	if err := loadPersistedProviderSettings(&config); err != nil {
		return Config{}, err
	}
	if err := loadPersistedMCPSettings(&config); err != nil {
		return Config{}, err
	}
	// 環境變數永遠具有最高優先權；持久化設定不得蓋過部署環境注入的值。
	applyEnvironment(&config)
	if err := validateConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func applyEnvironment(config *Config) {
	setString := func(key string, target *string) {
		if value, exists := os.LookupEnv(key); exists {
			*target = strings.TrimSpace(value)
		}
	}
	setString("AI_AGENT_SERVICE_NAME", &config.ServiceName)
	setString("AI_AGENT_UI_LANGUAGE", &config.UILanguage)
	setString("AI_AGENT_LISTEN", &config.ListenAddress)
	setString("AI_AGENT_DATA_DIR", &config.DataDir)
	setString("AI_AGENT_API_TOKEN", &config.APIToken)
	setString("AI_AGENT_DEFAULT_PROVIDER_ID", &config.DefaultProviderID)
	setString("AI_AGENT_TOOL_CALL_MODE", &config.ToolCallMode)
	providerID := strings.TrimSpace(config.DefaultProviderID)
	provider := config.Providers[providerID]
	var llm *openaicompat.Config
	if strings.EqualFold(strings.TrimSpace(provider.Type), "openai-codex-responses") {
		if provider.OpenAICodexResponses == nil {
			provider.OpenAICodexResponses = &openaicompat.Config{}
		}
		llm = provider.OpenAICodexResponses
	} else {
		if provider.OpenAICompatible == nil {
			provider.OpenAICompatible = &openaicompat.Config{}
		}
		llm = provider.OpenAICompatible
		setString("AI_AGENT_LLM_BASE_URL", &llm.BaseURL)
		setString("AI_AGENT_LLM_API_KEY", &llm.APIKey)
		if llm.APIKey == "" {
			setString("OPENAI_API_KEY", &llm.APIKey)
		}
		if _, exists := os.LookupEnv("AI_AGENT_LLM_BASE_URL"); !exists {
			setString("OPENAI_BASE_URL", &llm.BaseURL)
		}
	}
	setString("AI_AGENT_LLM_MODEL", &llm.Model)
	setString("AI_AGENT_LLM_INSTRUCTION_ROLE", &llm.InstructionRole)
	if value, exists := os.LookupEnv("AI_AGENT_LLM_MAX_ATTEMPTS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			llm.MaxAttempts = parsed
		}
	}
	applyBoolEnvironment("AI_AGENT_LLM_DISABLE_STREAMING", &llm.DisableStreaming)
	if strings.EqualFold(strings.TrimSpace(provider.Type), "openai-codex-responses") {
		provider.OpenAICodexResponses = llm
	} else {
		provider.OpenAICompatible = llm
	}
	config.Providers[providerID] = provider
	if value, exists := os.LookupEnv("AI_AGENT_ALLOWED_ORIGINS"); exists {
		config.AllowedOrigins = splitCSV(value)
	}
	if value, exists := os.LookupEnv("AI_AGENT_ALLOWED_TOOLS"); exists {
		config.AllowedTools = splitCSV(value)
	}
	if value, exists := os.LookupEnv("AI_AGENT_ALLOW_ELEVATED_TOOLS"); exists {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			config.AllowElevatedTools = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_TURNS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxTurns = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_AUTONOMOUS_TOOL_TURNS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxAutonomousToolTurns = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_WALL_CLOCK_SECONDS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxWallClockSeconds = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_TOKENS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxTokens = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_TOOL_CALLS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxToolCalls = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_CONTEXT_MAX_TOKENS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.Context.MaxEstimatedTokens = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_MAX_FILE_INPUT_BYTES"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			config.MaxFileInputBytes = parsed
		}
	}
	if value, exists := os.LookupEnv("AI_AGENT_PERMISSION_DEFAULT_PROFILE"); exists {
		config.Permissions.DefaultProfile = strings.TrimSpace(value)
	}
	if value, exists := os.LookupEnv("AI_AGENT_PERMISSION_ELEVATED_PROFILES"); exists {
		config.Permissions.ElevatedProfiles = splitCSV(value)
	}
	applyBoolEnvironment("AI_AGENT_PERMISSION_ALLOW_CLIENT_CHOICE", &config.Permissions.AllowClientChoice)
	applyBoolEnvironment("AI_AGENT_MEMORY_ENABLED", &config.Memory.Enabled)
	applyBoolEnvironment("AI_AGENT_MEMORY_AUTO_RECALL", &config.Memory.AutoRecall)
	applyBoolEnvironment("AI_AGENT_MEMORY_ALLOW_WRITES", &config.Memory.AllowWrites)
}

func validateConfig(config *Config) error {
	config.ServiceName = strings.TrimSpace(config.ServiceName)
	uiLanguage, validUILanguage := normalizeUILanguage(config.UILanguage)
	if !validUILanguage {
		return fmt.Errorf("ui_language must be auto, zh-TW, en, ja or ko")
	}
	config.UILanguage = uiLanguage
	config.ListenAddress = strings.TrimSpace(config.ListenAddress)
	config.DataDir = strings.TrimSpace(config.DataDir)
	if config.ServiceName == "" || config.ListenAddress == "" || config.DataDir == "" {
		return fmt.Errorf("service_name, listen_address and data_dir are required")
	}
	if utf8.RuneCountInString(config.ServiceName) > 80 {
		return fmt.Errorf("service_name must not exceed 80 characters")
	}
	host, _, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen_address must include a valid host and port: %w", err)
	}
	host = strings.Trim(host, "[]")
	loopback := strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	if !loopback && strings.TrimSpace(config.APIToken) == "" {
		return fmt.Errorf("api_token is required when listen_address is not loopback")
	}
	for _, origin := range config.AllowedOrigins {
		if strings.TrimSpace(origin) == "*" && strings.TrimSpace(config.APIToken) == "" {
			return fmt.Errorf("api_token is required when allowed_origins contains wildcard")
		}
	}
	if config.MaxTurns <= 0 || config.MaxTurns > 200 {
		return fmt.Errorf("max_turns must be between 1 and 200")
	}
	if config.MaxAutonomousToolTurns < 0 || config.MaxAutonomousToolTurns >= config.MaxTurns {
		return fmt.Errorf("max_autonomous_tool_turns must be zero (unlimited) or between 1 and max_turns-1")
	}
	config.ToolCallMode = strings.ToLower(strings.TrimSpace(config.ToolCallMode))
	if !harness.ValidToolCallMode(config.ToolCallMode) {
		return fmt.Errorf("tool_call_mode must be %q or %q", harness.ToolCallModeInstruction, harness.ToolCallModeNative)
	}
	if config.MaxCompletionChecks < 0 || config.MaxCompletionChecks > 5 {
		return fmt.Errorf("max_completion_checks must be between 0 and 5")
	}
	if err := validateAdjustableRunLimits(config.MaxWallClockSeconds, config.MaxTokens, config.MaxToolCalls); err != nil {
		return err
	}
	config.Permissions = config.Permissions.Normalize()
	if config.AllowElevatedTools && len(config.Permissions.ElevatedProfiles) == 0 {
		return fmt.Errorf("allow_elevated_tools requires at least one permissions.elevated_profiles entry")
	}
	if config.MaxToolOutputBytes <= 0 {
		config.MaxToolOutputBytes = 512 * 1024
	}
	if config.MaxFileInputBytes <= 0 {
		config.MaxFileInputBytes = 8 * 1024 * 1024
	}
	if config.HTTPFetch.MaxResponseBytes <= 0 {
		config.HTTPFetch.MaxResponseBytes = 1024 * 1024
	}
	if config.HTTPFetch.TimeoutSeconds <= 0 {
		config.HTTPFetch.TimeoutSeconds = 30
	}
	if config.HTTPFetch.TimeoutSeconds > 300 {
		return fmt.Errorf("http_fetch.timeout_seconds must not exceed 300")
	}
	if config.HTTPFetch.MaxRedirects < 0 || config.HTTPFetch.MaxRedirects > 20 {
		return fmt.Errorf("http_fetch.max_redirects must be between 0 and 20")
	}
	config.HTTPFetch.AllowedHosts = normalizeHostList(config.HTTPFetch.AllowedHosts)
	config.HTTPFetch.BlockedHosts = normalizeHostList(config.HTTPFetch.BlockedHosts)
	config.DefaultProviderID = strings.TrimSpace(config.DefaultProviderID)
	if config.DefaultProviderID == "" || len(config.Providers) == 0 {
		return fmt.Errorf("default_provider_id and providers are required")
	}
	defaultProvider, exists := config.Providers[config.DefaultProviderID]
	if !exists {
		return fmt.Errorf("default provider %q is not configured", config.DefaultProviderID)
	}
	if !defaultProvider.IsEnabled() {
		return fmt.Errorf("default provider %q must be enabled", config.DefaultProviderID)
	}
	normalizedMCP := make(map[string]mcpclient.ServerConfig, len(config.MCPServers))
	for id, value := range config.MCPServers {
		if strings.TrimSpace(value.ID) == "" {
			value.ID = id
		}
		normalized, err := value.Normalize()
		if err != nil {
			return err
		}
		if normalized.ID != strings.TrimSpace(id) {
			return fmt.Errorf("MCP map key %q must match id %q", id, normalized.ID)
		}
		normalizedMCP[normalized.ID] = normalized
	}
	config.MCPServers = normalizedMCP
	for id, provider := range config.Providers {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(provider.Type) == "" {
			return fmt.Errorf("provider id and type are required")
		}
		if utf8.RuneCountInString(strings.TrimSpace(provider.DisplayName)) > 80 {
			return fmt.Errorf("provider %q display_name must not exceed 80 characters", id)
		}
		switch strings.ToLower(strings.TrimSpace(provider.Type)) {
		case "openai-compatible":
			if provider.OpenAICompatible == nil {
				return fmt.Errorf("provider %q requires openai_compatible settings", id)
			}
			llm := provider.OpenAICompatible
			applyOpenAICompatibleDefaults(llm)
			if strings.TrimSpace(llm.Model) == "" {
				return fmt.Errorf("provider %q model is required", id)
			}
			if strings.TrimSpace(llm.BaseURL) == "" {
				return fmt.Errorf("provider %q base_url is required for API key authentication", id)
			}
			if llm.MaxAttempts <= 0 || llm.MaxAttempts > 3 {
				return fmt.Errorf("provider %q max_attempts must be between 1 and 3", id)
			}
		case "openai-codex-responses":
			if provider.OpenAICodexResponses == nil {
				return fmt.Errorf("provider %q requires openai_codex_responses settings", id)
			}
			llm := provider.OpenAICodexResponses
			applyOpenAICodexResponsesDefaults(llm)
			if strings.TrimSpace(llm.Model) == "" {
				return fmt.Errorf("provider %q model is required", id)
			}
			if llm.MaxAttempts <= 0 || llm.MaxAttempts > 3 {
				return fmt.Errorf("provider %q max_attempts must be between 1 and 3", id)
			}
		default:
			return fmt.Errorf("unsupported provider type %q for %q", provider.Type, id)
		}
	}
	normalizedPrices := make(map[string]map[string]domain.ModelPrice, len(config.ModelPrices))
	for providerID, models := range config.ModelPrices {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			return fmt.Errorf("model_prices provider id is required")
		}
		if _, exists := config.Providers[providerID]; !exists {
			return fmt.Errorf("model_prices provider %q is not configured", providerID)
		}
		normalizedModels := make(map[string]domain.ModelPrice, len(models))
		for model, price := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				return fmt.Errorf("model_prices.%s model is required", providerID)
			}
			if math.IsNaN(price.InputPerMillion) || math.IsInf(price.InputPerMillion, 0) || price.InputPerMillion < 0 || price.InputPerMillion > 1_000_000 {
				return fmt.Errorf("model_prices.%s.%s input_per_million must be between 0 and 1000000", providerID, model)
			}
			if math.IsNaN(price.OutputPerMillion) || math.IsInf(price.OutputPerMillion, 0) || price.OutputPerMillion < 0 || price.OutputPerMillion > 1_000_000 {
				return fmt.Errorf("model_prices.%s.%s output_per_million must be between 0 and 1000000", providerID, model)
			}
			currency := strings.ToUpper(strings.TrimSpace(price.Currency))
			if currency == "" {
				currency = "USD"
			}
			if currency != "USD" {
				return fmt.Errorf("model_prices.%s.%s currency must be USD", providerID, model)
			}
			price.Currency = currency
			normalizedModels[model] = price
		}
		normalizedPrices[providerID] = normalizedModels
	}
	config.ModelPrices = normalizedPrices
	if config.Context.MaxEstimatedTokens <= 0 {
		return fmt.Errorf("context.max_estimated_tokens must be greater than zero")
	}
	if config.Context.RetainMessages <= 0 {
		return fmt.Errorf("context.retain_messages must be greater than zero")
	}
	if config.Context.ReservedOutputTokens < 0 {
		return fmt.Errorf("context.reserved_output_tokens cannot be negative")
	}
	if config.Context.ReservedOutputTokens == 0 {
		config.Context.ReservedOutputTokens = 4_096
	}
	config.Context.SummaryProviderID = strings.TrimSpace(config.Context.SummaryProviderID)
	config.Context.SummaryModel = strings.TrimSpace(config.Context.SummaryModel)
	if config.Context.SummaryProviderID != "" {
		if _, exists := config.Providers[config.Context.SummaryProviderID]; !exists {
			return fmt.Errorf("context.summary_provider_id %q is not configured", config.Context.SummaryProviderID)
		}
	}
	if config.Memory.Enabled {
		if config.Memory.RecallLimit <= 0 || config.Memory.RecallLimit > 100 {
			return fmt.Errorf("memory.recall_limit must be between 1 and 100")
		}
		if config.Memory.MaxInjectedCharacters <= 0 {
			return fmt.Errorf("memory.max_injected_characters must be greater than zero")
		}
	}
	absolute, err := filepath.Abs(config.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data_dir: %w", err)
	}
	config.DataDir = filepath.Clean(absolute)
	return nil
}

// LeanToolNames 是精簡工具集：一個通用主機工具、最基本的讀取能力、辦公文件
// 產出與計畫控制。其餘工具（寫入、編輯、SSH、記憶、比較等）在管理介面打開
// 「擴充工具集」後才公開。MCP 工具由各自的 Server 設定控制，不受這個清單限制。
//
// 文件工具原本不在精簡集合裡，結果是使用者要一份 Excel，Agent 只剩 shell 可用，
// 就寫了一個沒有 BOM 的 CSV 交差——Excel 打開是亂碼，而且那根本不是使用者要的
// 格式。精簡集合當初是為了控制提示大小，這件事現在由工具檢索處理（目錄只帶進
// 這一輪相關的工具），沒有必要再用「拿掉能力」來換。
var LeanToolNames = []string{
	"shell_exec",
	"file_read",
	"directory_list",
	"file_search",
	"document_inspect",
	"document_read",
	"document_create",
	"document_convert",
	"plan_get",
	"plan_create",
	"plan_step_update",
}

// EffectiveAllowedTools 依「擴充工具集」開關計算本次實際公開的原生工具集合。
// 關閉時取精簡集合與設定檔 allowlist 的交集；allowlist 為空代表原本不設限，
// 此時直接使用精簡集合。
func EffectiveAllowedTools(config Config) []string {
	if config.ExtendedTools {
		return append([]string(nil), config.AllowedTools...)
	}
	if len(config.AllowedTools) == 0 {
		return append([]string(nil), LeanToolNames...)
	}
	configured := make(map[string]struct{}, len(config.AllowedTools))
	for _, name := range config.AllowedTools {
		configured[strings.TrimSpace(name)] = struct{}{}
	}
	result := make([]string, 0, len(LeanToolNames))
	for _, name := range LeanToolNames {
		if _, exists := configured[name]; exists {
			result = append(result, name)
		}
	}
	return result
}

func normalizeHostList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "*.")))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeUILanguage(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return DefaultUILanguage, true
	case "zh", "zh-tw", "zh_tw", "zh-hant", "zh_hant":
		return "zh-TW", true
	case "en", "ja", "ko":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func validateAdjustableRunLimits(maxWallClockSeconds, maxTokens, maxToolCalls int) error {
	if maxWallClockSeconds <= 0 || maxWallClockSeconds > 24*60*60 {
		return fmt.Errorf("max_wall_clock_seconds must be between 1 and 86400")
	}
	if maxTokens < 0 {
		return fmt.Errorf("max_tokens must be zero (unlimited) or greater than zero")
	}
	if maxToolCalls < 0 || maxToolCalls > 10_000 {
		return fmt.Errorf("max_tool_calls must be zero (unlimited) or between 1 and 10000")
	}
	return nil
}

func applyBoolEnvironment(key string, target *bool) {
	if value, exists := os.LookupEnv(key); exists {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			*target = parsed
		}
	}
}

func splitCSV(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
