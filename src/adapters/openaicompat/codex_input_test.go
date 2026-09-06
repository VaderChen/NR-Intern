package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

// function_call_output 的 output 是必填，而且「必填」包含不接受空字串。
//
// 實測兩階段：欄位被 omitempty 拿掉時回 400；改成一定送出但值為 "" 時，
// **仍然**回 400 Missing required parameter。所以空結果必須代換成明確文字。
// 這個錯誤要跑到第 40 個項目才炸，前面的工作全部作廢。
func TestCodexInputKeepsOutputFieldForEmptyToolResult(t *testing.T) {
	items := codexInput(domain.ModelRequest{
		History: []domain.Message{
			{Role: "assistant", Content: "呼叫工具", ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "shell_exec"}}},
			{Role: "tool", ToolCallID: "call_1", Content: ""},
		},
		UserPrompt: "繼續",
	})
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(encoded)
	if !strings.Contains(body, `"type":"function_call_output"`) {
		t.Fatalf("應產生 function_call_output：%s", body)
	}
	// 欄位存在還不夠：上游把空字串也當成缺少參數，實測 "output":"" 仍回 400。
	if strings.Contains(body, `"output":""`) {
		t.Fatalf("空字串仍會被上游當成缺少參數，必須代換成明確文字：%s", body)
	}
	if !strings.Contains(body, emptyToolResult) {
		t.Fatalf("空工具結果應代換成 emptyToolResult：%s", body)
	}
}

// 有內容時照常帶上，而且不能因為改用指標而漏掉。
func TestCodexInputCarriesToolOutput(t *testing.T) {
	items := codexInput(domain.ModelRequest{
		History: []domain.Message{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_read"}}},
			{Role: "tool", ToolCallID: "call_2", Content: "檔案內容"},
		},
	})
	encoded, _ := json.Marshal(items)
	if !strings.Contains(string(encoded), `"output":"檔案內容"`) {
		t.Fatalf("工具結果應原樣帶上：%s", encoded)
	}
}

// 其他項目型別不該憑空多出一個空的 output 欄位。
func TestCodexInputOmitsOutputForOtherItemTypes(t *testing.T) {
	items := codexInput(domain.ModelRequest{UserPrompt: "你好"})
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), `"output"`) {
		t.Fatalf("一般訊息不該帶 output：%s", encoded)
	}
}
