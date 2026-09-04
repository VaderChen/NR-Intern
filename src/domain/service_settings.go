package domain

// ServiceSettings 是管理介面可即時調整的服務、介面語言與單次工作上限。
// MaxTokens 與 MaxToolCalls 為 0 時表示不限制；時間上限仍須為正整數。
type ServiceSettings struct {
	ServiceName          string            `json:"service_name"`
	UILanguage           string            `json:"ui_language"`
	NotificationsEnabled bool              `json:"notifications_enabled"`
	MaxWallClockSeconds  int               `json:"max_wall_clock_seconds"`
	MaxTokens            int               `json:"max_tokens"`
	MaxToolCalls         int               `json:"max_tool_calls"`
	HTTPFetch            HTTPFetchSettings `json:"http_fetch"`
	// ExtendedTools 關閉時只公開精簡工具集。工具目錄會整份進入每一輪的提示，
	// 小型或本機模型在十幾個工具之間挑選既慢又容易挑錯；預設精簡，需要完整能力
	// （文件處理、SSH、寫入型工具等）時再由管理介面打開。
	ExtendedTools bool `json:"extended_tools"`
	// ToolCallMode 是工具呼叫協定：native 使用 OpenAI-compatible tool_calls 欄位，
	// instruction 由 Harness 要求模型輸出結構化 JSON 指令。Provider 不支援原生工具
	// 呼叫時才需要切到 instruction。
	ToolCallMode string `json:"tool_call_mode"`
	// ToolRetrieval 開啟時，工具目錄只有與本次需求相關的工具會進入提示，內建
	// 工具與 MCP 工具都適用；其餘工具仍可由模型以 find_tools 取回後呼叫，沒有
	// 任何工具被停用。關閉後整份目錄會進入每一次請求。
	ToolRetrieval bool `json:"tool_retrieval"`
	// MemorySpace 是實驗性功能：跨對話的共同記憶。開啟後套用准入過濾、去重、
	// 專案 scope、召回視窗與淘汰；關閉時維持既有長期記憶行為。
	// 機制說明見 docs/ai-agent/MEMORY_SPACE.md。
	MemorySpace bool `json:"memory_space"`
	// MemoryIsolatedProjects 控制能不能「新建」記憶體隔離專案，預設開啟。
	// 關閉只影響新建；既有的隔離專案仍照常運作，否則一次誤關就會讓使用者
	// 現有的專案突然不能用。
	MemoryIsolatedProjects bool `json:"memory_isolated_projects"`
}

// HTTPFetchSettings 是 http_fetch 工具的即時開關。
//
// 其他原生工具都只在本機沙箱內作用，這個工具會把資料送到外部網路，因此除了
// allowlist 與 elevated 權限之外，另外提供管理介面可直接關閉的開關；關閉後工具
// 不會出現在模型可用的工具清單裡。AllowPrivateNetworks 預設開啟，允許 Agent 連到
// localhost、loopback、內網或雲端 metadata 位址；需要時可由管理介面關閉。
type HTTPFetchSettings struct {
	Enabled              bool `json:"enabled"`
	AllowPrivateNetworks bool `json:"allow_private_networks"`
}

type UpdateServiceSettingsInput struct {
	ServiceName          string  `json:"service_name"`
	UILanguage           *string `json:"ui_language,omitempty"`
	NotificationsEnabled *bool   `json:"notifications_enabled,omitempty"`
	MaxWallClockSeconds  *int    `json:"max_wall_clock_seconds,omitempty"`
	MaxTokens            *int    `json:"max_tokens,omitempty"`
	MaxToolCalls         *int    `json:"max_tool_calls,omitempty"`
	// HTTPFetchEnabled 與 HTTPFetchAllowPrivateNetworks 省略時保留目前設定。
	HTTPFetchEnabled              *bool   `json:"http_fetch_enabled,omitempty"`
	HTTPFetchAllowPrivateNetworks *bool   `json:"http_fetch_allow_private_networks,omitempty"`
	ExtendedTools                 *bool   `json:"extended_tools,omitempty"`
	ToolCallMode                  *string `json:"tool_call_mode,omitempty"`
	ToolRetrieval                 *bool   `json:"tool_retrieval,omitempty"`
	MemorySpace                   *bool   `json:"memory_space,omitempty"`
	MemoryIsolatedProjects        *bool   `json:"memory_isolated_projects,omitempty"`
}
