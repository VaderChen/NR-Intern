package domain

// 工具傳輸／執行狀態與 IsError 分開：錯誤不代表沒有副作用。
const (
	ToolNotDispatched          = "not_dispatched"
	ToolSucceeded              = "succeeded"
	ToolFailed                 = "failed"
	ToolOutcomeUnknown         = "unknown"
	SessionEntryToolDispatched = "tool_dispatched"
)

func ToolExecutionState(details map[string]any) string {
	value, _ := details["execution_state"].(string)
	return value
}
