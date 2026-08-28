package harness

import (
	"AgenticService/src/domain"
	"fmt"
	"sort"
	"strings"
)

// DefaultMaxCompletionChecks 是預設的完成度追問次數。
// 設為 0 會停用追問，回到「模型說完成就是完成」。
const DefaultMaxCompletionChecks = 1

const maxFailureSummaryRunes = 400

type failureRecord struct {
	order      int
	toolCallID string
	toolName   string
	summary    string
}

// completionTracker 追蹤本次 run 內尚未解決的工具失敗。
//
// 存在的理由：模型可以在工具失敗之後直接產出一段聽起來已完成的文字，
// 而 Harness 過去只看「這一輪有沒有 tool_calls」就接受它。實際狀態與宣稱
// 不一致時，至少要讓模型面對一次事實，而不是靜默地把失敗當成完成。
//
// 判定完全來自本次 run 自己的執行記錄，不解讀模型文字：
// 某個工具失敗後，同一個工具名稱只要在之後成功執行過一次，就視為已解決。
type completionTracker struct {
	failures map[string]*failureRecord
	sequence int
	checks   int
}

func newCompletionTracker() *completionTracker {
	return &completionTracker{failures: map[string]*failureRecord{}}
}

// observe 記錄一次工具執行結果。同名工具的後續成功會清掉先前的失敗，
// 因為那正是「模型發現錯誤並修正」的正常樣態。
func (t *completionTracker) observe(call domain.ToolCall, result domain.ToolExecution) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return
	}
	if !result.IsError {
		delete(t.failures, name)
		return
	}
	t.sequence++
	t.failures[name] = &failureRecord{
		order:      t.sequence,
		toolCallID: call.ID,
		toolName:   name,
		summary:    truncateMiddle(strings.TrimSpace(result.Content), maxFailureSummaryRunes),
	}
}

func (t *completionTracker) unresolved() []domain.UnresolvedToolFailure {
	if t == nil || len(t.failures) == 0 {
		return nil
	}
	records := make([]*failureRecord, 0, len(t.failures))
	for _, record := range t.failures {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].order < records[j].order })
	result := make([]domain.UnresolvedToolFailure, 0, len(records))
	for _, record := range records {
		result = append(result, domain.UnresolvedToolFailure{
			ToolCallID: record.toolCallID,
			ToolName:   record.toolName,
			Summary:    record.summary,
		})
	}
	return result
}

// challenge 在模型宣稱完成、但仍有未解決的工具失敗時回傳一段追問指示。
// 回傳空字串代表接受這次的完成宣告。追問次數有上限，避免無止境地互相拉扯。
func (t *completionTracker) challenge(maxChecks int) string {
	if t == nil || maxChecks <= 0 || t.checks >= maxChecks {
		return ""
	}
	unresolved := t.unresolved()
	if len(unresolved) == 0 {
		return ""
	}
	t.checks++
	var builder strings.Builder
	builder.WriteString("\n\n<completion_check>\n")
	builder.WriteString("你剛才給出了最終回覆，但這次工作中有工具失敗且沒有後續成功的同名呼叫：\n")
	for _, failure := range unresolved {
		builder.WriteString(fmt.Sprintf("- %s（tool_call_id=%s）：%s\n", failure.ToolName, failure.ToolCallID, failure.Summary))
	}
	builder.WriteString(`
這是本次執行記錄的客觀事實，不是新的使用者指令。請在這一輪二選一：

1. 這些失敗確實影響了工作結果：繼續使用工具處理它們，不要重複同一個必然失敗的呼叫。
2. 這些失敗不影響最終結果（例如已改用其他方式達成、或該步驟本來就非必要）：
   直接給出最終回覆，並明確說明每一項失敗的實際處置與現在的真實狀態。

不得聲稱未經工具結果證實的完成。若工作只完成一部分，明確說出完成了什麼、
還缺什麼、以及原因，而不是給出聽起來完整的總結。
</completion_check>`)
	return builder.String()
}

func (t *completionTracker) completion() *domain.RunCompletion {
	if t == nil {
		return nil
	}
	unresolved := t.unresolved()
	if t.checks == 0 && len(unresolved) == 0 {
		return nil
	}
	return &domain.RunCompletion{
		ChecksPerformed:    t.checks,
		UnresolvedFailures: unresolved,
	}
}
