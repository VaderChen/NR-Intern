package tokens

import (
	"AgenticService/src/domain"
	"strings"
	"testing"
)

// TestEstimateTextChineseIsNotQuarteredLikeASCII 是這個套件存在的理由：
// 舊的 characters/4 估算對中文低估 3～4 倍，使 context 預算完全失效。
func TestEstimateTextChineseIsNotQuarteredLikeASCII(t *testing.T) {
	counter := NewHeuristicCounter()
	chinese := strings.Repeat("繁體中文字", 200) // 1000 個 CJK 字元

	estimate := counter.EstimateText(chinese)

	if estimate < 900 {
		t.Fatalf("estimate = %d, want at least 900 tokens for 1000 CJK runes", estimate)
	}
	legacy := len([]rune(chinese)) / 4
	if estimate <= legacy*2 {
		t.Fatalf("estimate = %d is not meaningfully above the characters/4 assumption (%d)", estimate, legacy)
	}
}

func TestEstimateTextASCIIUsesFourCharactersPerToken(t *testing.T) {
	counter := NewHeuristicCounter()
	text := strings.Repeat("a", 400)

	if got, want := counter.EstimateText(text), 100; got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
}

func TestEstimateTextMixedContent(t *testing.T) {
	counter := NewHeuristicCounter()

	chinese := counter.EstimateText(strings.Repeat("字", 100))
	ascii := counter.EstimateText(strings.Repeat("a", 100))

	if chinese <= ascii {
		t.Fatalf("CJK estimate %d must exceed the ASCII estimate %d for the same rune count", chinese, ascii)
	}
}

func TestEstimateTextCountsFullWidthPunctuation(t *testing.T) {
	counter := NewHeuristicCounter()

	if got := counter.EstimateText("，。：；"); got < 4 {
		t.Fatalf("estimate = %d, want full-width punctuation counted like CJK", got)
	}
}

func TestEstimateMessagesIncludesToolCallArguments(t *testing.T) {
	counter := NewHeuristicCounter()
	bare := []domain.Message{{Role: "assistant"}}
	withCall := []domain.Message{{Role: "assistant", ToolCalls: []domain.ToolCall{{
		ID:        "call_1",
		Name:      "shell_exec",
		Arguments: map[string]any{"command": strings.Repeat("echo hello ", 20)},
	}}}}

	if counter.EstimateMessages(withCall) <= counter.EstimateMessages(bare) {
		t.Fatal("tool call arguments must contribute to the estimate")
	}
}

func TestEstimateToolsCountsSchemas(t *testing.T) {
	counter := NewHeuristicCounter()
	definitions := []domain.ToolDefinition{{
		Name:        "file_search",
		Description: "在 workspace 內搜尋檔案內容",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}}},
	}}

	if counter.EstimateTools(definitions) <= 0 {
		t.Fatal("tool definitions must contribute to the estimate")
	}
	if counter.EstimateTools(nil) != 0 {
		t.Fatal("no definitions must cost nothing")
	}
}

func TestZeroValueCounterFallsBackToDefaults(t *testing.T) {
	var counter HeuristicCounter

	if got, want := counter.EstimateText(strings.Repeat("a", 400)), 100; got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
}
