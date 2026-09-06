package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"strings"
	"testing"
)

// 空的工具結果不能原樣送出，Chat Completions 這條路一樣不行。
//
// 實測案例：file_read 讀到一個 0 byte 的 stderr.log，工具正常完成、content 是
// 空字串。這份請求經過相容代理轉成 Responses 協定時，代理的 output 欄位帶著
// omitempty，空字串讓欄位整個消失，上游回 400 Missing required parameter:
// 'input[40].output'。整輪對話從那則空結果之後就再也送不出去。
//
// 修正放在送出端而不是等代理修好：NR-Intern 不知道對面是誰，只能確保自己送出的
// 每一則工具結果都有可辨識的內容。
func TestChatMessagesReplaceEmptyToolResult(t *testing.T) {
	model := &Model{instructionRole: "system"}
	messages := model.messages(domain.ModelRequest{
		History: []domain.Message{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
			{Role: "tool", ToolCallID: "call_1", Content: ""},
		},
		UserPrompt: "繼續",
	})
	var tool *chatMessage
	for index := range messages {
		if messages[index].Role == "tool" {
			tool = &messages[index]
		}
	}
	if tool == nil {
		t.Fatalf("沒有產生 tool 訊息：%+v", messages)
	}
	// 只看 tool 訊息：帶著 tool_calls 的 assistant 訊息 content 為空是正常的，
	// 相容層不會把它當成工具結果。
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, `"content":""`) {
		t.Fatalf("空的工具結果會讓相容代理省略 output 欄位：%s", body)
	}
	if !strings.Contains(body, emptyToolResult) {
		t.Fatalf("空工具結果應代換成 emptyToolResult：%s", body)
	}
}

// 有內容的工具結果照原樣帶上，代換只適用於真的空的情況。
func TestChatMessagesKeepToolResultContent(t *testing.T) {
	model := &Model{instructionRole: "system"}
	messages := model.messages(domain.ModelRequest{
		History: []domain.Message{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_2", Name: "file_read"}}},
			{Role: "tool", ToolCallID: "call_2", Content: "檔案內容"},
		},
	})
	encoded, _ := json.Marshal(messages)
	body := string(encoded)
	if !strings.Contains(body, "檔案內容") {
		t.Fatalf("工具結果內容不應被改寫：%s", body)
	}
	if strings.Contains(body, emptyToolResult) {
		t.Fatalf("非空結果不該被代換：%s", body)
	}
}
