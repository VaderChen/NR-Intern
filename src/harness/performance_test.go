package harness

import (
	"strings"
	"testing"

	"AgenticService/src/domain"
	"AgenticService/src/tokens"
)

var performanceText string

// 僅使用合成內容，避免基準測試讀取實際對話或呼叫 Provider。
func BenchmarkPerformanceTruncateMiddle(b *testing.B) {
	for _, sample := range []struct{ name, unit string }{{"ASCII", "abcdefgh"}, {"CJK", "長任務記憶體最佳化"}} {
		b.Run(sample.name, func(b *testing.B) {
			value := strings.Repeat(sample.unit, 128*1024)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				performanceText = truncateMiddle(value, 4096)
			}
		})
	}
}

func BenchmarkPerformanceFitSummary(b *testing.B) {
	counter := tokens.NewHeuristicCounter()
	summary := strings.Repeat("保留目前狀態與限制。", 3200)
	messages := make([]sequencedMessage, 48)
	for i := range messages {
		messages[i].Message = domain.Message{Role: "assistant", Content: strings.Repeat("observation ", 180), ToolCalls: []domain.ToolCall{{Name: "synthetic_tool", Arguments: map[string]any{"text": strings.Repeat("argument ", 200)}}}}
	}
	definitions := make([]domain.ToolDefinition, 64)
	for i := range definitions {
		definitions[i] = domain.ToolDefinition{Name: "synthetic_tool", InputSchema: map[string]any{"type": "object", "description": strings.Repeat("schema ", 160)}}
	}
	budget := estimateContextTokens(counter, "system", messages, definitions) + 2000
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		performanceText = fitSummaryToBudget(counter, "system", summary, messages, definitions, budget)
	}
}

func BenchmarkPerformanceInstructionStream(b *testing.B) {
	b.Run("ToolJSON", func(b *testing.B) {
		value := `{"type":"tool_use","name":"synthetic_tool","input":{"text":"` + strings.Repeat("Synthetic 大型內容 ", 16384) + `"}}`
		emit := func(string) error { return nil }
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			var stream instructionTextStream
			for offset := 0; offset < len(value); offset += 256 {
				if err := stream.Push(value[offset:min(offset+256, len(value))], emit); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("OrdinaryBraces", func(b *testing.B) {
		value := "一般文字：" + strings.Repeat("{example} ", 4096)
		emit := func(string) error { return nil }
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			var stream instructionTextStream
			if err := stream.Push(value, emit); err != nil {
				b.Fatal(err)
			}
		}
	})
}
