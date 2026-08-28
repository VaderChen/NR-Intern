package ports

import "AgenticService/src/domain"

// TokenCounter 估算送入模型的 token 數。
//
// 實作必須分別處理 CJK 與 ASCII，不得假設固定的 characters/token 比例：
// 以英文為前提的 characters/4 對中文會低估 3～4 倍，使 context 預算失效。
// 估算偏高只會提早 compaction，估算偏低則會讓 Provider 直接拒絕請求，
// 因此實作在不確定時應該偏保守。
type TokenCounter interface {
	EstimateText(text string) int
	EstimateMessages(messages []domain.Message) int
	EstimateTools(definitions []domain.ToolDefinition) int
}
