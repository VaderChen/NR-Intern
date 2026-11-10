package tokens

import (
	"strings"
	"testing"
)

func BenchmarkFunctionTokenWeight(b *testing.B) {
	for _, input := range []struct{ name, value string }{
		{"ASCII", strings.Repeat("synthetic tool output ", 4096)},
		{"CJK", strings.Repeat("合成文字工具輸出", 4096)},
		{"Mixed", strings.Repeat("synthetic 合成🙂é output ", 4096)},
	} {
		b.Run(input.name, func(b *testing.B) {
			counter := NewHeuristicCounter()
			b.ReportAllocs()
			for b.Loop() {
				_ = counter.EstimateText(input.value)
			}
		})
	}
}
