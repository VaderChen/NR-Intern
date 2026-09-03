package harness

import (
	"context"
	"testing"

	"AgenticService/src/domain"
)

// 實測的失敗：模型送出 cell "Sheet1!A1" 被工具拒絕，接著把它改成
// {"cell":"A1","sheet":"Sheet1"}——完全正確的修法——卻被 loop guard 判成
// 「相同策略」不准執行，連兩次正確答案都被擋下，最後只好用 shell 寫出空殼檔案。
//
// 錯誤指名內容欄位時，改好內容就是不同的嘗試，必須放行。
func TestLoopGuardAllowsCorrectedPayloadAfterValidationFailure(t *testing.T) {
	sessions := newMemorySessions(testSession())
	badCells := []any{map[string]any{"cell": "Sheet1!A1", "value": "總覽"}}
	goodCells := []any{map[string]any{"cell": "A1", "sheet": "Sheet1", "value": "總覽"}}
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "document_create", Arguments: map[string]any{"path": "a.xlsx", "cell_updates": badCells}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "document_create", Arguments: map[string]any{"path": "a.xlsx", "cell_updates": badCells}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_3", Name: "document_create", Arguments: map[string]any{"path": "a.xlsx", "cell_updates": goodCells}}}},
		{Content: "已建立 a.xlsx。"},
	}}
	executed := []string{}
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "document_create", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed = append(executed, call.ID)
			cells, _ := call.Arguments["cell_updates"].([]any)
			first, _ := cells[0].(map[string]any)
			if reference, _ := first["cell"].(string); reference == "Sheet1!A1" {
				return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name,
					Content: `cell_updates[0]: cell "Sheet1!A1" is not a valid reference such as A1`, IsError: true}, nil
			}
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "ok"}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.Budget.MaxTurns = 6

	result, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "轉成 Excel"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(executed) != 3 {
		t.Fatalf("executed %v; the corrected payload must be allowed to run", executed)
	}
	if result.Message.Content != "已建立 a.xlsx。" {
		t.Fatalf("result = %q", result.Message.Content)
	}
}

// 錯誤與內容無關時（例如「檔案已存在」），換一份內容再送一次仍要被擋——
// 那才是原本要防的原地打轉。
func TestLoopGuardStillBlocksContentOnlyRetriesForUnrelatedErrors(t *testing.T) {
	sessions := newMemorySessions(testSession())
	model := &scriptedModel{responses: []domain.ModelResponse{
		{ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_write", Arguments: map[string]any{"path": "a.md", "content": "draft 1", "overwrite": false}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_write", Arguments: map[string]any{"path": "a.md", "content": "draft 2", "overwrite": false}}}},
		{ToolCalls: []domain.ToolCall{{ID: "call_3", Name: "file_write", Arguments: map[string]any{"path": "a.md", "content": "draft 3", "overwrite": false}}}},
		{Content: "無法寫入。"},
	}}
	executed := 0
	tools := &fakeTools{
		definitions: []domain.ToolDefinition{{Name: "file_write", Capabilities: []string{"atomic-replace"}}},
		execute: func(_ context.Context, _ domain.Session, call domain.ToolCall) (domain.ToolExecution, error) {
			executed++
			return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: "file exists", IsError: true}, nil
		},
	}
	runner := newTestRunner(sessions, model, tools)
	runner.Budget.MaxTurns = 6

	if _, err := runner.Run(context.Background(), Input{Session: testSession(), UserInput: "寫檔"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed != 2 {
		t.Fatalf("executed = %d, want the third content-only retry to be blocked", executed)
	}
}
