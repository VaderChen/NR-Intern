package bootstrap

import (
	"strings"
	"testing"
)

// 查詢類回答常常是多筆資料。表格比逐條列出好讀，也讓使用者一眼比較欄位；
// 但只有一兩筆時硬做表格反而囉嗦，提示必須同時說出這個界線。
func TestSystemPromptPrefersTablesForListAnswers(t *testing.T) {
	prompt := systemPrompt()

	for _, expected := range []string{"優先用 Markdown 表格", "不必硬做表格", "說明是否還有未列出的項目"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("system prompt is missing %q", expected)
		}
	}
}
