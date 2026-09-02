package domain

import (
	"strings"
	"time"
)

const (
	SessionEntryMessage             = "message"
	SessionEntryOperationStarted    = "operation_started"
	SessionEntryOperationFinished   = "operation_finished"
	SessionEntryTurnStarted         = "turn_started"
	SessionEntryTurnFinished        = "turn_finished"
	SessionEntryToolStarted         = "tool_started"
	SessionEntryToolFinished        = "tool_finished"
	SessionEntryCompaction          = "compaction"
	SessionEntryMemoryRecall        = "memory_recall"
	SessionEntryCompletionCheck     = "completion_check"
	SessionEntryPlanCompletionCheck = "plan_completion_check"
	// SessionEntryMessagesRetracted 標記「從某則訊息起的內容不再屬於這個對話」。
	//
	// 重新提問要把上一次的回答與過程清掉，但 transcript 是 append-only 的稽核
	// 記錄，刪檔案會讓「當時到底發生什麼」永遠查不回來。因此改成追加一筆撤回
	// 記錄：組裝對話時略過被撤回的區段，原始內容仍完整留在 transcript 裡。
	SessionEntryMessagesRetracted = "messages_retracted"
)

const (
	OperationStatusRunning   = "running"
	OperationStatusCompleted = "completed"
	OperationStatusFailed    = "failed"
	OperationStatusCanceled  = "canceled"
)

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// Add 累加 Provider 回報的單輪用量；Provider 偶爾不提供 total_tokens，
// 此時用 input 與 output 補足，讓預算與歷史統計都維持同一套計算方式。
func (usage *Usage) Add(value Usage) {
	if usage == nil {
		return
	}
	usage.InputTokens += value.InputTokens
	usage.OutputTokens += value.OutputTokens
	if value.TotalTokens > 0 {
		usage.TotalTokens += value.TotalTokens
	} else {
		usage.TotalTokens += value.InputTokens + value.OutputTokens
	}
}

func (usage Usage) Total() int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens
}

// ModelPrice 是可由設定檔提供的模型單價，單位為每一百萬 token 的美元。
// 價格不寫死在程式中，因為同一模型可能經由不同 Provider 或自架服務計價。
type ModelPrice struct {
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
	Currency         string  `json:"currency,omitempty"`
}

// RunUsage 是單一 Run 的最終用量快照。EstimatedCostUSD 使用指標區分
// 「尚未設定價格」與確實算出的零成本；沒有價格時不應猜測金額。
type RunUsage struct {
	ProviderID       string   `json:"provider_id,omitempty"`
	Model            string   `json:"model,omitempty"`
	InputTokens      int      `json:"input_tokens,omitempty"`
	OutputTokens     int      `json:"output_tokens,omitempty"`
	TotalTokens      int      `json:"total_tokens,omitempty"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd,omitempty"`
	Currency         string   `json:"currency,omitempty"`
}

// SessionUsage 是依 Session 所有 Run 即時彙總的統計，不作為 Session metadata
// 落盤；ByModel 讓使用者能辨識不同 Provider／模型的用量來源。
type SessionUsage struct {
	InputTokens      int        `json:"input_tokens"`
	OutputTokens     int        `json:"output_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	EstimatedCostUSD *float64   `json:"estimated_cost_usd,omitempty"`
	Currency         string     `json:"currency,omitempty"`
	ByModel          []RunUsage `json:"by_model,omitempty"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Label       string         `json:"label,omitempty"`
	Version     string         `json:"version,omitempty"`
	Category    string         `json:"category,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	// OutputSchema 與 ServerInstructions 主要由 MCP 使用。OutputSchema 讓
	// instruction tool protocol 能知道工具回傳的資料形狀；ServerInstructions
	// 保留 MCP initialize 的使用提示，由 Harness 以「外部資料」邊界提供給模型。
	OutputSchema       map[string]any `json:"output_schema,omitempty"`
	ServerInstructions string         `json:"server_instructions,omitempty"`
	Platforms          []string       `json:"platforms,omitempty"`
	Capabilities       []string       `json:"capabilities,omitempty"`
	ReadOnly           bool           `json:"read_only,omitempty"`
	RequiresPermission bool           `json:"requires_permission,omitempty"`
}

type ToolCatalogEntry struct {
	Definition        ToolDefinition `json:"definition"`
	Allowed           bool           `json:"allowed"`
	Available         bool           `json:"available"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
}

