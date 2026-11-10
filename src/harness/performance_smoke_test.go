package harness

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"AgenticService/src/domain"
	"AgenticService/src/tokens"
)

type performanceCountingCounter struct {
	*tokens.HeuristicCounter
	messageCalls, toolCalls int
}

func (c *performanceCountingCounter) EstimateMessages(values []domain.Message) int {
	c.messageCalls++
	return c.HeuristicCounter.EstimateMessages(values)
}

func (c *performanceCountingCounter) EstimateTools(values []domain.ToolDefinition) int {
	c.toolCalls++
	return c.HeuristicCounter.EstimateTools(values)
}

func TestFitSummaryEstimateReuseSmoke(t *testing.T) {
	plain := tokens.NewHeuristicCounter()
	counter := &performanceCountingCounter{HeuristicCounter: plain}
	messages := []sequencedMessage{{Message: domain.Message{Role: "user", Content: "不可截斷的目前訊息"}}}
	definitions := []domain.ToolDefinition{{Name: "synthetic", InputSchema: map[string]any{"type": "object"}}}
	summary := strings.Repeat("歷史限制與已驗證的結果。", 100)
	for _, budget := range []int{0, 100, 1000, 10_000} {
		counter.messageCalls, counter.toolCalls = 0, 0
		got := fitSummaryToBudget(counter, "system", summary, messages, definitions, budget)
		if counter.messageCalls != 1 || counter.toolCalls != 1 {
			t.Fatalf("重複估算固定輸入：message=%d tool=%d", counter.messageCalls, counter.toolCalls)
		}
		// 用原本每次完整估算的二分搜尋比對結果與預算邊界。
		low, high, want := 0, utf8.RuneCountInString(summary), ""
		for low <= high {
			middle := low + (high-low)/2
			candidate := ""
			if middle > 0 {
				candidate = truncateMiddle(summary, middle)
			}
			if estimateContextTokens(plain, withSummary("system", candidate), messages, definitions) <= budget {
				want, low = candidate, middle+1
			} else {
				high = middle - 1
			}
		}
		if got != want {
			t.Fatalf("budget=%d 摘要結果不同", budget)
		}
	}
}

func TestInstructionStreamPerformanceCompatibilitySmoke(t *testing.T) {
	for _, value := range []string{
		"一般文字：" + strings.Repeat("{example} ", 200),
		`{"result":"一般 JSON，非工具"}`,
		"\n  ```text\ncode example\n```",
		"to=ordinary_example",
		"一般文字：{ \"TYPE\" : \"tool_use\",\"name\":\"private\"}",
		"一般文字：{\"type\":\"toolbox\"}",
	} {
		for _, size := range []int{1, 2, 7, 256, len(value)} {
			var stream instructionTextStream
			var output strings.Builder
			emit := func(delta string) error { output.WriteString(delta); return nil }
			for start := 0; start < len(value); start += size {
				if err := stream.Push(value[start:min(start+size, len(value))], emit); err != nil {
					t.Fatal(err)
				}
			}
			if err := stream.Finish(value, emit); err != nil {
				t.Fatal(err)
			}
			want := value
			if strings.Contains(value, `"TYPE"`) {
				want = "一般文字："
			}
			if output.String() != want {
				t.Fatalf("分段長度=%d got=%q want=%q", size, output.String(), want)
			}
		}
	}
}

func TestInstructionStreamDoesNotRetainToolPayloadSmoke(t *testing.T) {
	var stream instructionTextStream
	emit := func(string) error { t.Fatal("工具 JSON 洩漏"); return nil }
	for _, delta := range []string{" ", "{", strings.Repeat("大型工具參數", 200_000)} {
		if err := stream.Push(delta, emit); err != nil {
			t.Fatal(err)
		}
	}
	if stream.buffer.Len() != 0 || !stream.holding || stream.Released() {
		t.Fatal("已識別的工具指令不應額外保存完整本文")
	}
	cause := errors.New("synthetic sink cancellation")
	stream = instructionTextStream{}
	if err := stream.Push("一般{內容}", func(string) error { return cause }); !errors.Is(err, cause) {
		t.Fatalf("sink 錯誤未傳回：%v", err)
	}
}
