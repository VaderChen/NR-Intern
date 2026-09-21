package domain

// ToolOperationState 依派送與結果證據分類；錯誤或逾時本身不能證明沒有副作用。
// completed 指收到確定的執行結果，不是本機等待已結束。
func ToolOperationState(dispatched, completed, failed bool) string {
	if !dispatched {
		return ToolNotDispatched
	}
	if !completed {
		return ToolOutcomeUnknown
	}
	if failed {
		return ToolFailed
	}
	return ToolSucceeded
}
