package bootstrap

import (
	"AgenticService/src/adapters/openaicompat"
	"AgenticService/src/domain"
	"AgenticService/src/harness"
	"AgenticService/src/memory"
	nativessh "AgenticService/src/tools/native/ssh"
	"encoding/json"
	"fmt"
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
	ServiceName        string                  `json:"service_name"`
	UILanguage         string                  `json:"ui_language,omitempty"`
	ListenAddress      string                  `json:"listen_address"`
	DataDir            string                  `json:"data_dir"`
	APIToken           string                  `json:"api_token,omitempty"`
	AllowedOrigins     []string                `json:"allowed_origins,omitempty"`
	AllowedTools       []string                `json:"allowed_tools,omitempty"`
	AllowElevatedTools bool                    `json:"allow_elevated_tools"`
	Permissions        domain.PermissionPolicy `json:"permissions"`
	// ToolCallMode 預設採 instruction：由 Harness 強制 LLM 輸出結構化工具指令，
	// 不依賴各 Provider 是否正確轉換 OpenAI tool_calls。
	ToolCallMode string `json:"tool_call_mode"`
	// MaxTurns 是單次 Run 的安全回合上限，和 Context 容量及自動壓縮無關。
	MaxTurns int `json:"max_turns"`
	// MaxAutonomousToolTurns 到達後會停止擴張工具範圍，強制整理最終答案。
	MaxAutonomousToolTurns int `json:"max_autonomous_tool_turns"`
	// MaxCompletionChecks 是模型宣稱完成、但仍有未解決工具失敗時的追問次數上限。
	MaxCompletionChecks int `json:"max_completion_checks"`
	MaxWallClockSeconds int `json:"max_wall_clock_seconds"`
	// MaxTokens 與 MaxToolCalls 設為 0 時不限制，仍可由設定或環境變數重新啟用。
	MaxTokens          int                          `json:"max_tokens"`
	MaxToolCalls       int                          `json:"max_tool_calls"`
	MaxToolOutputBytes int                          `json:"max_tool_output_bytes"`
	MaxFileInputBytes  int                          `json:"max_file_input_bytes"`
	Context            harness.ContextConfig        `json:"context"`
	Memory             memory.Config                `json:"memory"`
	DefaultProviderID  string                       `json:"default_provider_id"`
	Providers          map[string]ProviderConfig    `json:"providers"`
	SSHProfiles        map[string]nativessh.Profile `json:"ssh_profiles,omitempty"`
}

// ProviderConfig 以 type 作為 adapter 工廠的辨識欄位；目前只實作 openai-compatible。
// 後續 Provider 類型應新增自己的具名設定區塊，不改動 Harness 或 Workspace schema。
type ProviderConfig struct {
	Type string `json:"type"`
	// Enabled=nil 視為啟用，讓既有設定檔與持久化資料可直接升級。
	Enabled          *bool                `json:"enabled,omitempty"`
	OpenAICompatible *openaicompat.Config `json:"openai_compatible,omitempty"`
}

func (config ProviderConfig) IsEnabled() bool {
	return config.Enabled == nil || *config.Enabled
}

func DefaultConfig() Config {
	return Config{
		ServiceName:            DefaultServiceName,
		UILanguage:             DefaultUILanguage,
		ListenAddress:          "127.0.0.1:8787",
		DataDir:                filepath.Join("data", "ai-agent"),
		AllowedTools:           []string{"plan_get", "plan_create", "plan_step_update", "directory_list", "directory_create", "file_read", "file_search", "file_compare", "file_write", "file_edit", "document_inspect", "document_read", "shell_exec", "ssh_exec", "memory_search", "memory_remember", "memory_forget"},
		AllowElevatedTools:     true,
		MaxTurns:               harness.DefaultMaxTurns,
		MaxAutonomousToolTurns: harness.DefaultMaxAutonomousToolTurns,
		ToolCallMode:           string(harness.ToolCallModeInstruction),
		MaxCompletionChecks:    harness.DefaultMaxCompletionChecks,
		MaxWallClockSeconds:    2 * 60 * 60,
		MaxTokens:              0,
		MaxToolCalls:           0,
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
		Providers: map[string]ProviderConfig{
			"openai-compatible": {
				Type: "openai-compatible",
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
		SSHProfiles: map[string]nativessh.Profile{},
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
	applyEnvironment(&config)
	if err := loadPersistedServiceSettings(&config); err != nil {
		return Config{}, err
	}
	if err := loadPersistedProviderSettings(&config); err != nil {
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
	if provider.OpenAICompatible == nil {
		provider.OpenAICompatible = &openaicompat.Config{}
	}
	llm := provider.OpenAICompatible
	setString("AI_AGENT_LLM_BASE_URL", &llm.BaseURL)
	setString("AI_AGENT_LLM_API_KEY", &llm.APIKey)
	setString("AI_AGENT_LLM_MODEL", &llm.Model)
	setString("AI_AGENT_LLM_INSTRUCTION_ROLE", &llm.InstructionRole)
	if value, exists := os.LookupEnv("AI_AGENT_LLM_MAX_ATTEMPTS"); exists {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			llm.MaxAttempts = parsed
		}
	}
	applyBoolEnvironment("AI_AGENT_LLM_DISABLE_STREAMING", &llm.DisableStreaming)
	if llm.APIKey == "" {
		setString("OPENAI_API_KEY", &llm.APIKey)
	}
	if _, exists := os.LookupEnv("AI_AGENT_LLM_BASE_URL"); !exists {
		setString("OPENAI_BASE_URL", &llm.BaseURL)
	}
	provider.OpenAICompatible = llm
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
	if config.MaxAutonomousToolTurns <= 0 || config.MaxAutonomousToolTurns >= config.MaxTurns {
		return fmt.Errorf("max_autonomous_tool_turns must be between 1 and max_turns-1")
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
	for id, provider := range config.Providers {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(provider.Type) == "" {
			return fmt.Errorf("provider id and type are required")
		}
		switch strings.ToLower(strings.TrimSpace(provider.Type)) {
		case "openai-compatible":
			if provider.OpenAICompatible == nil {
				return fmt.Errorf("provider %q requires openai_compatible settings", id)
			}
			llm := provider.OpenAICompatible
			if strings.TrimSpace(llm.BaseURL) == "" || strings.TrimSpace(llm.Model) == "" {
				return fmt.Errorf("provider %q base_url and model are required", id)
			}
			if llm.MaxAttempts <= 0 || llm.MaxAttempts > 3 {
				return fmt.Errorf("provider %q max_attempts must be between 1 and 3", id)
			}
		default:
			return fmt.Errorf("unsupported provider type %q for %q", provider.Type, id)
		}
	}
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
