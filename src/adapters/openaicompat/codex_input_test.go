package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

// function_call_output 的 output 是必填。工具結果為空字串時（沒有輸出的指令、
// 沒有命中的搜尋）若讓欄位消失，上游會回
// 「400 Missing required parameter: 'input[N].output'」，整個 Run 就死在那裡——
// 實際發生過，而且要跑到第 40 個項目才炸，前面的工作全部作廢。
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
	if !strings.Contains(body, `"output":""`) {
		t.Fatalf("空工具結果仍必須送出 output 欄位：%s", body)
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