type ToolExecution struct {
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Content    string         `json:"content"`
	Details    map[string]any `json:"details,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
	Terminate  bool           `json:"terminate,omitempty"`
}

type ModelRequest struct {
	SessionID    string `json:"session_id"`
	ProviderID   string `json:"provider_id,omitempty"`
	Model        string `json:"model,omitempty"`
	ThinkingMode string `json:"thinking_mode,omitempty"`
	ToolChoice   string `json:"tool_choice,omitempty"`
	// SystemPrompt、HostPrompt、ToolPrompt、PhasePrompt、ContextPrompt、History 與 UserPrompt
	// 保持獨立，讓 Provider adapter 以不同訊息區段編碼，不把權限、工具資料、
	// Harness 階段控制與使用者內容混在一起。
	SystemPrompt  string           `json:"system_prompt"`
	HostPrompt    string           `json:"host_prompt,omitempty"`
	ToolPrompt    string           `json:"tool_prompt,omitempty"`
	PhasePrompt   string           `json:"phase_prompt,omitempty"`
	ContextPrompt string           `json:"context_prompt,omitempty"`
	History       []Message        `json:"history,omitempty"`
	UserPrompt    string           `json:"user_prompt,omitempty"`
	Tools         []ToolDefinition `json:"tools,omitempty"`
	Metadata      map[string]any   `json:"metadata,omitempty"`
}

type ModelResponse struct {
	ProviderID        string     `json:"provider_id,omitempty"`
	ProviderRequestID string     `json:"provider_request_id,omitempty"`
	Model             string     `json:"model,omitempty"`
	Content           string     `json:"content,omitempty"`
	Reasoning         string     `json:"reasoning,omitempty"`
	ToolCalls         []ToolCall `json:"tool_calls,omitempty"`
	StopReason        string     `json:"stop_reason,omitempty"`
	Usage             Usage      `json:"usage,omitempty"`
}

const (
	ModelEventTextDelta     = "text_delta"
	ModelEventThinkingDelta = "thinking_delta"
	ModelEventToolCallDelta = "tool_call_delta"
	ModelEventUsage         = "usage"
	ModelEventProgress      = "progress"
)

// ModelEvent 是 Provider 串流過程的結構化事件。
//
// 這裡刻意不是單純的 {type, text}：工具參數在 adapter 內部累積完才一次吐出時，
// 前端無法顯示工具呼叫的形成過程；thinking 與 usage 同樣需要能被獨立辨識。
// Delta 對 text_delta、thinking_delta、progress 是增量文字，
// 對 tool_call_delta 則是該次工具參數的片段。
type ModelEvent struct {
	Type     string         `json:"type"`
	Delta    string         `json:"delta,omitempty"`
	ToolCall *ToolCallDelta `json:"tool_call,omitempty"`
	Usage    *Usage         `json:"usage,omitempty"`
}

// ToolCallDelta 描述目前正在累積的工具呼叫。Index 是同一則訊息內的工具呼叫序號，
// ID 與 Name 在 Provider 尚未送出前可能為空。
type ToolCallDelta struct {
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
}

// ModelCapabilities 是後端設定宣告的模型限制。
//
// OpenAI-compatible 端點無法可靠地被探測，用模型名稱字串比對又太脆弱，
// 因此這些值由設定提供；ContextWindow 為 0 代表未知，此時退回設定的固定預算。
type ModelCapabilities struct {
	ContextWindow   int  `json:"context_window,omitempty"`
	MaxOutputTokens int  `json:"max_output_tokens,omitempty"`
	SupportsTools   bool `json:"supports_tools"`
	Streaming       bool `json:"streaming"`
}

// UnresolvedToolFailure 是一次工具失敗，且同名工具在之後沒有成功執行過。
type UnresolvedToolFailure struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Summary    string `json:"summary,omitempty"`
}

// RunCompletion 記錄這次 run 的完成度判定結果。
//
// 沒有它時，「工具全部成功後完成」與「工具失敗後照樣宣稱完成」在 API 上
// 完全無法區分，兩者都只是 status=completed。
type RunCompletion struct {
	ChecksPerformed    int                     `json:"checks_performed"`
	UnresolvedFailures []UnresolvedToolFailure `json:"unresolved_failures,omitempty"`
}

// RetractedFromMessageID 取出撤回記錄指向的起點訊息 ID；不是撤回記錄時回傳空字串。
// 讀取 transcript 的每一端都要用同一套判讀，否則模型看到的對話會跟畫面不一致。
func RetractedFromMessageID(entry SessionEntry) string {
	if entry.Type != SessionEntryMessagesRetracted {
		return ""
	}
	value, _ := entry.Data["from_message_id"].(string)
	return strings.TrimSpace(value)
}

type SessionEntry struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Sequence  int64          `json:"sequence"`
	Type      string         `json:"type"`
	Message   *Message       `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
