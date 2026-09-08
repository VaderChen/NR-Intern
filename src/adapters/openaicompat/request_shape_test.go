package openaicompat

import (
	"AgenticService/src/domain"
	"strings"
	"testing"
)

// 摘要要認得出工具結果是空的還是有值——空值被中途某一層的 omitempty 吃掉，
// 是這個專案實際踩過的事故，而上游只回報索引，光看索引無從得知那一項是什麼。
func TestDescribeChatMessagesMarksEmptyToolResults(t *testing.T) {
	model := &Model{instructionRole: "system"}
	messages := model.messages(domain.ModelRequest{
		History: []domain.Message{
			{Role: "user", Content: "做這件事"},
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_abcdefghijkl", Name: "file_read"}}},
			{Role: "tool", ToolCallID: "call_abcdefghijkl", Content: "檔案內容"},
		},
	})
	shape := DescribeChatMessages(messages)
	if !strings.Contains(shape, "calls:1/call_abcde") {
		t.Fatalf("工具呼叫沒有出現在摘要裡：%s", shape)
	}
	if !strings.Contains(shape, "out/call_abcde/set") {
		t.Fatalf("有值的工具結果應標成 set：%s", shape)
	}
}

// 摘要不能夾帶內容：診斷若把整段對話抄進日誌，就不是值得擁有的診斷。
func TestDescribeChatMessagesCarriesNoContent(t *testing.T) {
	model := &Model{instructionRole: "system"}
	messages := model.messages(domain.ModelRequest{
		SystemPrompt: "系統祕密",
		History: []domain.Message{
			{Role: "user", Content: "使用者的機密資料"},
			{Role: "assistant", Content: "助理的回答"},
		},
		UserPrompt: "這一輪的問題",
	})
	shape := DescribeChatMessages(messages)
	for _, secret := range []string{"系統祕密", "使用者的機密資料", "助理的回答", "這一輪的問題"} {
		if strings.Contains(shape, secret) {
			t.Fatalf("摘要夾帶了內容 %q：%s", secret, shape)
		}
	}
}

// 空工具結果現在會先被代換掉，所以摘要看到的是 set；真的送出空字串時
// 才會是 empty。這一條把「代換有生效」與「摘要看得出差別」一起釘住。
func TestDescribeChatMessagesReportsEmptyWhenNothingWasSubstituted(t *testing.T) {
	direct := []chatMessage{{Role: "tool", ToolCallID: "call_1", Content: ""}}
	if shape := DescribeChatMessages(direct); !strings.Contains(shape, "out/call_1/empty") {
		t.Fatalf("空的工具結果應標成 empty：%s", shape)
	}
	model := &Model{instructionRole: "system"}
	substituted := model.messages(domain.ModelRequest{
		History: []domain.Message{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_1", Name: "file_read"}}},
			{Role: "tool", ToolCallID: "call_1", Content: ""},
		},
	})
	if shape := DescribeChatMessages(substituted); !strings.Contains(shape, "out/call_1/set") {
		t.Fatalf("代換後送出的工具結果不該是空的：%s", shape)
	}
}
